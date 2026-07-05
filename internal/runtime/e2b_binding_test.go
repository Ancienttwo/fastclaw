// Added by SalesKo: gatekeeper-mandated regression lock for the HIGH
// finding on salesko/erase-user-cascade — the /runtime/* HTTP entrypoints
// only stamp config.WithUserID on r.Context() (a different ctx key than
// sandbox.WithUserID), so Manager.Exec/Up/Wake/Get/Logs used to forward
// an untagged ctx straight into pool.Get, and the E2B pool's binding
// write + create-payload metadata both silently no-op'd for every
// sandbox this package creates.

package runtime

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fastclaw-ai/fastclaw/internal/sandbox"
	"github.com/fastclaw-ai/fastclaw/internal/store"
)

// newRuntimeTestStore opens an in-memory sqlite store with migrations
// applied — same shape as the equivalent helpers in internal/store,
// internal/sandbox, and internal/api's own test suites.
func newRuntimeTestStore(t *testing.T) store.Store {
	t.Helper()
	db, err := store.NewDBStore("sqlite", "file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate test store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// connectFrame wraps payload in the same [1 byte flags][4 byte BE
// length][payload] envelope E2B's envd Connect-protocol endpoint uses
// (see sandbox.connectEnvelope, unexported — replicated here since this
// test lives in a different package).
func connectFrame(payload []byte) []byte {
	buf := make([]byte, 5+len(payload))
	buf[0] = 0 // flags: no compression, not end-of-stream
	binary.BigEndian.PutUint32(buf[1:5], uint32(len(payload)))
	copy(buf[5:], payload)
	return buf
}

// connectExecResponse fakes envd's response to a process.Process/Start
// call: one data frame carrying the given stdout, followed by an end
// frame reporting a clean exit.
func connectExecResponse(stdout string) []byte {
	dataPayload, _ := json.Marshal(map[string]any{
		"event": map[string]any{
			"data": map[string]any{"stdout": base64.StdEncoding.EncodeToString([]byte(stdout))},
		},
	})
	endPayload, _ := json.Marshal(map[string]any{
		"event": map[string]any{
			"end": map[string]any{"exited": true, "status": "exit status 0"},
		},
	})
	out := connectFrame(dataPayload)
	out = append(out, connectFrame(endPayload)...)
	return out
}

// connectSuccessResponse fakes envd's response for the plain "did this
// run without erroring" commands: Hydrate's mkdir+chown,
// verifyWorkspaceWritable's touch+rm (which explicitly checks for "ok"
// in the output), warmupCamoufoxDaemon's camoufox-open, and a one-shot
// Exec/poolExec call — none of these care about the actual command that
// was sent, just that it exits 0.
func connectSuccessResponse() []byte {
	return connectExecResponse("ok\n")
}

// execCommand extracts the shell command envd's process.Process/Start
// request is asking to run, by unwrapping the same Connect envelope
// connectFrame builds (see sandbox.connectEnvelope / execOnce, both
// unexported — replicated here since this test lives in a different
// package). Returns "" on any parse failure (caller falls back to the
// generic response in that case).
func execCommand(body []byte) string {
	if len(body) < 5 {
		return ""
	}
	length := binary.BigEndian.Uint32(body[1:5])
	if uint32(len(body)-5) < length {
		return ""
	}
	var msg struct {
		Process struct {
			Args []string `json:"args"`
		} `json:"process"`
	}
	if err := json.Unmarshal(body[5:5+length], &msg); err != nil {
		return ""
	}
	if len(msg.Process.Args) >= 2 {
		return msg.Process.Args[1]
	}
	return ""
}

// execHandlerResponse is the generic envd exec response shared by both
// regression tests below: "200" for a curl port-probe (so
// startDevServerExec / waitForDevServerExec see the dev server as
// already up and return immediately — these tests aren't validating
// dev-server-readiness polling, just that Up()/Exec() tag ctx correctly
// before touching the pool), "ok\n" for everything else (Hydrate's
// mkdir+chown, verifyWorkspaceWritable's touch+rm which explicitly
// checks for "ok" in the output, warmupCamoufoxDaemon, and a plain
// one-shot Exec command).
func execHandlerResponse(body []byte) []byte {
	if strings.Contains(execCommand(body), "curl") {
		return connectExecResponse("200")
	}
	return connectSuccessResponse()
}

// TestManagerExecTagsCtxSoE2BBindingAndMetadataAreRecorded goes through
// the exact call shape the real HTTP handler uses (internal/setup/
// handlers_runtime.go passes r.Context() straight through, with no
// sandbox.WithUserID tag — that's the auth middleware's job under a
// DIFFERENT ctx key, and it never happens before reaching Manager), then
// asserts both halves the erase-user cascade depends on:
//  1. the E2B create payload carries the fastclaw_user_id/agent_id
//     metadata tag, and
//  2. a durable sandbox_bindings row exists for the user — and
//     KillForUser can actually find and kill it.
//
// Before the fix, both of these were silently empty because ctx never
// carried the tag by the time it reached pool.Get.
func TestManagerExecTagsCtxSoE2BBindingAndMetadataAreRecorded(t *testing.T) {
	var createPayload map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/sandboxes":
			_ = json.NewDecoder(r.Body).Decode(&createPayload)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"sandboxID":"sb-runtime-1","envdAccessToken":"tok"}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/process.Process/Start"):
			body, _ := io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/connect+json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(execHandlerResponse(body))
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/sandboxes/"):
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	restore := sandbox.SetE2BEndpointsForTest(srv.URL)
	defer restore()

	st := newRuntimeTestStore(t)
	pool := sandbox.NewE2BExecutorPool("test-api-key", "base", "", time.Minute)
	pool.SetBindingStore(st)

	mgr := NewManager(st, t.TempDir(), "", nil, "", "e2b", pool)

	// No sandbox.WithUserID tag on this ctx — simulating exactly what
	// internal/setup/handlers_runtime.go passes today (r.Context(),
	// tagged only with config.WithUserID by the auth middleware).
	ctx := context.Background()
	out, err := mgr.Exec(ctx, "u_runtime_1", "agt_runtime_1", "proj_1", "", "echo hi", 5*time.Second)
	if err != nil {
		t.Fatalf("Manager.Exec: %v (out=%s)", err, out)
	}

	// 1. Create payload carries the metadata tag.
	meta, ok := createPayload["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("create payload missing metadata field entirely: %+v", createPayload)
	}
	if meta["fastclaw_user_id"] != "u_runtime_1" || meta["agent_id"] != "agt_runtime_1" {
		t.Errorf("metadata = %+v, want fastclaw_user_id=u_runtime_1 agent_id=agt_runtime_1", meta)
	}

	// 2. A durable binding row exists for the user.
	bindings, err := st.ListSandboxBindingsByUser(context.Background(), "u_runtime_1")
	if err != nil {
		t.Fatalf("ListSandboxBindingsByUser: %v", err)
	}
	if len(bindings) != 1 || bindings[0].SandboxID != "sb-runtime-1" {
		t.Fatalf("bindings for u_runtime_1 = %+v, want exactly one row for sb-runtime-1", bindings)
	}
	if bindings[0].AgentID != "agt_runtime_1" {
		t.Errorf("binding.AgentID = %q, want agt_runtime_1", bindings[0].AgentID)
	}

	// 3. And the admin erase-user cascade's KillForUser can actually
	// find + kill it — the end-to-end proof that the fix closes the gap,
	// not just that a row happens to exist.
	killed, failed, err := pool.KillForUser(context.Background(), "u_runtime_1")
	if err != nil {
		t.Fatalf("KillForUser: %v", err)
	}
	if len(failed) != 0 {
		t.Fatalf("KillForUser failed = %+v, want none", failed)
	}
	if len(killed) != 1 || killed[0] != "sb-runtime-1" {
		t.Fatalf("KillForUser killed = %v, want [sb-runtime-1]", killed)
	}
}

// TestManagerUpViaPoolTagsCtx covers the Up entrypoint specifically
// (Wake delegates to Up; Get/Logs are covered at the unit level by
// inspection since they don't need a live dev server) — proves the
// pool-backed preview path (upViaPool) also gets a bound + tagged
// sandbox, not just the one-shot Exec path.
func TestManagerUpViaPoolTagsCtx(t *testing.T) {
	var createPayload map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/sandboxes":
			_ = json.NewDecoder(r.Body).Decode(&createPayload)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"sandboxID":"sb-runtime-2","envdAccessToken":"tok"}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/process.Process/Start"):
			body, _ := io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/connect+json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(execHandlerResponse(body))
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/sandboxes/"):
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	restore := sandbox.SetE2BEndpointsForTest(srv.URL)
	defer restore()

	st := newRuntimeTestStore(t)
	pool := sandbox.NewE2BExecutorPool("test-api-key", "base", "", time.Minute)
	pool.SetBindingStore(st)

	mgr := NewManager(st, t.TempDir(), "", nil, "", "e2b", pool)
	mgr.RegisterTemplate("vite-react", TemplateSpec{
		DevPort: 5173,
		// No scaffold needed for this test: the probe for "needs
		// scaffold" always succeeds against the mock envd anyway, but
		// skipping ScaffoldCmd keeps the test to exactly the calls this
		// assertion cares about.
		DevCmd: "true",
	})

	ctx := context.Background() // untagged, same as the real HTTP handler
	rec, err := mgr.Up(ctx, "u_runtime_2", "agt_runtime_2", "proj_2", "", "vite-react")
	if err != nil {
		t.Fatalf("Manager.Up: %v", err)
	}
	if rec.Status != StatusRunning {
		t.Fatalf("rec.Status = %q, want running", rec.Status)
	}

	meta, ok := createPayload["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("create payload missing metadata field entirely: %+v", createPayload)
	}
	if meta["fastclaw_user_id"] != "u_runtime_2" || meta["agent_id"] != "agt_runtime_2" {
		t.Errorf("metadata = %+v, want fastclaw_user_id=u_runtime_2 agent_id=agt_runtime_2", meta)
	}

	bindings, err := st.ListSandboxBindingsByUser(context.Background(), "u_runtime_2")
	if err != nil {
		t.Fatalf("ListSandboxBindingsByUser: %v", err)
	}
	if len(bindings) != 1 || bindings[0].SandboxID != "sb-runtime-2" {
		t.Fatalf("bindings for u_runtime_2 = %+v, want exactly one row for sb-runtime-2", bindings)
	}
}
