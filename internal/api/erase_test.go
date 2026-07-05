package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fastclaw-ai/fastclaw/internal/auth"
	"github.com/fastclaw-ai/fastclaw/internal/sandbox"
	"github.com/fastclaw-ai/fastclaw/internal/store"
	"github.com/fastclaw-ai/fastclaw/internal/users"
)

// newEraseTestStore opens an in-memory sqlite store with migrations
// applied, mirroring internal/store's own test helper (unexported there,
// so reimplemented via the public constructor).
func newEraseTestStore(t *testing.T) store.Store {
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

// stubSandboxPool is a minimal sandbox.ExecutorPool + sandbox.UserKiller
// double. Only KillForUser's behavior is configurable — the erase
// handler never calls Get/Release/CloseAll/Backend.
type stubSandboxPool struct {
	killFunc func(ctx context.Context, userID string) ([]string, []sandbox.KillFailure, error)
}

func (p *stubSandboxPool) Get(ctx context.Context, agentID, projectID, sessionID string) (sandbox.Executor, error) {
	return nil, errors.New("stubSandboxPool: Get not implemented")
}
func (p *stubSandboxPool) Release(agentID, projectID, sessionID string) error { return nil }
func (p *stubSandboxPool) CloseAll()                                          {}
func (p *stubSandboxPool) Backend() string                                    { return "stub" }
func (p *stubSandboxPool) KillForUser(ctx context.Context, userID string) ([]string, []sandbox.KillFailure, error) {
	if p.killFunc != nil {
		return p.killFunc(ctx, userID)
	}
	return nil, nil, nil
}

var _ sandbox.ExecutorPool = (*stubSandboxPool)(nil)
var _ sandbox.UserKiller = (*stubSandboxPool)(nil)

// eraseRequest builds a POST /v1/users/{externalId}/erase request carrying
// the given identity, with the path value pre-set the way the real mux
// would populate it from the "{externalId}" route pattern.
func eraseRequest(ident auth.Identity, externalID string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/v1/users/"+externalID+"/erase", nil)
	req.SetPathValue("externalId", externalID)
	return req.WithContext(auth.WithIdentity(context.Background(), ident))
}

func decodeReceipt(t *testing.T, rec *httptest.ResponseRecorder) eraseReceipt {
	t.Helper()
	var out eraseReceipt
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode receipt: %v (body=%s)", err, rec.Body.String())
	}
	return out
}

