package sandbox

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fastclaw-ai/fastclaw/internal/store"
)

// newTestBindingStore opens an in-memory sqlite store with migrations
// applied — the same shape internal/store's own tests use, reimplemented
// here since that package's openTestDB helper is unexported.
func newTestBindingStore(t *testing.T) store.Store {
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

// newMockE2BServer fakes just the one endpoint KillForUser/Close need:
// DELETE /sandboxes/{id}. Always 204s — status code doesn't matter to
// deleteE2BSandbox (see its doc comment), only transport-level errors
// do, which is what failingRoundTripper below simulates instead.
func newMockE2BServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/sandboxes/") {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// failingRoundTripper simulates a transport-level failure (connection
// refused / DNS failure / etc — NOT an HTTP error status) for requests
// whose path contains failFor, and proxies everything else to inner.
// This is the only way to exercise KillForUser's "failed" branch: E2B's
// own DELETE handling (deleteE2BSandbox, mirroring the pre-existing
// Close()) intentionally never inspects the response status, only
// client.Do's error return.
type failingRoundTripper struct {
	inner   http.RoundTripper
	failFor string
}

func (f *failingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if f.failFor != "" && strings.Contains(req.URL.Path, f.failFor) {
		return nil, fmt.Errorf("simulated transport failure for %s", req.URL.Path)
	}
	return f.inner.RoundTrip(req)
}

// withMockE2BBaseURL points the package-level e2bBaseURL at srv for the
// duration of the test and restores the original value on cleanup —
// e2bBaseURL is a var specifically so tests can do this instead of
// hitting the real E2B API.
func withMockE2BBaseURL(t *testing.T, srv *httptest.Server) {
	t.Helper()
	orig := e2bBaseURL
	e2bBaseURL = srv.URL
	t.Cleanup(func() { e2bBaseURL = orig })
}

func TestE2BExecutorPoolKillForUserEnumeratesAndKills(t *testing.T) {
	srv := newMockE2BServer(t)
	withMockE2BBaseURL(t, srv)

	st := newTestBindingStore(t)
	pool := NewE2BExecutorPool("test-api-key", "base", "", 0)
	pool.SetBindingStore(st)
	pool.httpClient = &http.Client{Transport: &failingRoundTripper{inner: http.DefaultTransport, failFor: "sb-fail"}}

	ctx := context.Background()
	bindings := []*store.SandboxBindingRecord{
		{SandboxID: "sb-ok-1", UserID: "u_target", AgentID: "agt_1", ExecutionRef: "agt_1:s:sess_1"},
		{SandboxID: "sb-ok-2", UserID: "u_target", AgentID: "agt_2", ExecutionRef: "agt_2:s:sess_2"},
		{SandboxID: "sb-fail-1", UserID: "u_target", AgentID: "agt_3", ExecutionRef: "agt_3:s:sess_3"},
		{SandboxID: "sb-other-user", UserID: "u_other", AgentID: "agt_4", ExecutionRef: "agt_4:s:sess_4"},
	}
	for _, b := range bindings {
		if err := st.SaveSandboxBinding(ctx, b); err != nil {
			t.Fatalf("seed binding %s: %v", b.SandboxID, err)
		}
	}

	// Pre-populate the in-memory cache for one of the "to be killed"
	// sandboxes to prove KillForUser also drops the pool's own map
	// entry (not just the durable row) — a racing Get() on the same
	// execution_ref must not hand back a reference to a sandbox we just
	// told E2B to destroy.
	pool.executors["agt_1:s:sess_1"] = &E2BExecutor{apiKey: "test-api-key", sandboxID: "sb-ok-1", client: pool.httpClient}

	killed, failed, err := pool.KillForUser(ctx, "u_target")
	if err != nil {
		t.Fatalf("KillForUser: %v", err)
	}

	killedSet := map[string]bool{}
	for _, id := range killed {
		killedSet[id] = true
	}
	if !killedSet["sb-ok-1"] || !killedSet["sb-ok-2"] {
		t.Errorf("killed = %v, want sb-ok-1 and sb-ok-2", killed)
	}
	if killedSet["sb-fail-1"] {
		t.Errorf("sb-fail-1 reported killed, want it in failed")
	}
	if killedSet["sb-other-user"] {
		t.Errorf("sb-other-user (belongs to u_other) was killed by a u_target request")
	}

	if len(failed) != 1 || failed[0].SandboxID != "sb-fail-1" {
		t.Fatalf("failed = %+v, want exactly one entry for sb-fail-1", failed)
	}
	if failed[0].Error == "" {
		t.Errorf("failed[0].Error is empty, want the transport error message")
	}

	// In-memory cache entry for the killed sandbox is gone.
	if _, ok := pool.executors["agt_1:s:sess_1"]; ok {
		t.Errorf("pool.executors still holds agt_1:s:sess_1 after KillForUser killed sb-ok-1")
	}

	// Successful kills clear their binding row; the failed one keeps
	// its row so a retried KillForUser (or a natural recreate) gets
	// another chance at it.
	remaining, err := st.ListSandboxBindingsByUser(ctx, "u_target")
	if err != nil {
		t.Fatalf("ListSandboxBindingsByUser: %v", err)
	}
	if len(remaining) != 1 || remaining[0].SandboxID != "sb-fail-1" {
		t.Fatalf("remaining u_target bindings = %+v, want only sb-fail-1", remaining)
	}

	// u_other's binding was never touched by a u_target erase.
	otherRemaining, err := st.ListSandboxBindingsByUser(ctx, "u_other")
	if err != nil {
		t.Fatalf("ListSandboxBindingsByUser(u_other): %v", err)
	}
	if len(otherRemaining) != 1 || otherRemaining[0].SandboxID != "sb-other-user" {
		t.Fatalf("u_other bindings = %+v, want sb-other-user untouched", otherRemaining)
	}
}

func TestE2BExecutorPoolKillForUserNoBindingStoreIsNoop(t *testing.T) {
	pool := NewE2BExecutorPool("test-api-key", "base", "", 0)
	killed, failed, err := pool.KillForUser(context.Background(), "u_target")
	if err != nil || killed != nil || failed != nil {
		t.Fatalf("KillForUser with no binding store = (%v, %v, %v), want (nil, nil, nil)", killed, failed, err)
	}
}

func TestE2BExecutorPoolKillForUserNoBindingsIsEmptyNotError(t *testing.T) {
	st := newTestBindingStore(t)
	pool := NewE2BExecutorPool("test-api-key", "base", "", 0)
	pool.SetBindingStore(st)
	killed, failed, err := pool.KillForUser(context.Background(), "u_nobody")
	if err != nil {
		t.Fatalf("KillForUser: %v", err)
	}
	if len(killed) != 0 || len(failed) != 0 {
		t.Fatalf("KillForUser for a user with no bindings = (%v, %v), want both empty", killed, failed)
	}
}

// TestE2BExecutorPoolReleaseClearsBinding covers the OTHER lifecycle hook
// the erase-user cascade depends on for accuracy: a normal session-end
// Release() must clear the binding row too, so a later KillForUser
// doesn't report (or attempt to re-kill) a sandbox this same process
// already tore down through the ordinary path.
func TestE2BExecutorPoolReleaseClearsBinding(t *testing.T) {
	srv := newMockE2BServer(t)
	withMockE2BBaseURL(t, srv)

	st := newTestBindingStore(t)
	pool := NewE2BExecutorPool("test-api-key", "base", "", 0)
	pool.SetBindingStore(st)

	ctx := context.Background()
	if err := st.SaveSandboxBinding(ctx, &store.SandboxBindingRecord{
		SandboxID: "sb-release-1", UserID: "u_1", AgentID: "agt_1", ExecutionRef: "agt_1:s:sess_1",
	}); err != nil {
		t.Fatalf("seed binding: %v", err)
	}
	pool.executors["agt_1:s:sess_1"] = &E2BExecutor{apiKey: "test-api-key", sandboxID: "sb-release-1", client: http.DefaultClient}

	if err := pool.Release("agt_1", "", "sess_1"); err != nil {
		t.Fatalf("Release: %v", err)
	}

	if _, ok := pool.executors["agt_1:s:sess_1"]; ok {
		t.Errorf("pool.executors still holds the key after Release")
	}
	remaining, err := st.ListSandboxBindingsByUser(ctx, "u_1")
	if err != nil {
		t.Fatalf("ListSandboxBindingsByUser: %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("bindings for u_1 after Release = %+v, want none", remaining)
	}
}

// TestE2BExecutorPoolRecordBindingRequiresContextUserID guards the
// no-op-when-untagged half of the write path Get() calls after every
// successful creation: callers that never tagged ctx via
// sandbox.WithUserID (cron flushes, admin reload triggers) must not
// produce a binding row with an empty user_id — that would make
// KillForUser("") (which nobody should call, but defense in depth)
// enumerate orphaned sandboxes as if "" were a real user.
func TestE2BExecutorPoolRecordBindingRequiresContextUserID(t *testing.T) {
	st := newTestBindingStore(t)
	pool := NewE2BExecutorPool("test-api-key", "base", "", 0)
	pool.SetBindingStore(st)
	ex := &E2BExecutor{apiKey: "test-api-key", sandboxID: "sb-untagged"}

	pool.recordBinding(context.Background(), ex, "agt_1", "agt_1:s:sess_1")

	got, err := st.ListSandboxBindingsByUser(context.Background(), "")
	if err != nil {
		t.Fatalf("ListSandboxBindingsByUser(\"\"): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("recordBinding wrote a row despite an untagged ctx: %+v", got)
	}

	tagged := WithUserID(context.Background(), "u_tagged")
	pool.recordBinding(tagged, ex, "agt_1", "agt_1:s:sess_1")
	got, err = st.ListSandboxBindingsByUser(context.Background(), "u_tagged")
	if err != nil {
		t.Fatalf("ListSandboxBindingsByUser(u_tagged): %v", err)
	}
	if len(got) != 1 || got[0].SandboxID != "sb-untagged" {
		t.Fatalf("recordBinding with a tagged ctx = %+v, want one row for sb-untagged", got)
	}
}