func TestHandleEraseAppUserRejectsNonAPIKeyAuth(t *testing.T) {
	srv := &Server{store: newEraseTestStore(t)}
	ident := auth.Identity{UserID: "u_owner", Role: users.RoleUser, AuthMethod: "session"}
	rec := httptest.NewRecorder()
	srv.HandleEraseAppUser(rec, eraseRequest(ident, "ext-1"))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestHandleEraseAppUserRejectsSwitchedAppUserIdentity(t *testing.T) {
	srv := &Server{store: newEraseTestStore(t)}
	// Simulates a caller that sent X-Fastclaw-End-User: the auth
	// middleware would have already switched ident.Role to app_user
	// before this handler ever runs.
	ident := auth.Identity{UserID: "u_some_app_user", Role: users.RoleAppUser, AuthMethod: "apikey", APIKeyID: "ak_1"}
	rec := httptest.NewRecorder()
	srv.HandleEraseAppUser(rec, eraseRequest(ident, "ext-1"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestHandleEraseAppUserUnknownExternalID404s(t *testing.T) {
	srv := &Server{store: newEraseTestStore(t)}
	ident := auth.Identity{UserID: "u_owner", Role: users.RoleUser, AuthMethod: "apikey", APIKeyID: "ak_1"}
	rec := httptest.NewRecorder()
	srv.HandleEraseAppUser(rec, eraseRequest(ident, "never-existed"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleEraseAppUserForeignOwnerExternalID404s(t *testing.T) {
	st := newEraseTestStore(t)
	srv := &Server{store: st}
	ctx := context.Background()

	// externalId "ext-1" belongs to owner_A; owner_B must not be able to
	// erase it by guessing the externalId.
	if err := st.CreateUser(ctx, &store.UserRecord{
		ID: "u_appA", Username: "u_appA", Email: "appA@test.local",
		Role: users.RoleAppUser, Status: users.StatusActive, AgentQuota: -1,
		OwnerUserID: "owner_A", ExternalID: "ext-1",
	}); err != nil {
		t.Fatalf("seed app_user: %v", err)
	}

	ident := auth.Identity{UserID: "owner_B", Role: users.RoleUser, AuthMethod: "apikey", APIKeyID: "ak_b"}
	rec := httptest.NewRecorder()
	srv.HandleEraseAppUser(rec, eraseRequest(ident, "ext-1"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (foreign owner must look identical to not-found), body=%s", rec.Code, rec.Body.String())
	}

	// The row must still exist — a 404 must not have deleted anything.
	if _, err := st.GetUser(ctx, "u_appA"); err != nil {
		t.Fatalf("app_user row should survive a foreign-owner erase attempt, got err=%v", err)
	}
}

func TestHandleEraseAppUserSuccessAndIdempotentReplay(t *testing.T) {
	st := newEraseTestStore(t)
	ctx := context.Background()

	if err := st.CreateUser(ctx, &store.UserRecord{
		ID: "u_app1", Username: "u_app1", Email: "app1@test.local",
		Role: users.RoleAppUser, Status: users.StatusActive, AgentQuota: -1,
		OwnerUserID: "owner_A", ExternalID: "ext-1",
	}); err != nil {
		t.Fatalf("seed app_user: %v", err)
	}
	if err := st.SaveAgent(ctx, &store.AgentRecord{ID: "agt_1", UserID: "u_app1", Name: "agent"}); err != nil {
		t.Fatalf("seed agent: %v", err)
	}

	var killForUserCalledWith string
	pool := &stubSandboxPool{killFunc: func(ctx context.Context, userID string) ([]string, []sandbox.KillFailure, error) {
		killForUserCalledWith = userID
		return []string{"sb-1", "sb-2"}, nil, nil
	}}
	srv := &Server{store: st, sandboxPool: pool}
	ident := auth.Identity{UserID: "owner_A", Role: users.RoleUser, AuthMethod: "apikey", APIKeyID: "ak_a"}

	rec := httptest.NewRecorder()
	srv.HandleEraseAppUser(rec, eraseRequest(ident, "ext-1"))
	if rec.Code != http.StatusOK {
		t.Fatalf("first erase status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	receipt := decodeReceipt(t, rec)
	if !receipt.Erased || receipt.UserID != "u_app1" || receipt.ExternalID != "ext-1" {
		t.Fatalf("receipt = %+v, want erased=true userId=u_app1 externalId=ext-1", receipt)
	}
	if receipt.DB.Agents != 1 {
		t.Errorf("receipt.DB.Agents = %d, want 1", receipt.DB.Agents)
	}
	if len(receipt.Sandboxes.Killed) != 2 {
		t.Errorf("receipt.Sandboxes.Killed = %v, want [sb-1 sb-2]", receipt.Sandboxes.Killed)
	}
	if killForUserCalledWith != "u_app1" {
		t.Errorf("KillForUser called with userID=%q, want u_app1", killForUserCalledWith)
	}

	// Rows are actually gone.
	if _, err := st.GetUser(ctx, "u_app1"); err == nil {
		t.Fatal("u_app1 row should be gone after erase")
	}
	if _, err := st.GetAgent(ctx, "agt_1"); err == nil {
		t.Fatal("agt_1 row should be gone after erase")
	}

	// Replay: GetUserByExternal can no longer resolve the row, but the
	// tombstone recorded at the end of the first call makes this an
	// idempotent 200 with a same-shape zero-value receipt, not a 404.
	killForUserCalledWith = ""
	rec2 := httptest.NewRecorder()
	srv.HandleEraseAppUser(rec2, eraseRequest(ident, "ext-1"))
	if rec2.Code != http.StatusOK {
		t.Fatalf("replay status = %d, want 200, body=%s", rec2.Code, rec2.Body.String())
	}
	replay := decodeReceipt(t, rec2)
	if !replay.Erased || replay.UserID != "u_app1" || replay.ExternalID != "ext-1" {
		t.Fatalf("replay receipt = %+v, want erased=true userId=u_app1 externalId=ext-1", replay)
	}
	if replay.DB != (store.DeleteUserCounts{}) {
		t.Errorf("replay receipt.DB = %+v, want all-zero", replay.DB)
	}
	if len(replay.Sandboxes.Killed) != 0 || len(replay.Sandboxes.Failed) != 0 {
		t.Errorf("replay receipt.Sandboxes = %+v, want empty", replay.Sandboxes)
	}
	if len(replay.Disk.DirsRemoved) != 0 {
		t.Errorf("replay receipt.Disk = %+v, want empty", replay.Disk)
	}
	// The replay must NOT re-invoke the sandbox kill step — there's
	// nothing left to kill, and doing so would be pointless E2B traffic
	// on every retried erase call.
	if killForUserCalledWith != "" {
		t.Errorf("KillForUser was called again on replay (userID=%q), want it skipped", killForUserCalledWith)
	}
}

func TestHandleEraseAppUserPartialSandboxFailureReturns207(t *testing.T) {
	st := newEraseTestStore(t)
	ctx := context.Background()
	if err := st.CreateUser(ctx, &store.UserRecord{
		ID: "u_app2", Username: "u_app2", Email: "app2@test.local",
		Role: users.RoleAppUser, Status: users.StatusActive, AgentQuota: -1,
		OwnerUserID: "owner_B", ExternalID: "ext-2",
	}); err != nil {
		t.Fatalf("seed app_user: %v", err)
	}

	pool := &stubSandboxPool{killFunc: func(ctx context.Context, userID string) ([]string, []sandbox.KillFailure, error) {
		return []string{"sb-ok"}, []sandbox.KillFailure{{SandboxID: "sb-bad", Error: "boom"}}, nil
	}}
	srv := &Server{store: st, sandboxPool: pool}
	ident := auth.Identity{UserID: "owner_B", Role: users.RoleUser, AuthMethod: "apikey", APIKeyID: "ak_b"}

	rec := httptest.NewRecorder()
	srv.HandleEraseAppUser(rec, eraseRequest(ident, "ext-2"))
	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("status = %d, want 207, body=%s", rec.Code, rec.Body.String())
	}
	receipt := decodeReceipt(t, rec)
	if !receipt.Erased {
		t.Errorf("receipt.Erased = false, want true (DB cascade still ran despite partial sandbox failure)")
	}
	if len(receipt.Sandboxes.Failed) != 1 || receipt.Sandboxes.Failed[0].SandboxID != "sb-bad" {
		t.Errorf("receipt.Sandboxes.Failed = %+v, want [{sb-bad boom}]", receipt.Sandboxes.Failed)
	}
	// The user row must actually be gone — a 207 reports a partial
	// SANDBOX failure, not a partial DB cascade.
	if _, err := st.GetUser(ctx, "u_app2"); err == nil {
		t.Fatal("u_app2 row should be gone even though sandbox kill partially failed")
	}
}

func TestHandleEraseAppUserNoSandboxPoolStillErases(t *testing.T) {
	st := newEraseTestStore(t)
	ctx := context.Background()
	if err := st.CreateUser(ctx, &store.UserRecord{
		ID: "u_app3", Username: "u_app3", Email: "app3@test.local",
		Role: users.RoleAppUser, Status: users.StatusActive, AgentQuota: -1,
		OwnerUserID: "owner_C", ExternalID: "ext-3",
	}); err != nil {
		t.Fatalf("seed app_user: %v", err)
	}
	// No SetSandboxPool call — mirrors sandboxing disabled entirely.
	srv := &Server{store: st}
	ident := auth.Identity{UserID: "owner_C", Role: users.RoleUser, AuthMethod: "apikey", APIKeyID: "ak_c"}

	rec := httptest.NewRecorder()
	srv.HandleEraseAppUser(rec, eraseRequest(ident, "ext-3"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if _, err := st.GetUser(ctx, "u_app3"); err == nil {
		t.Fatal("u_app3 row should be gone")
	}
}
