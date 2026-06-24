package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	agentpkg "github.com/fastclaw-ai/fastclaw/internal/agent"
	"github.com/fastclaw-ai/fastclaw/internal/auth"
	"github.com/fastclaw-ai/fastclaw/internal/bus"
	"github.com/fastclaw-ai/fastclaw/internal/config"
	"github.com/fastclaw-ai/fastclaw/internal/users"
)

type testAgentInjectorResolver struct {
	space       *UserSpaceView
	ensureUser  string
	ensureAgent string
}

func (r *testAgentInjectorResolver) UserSpaceFor(userID string) (*UserSpaceView, error) {
	return r.space, nil
}

func (r *testAgentInjectorResolver) LocalAgentManager() *agentpkg.Manager { return nil }

func (r *testAgentInjectorResolver) IsCloudMode() bool { return true }

func (r *testAgentInjectorResolver) EnsureAgent(ctx context.Context, userID, agentID string) error {
	r.ensureUser = userID
	r.ensureAgent = agentID
	home, err := config.HomeDir()
	if err != nil {
		return err
	}
	return r.space.Agents.AddAgent(config.ResolvedAgent{
		ID:        agentID,
		UserID:    "owner-user",
		Home:      filepath.Join(home, "agents", agentID),
		Workspace: filepath.Join(home, "workspaces", agentID),
		Model:     "test-model",
	}, nil, bus.New())
}

func TestResolveRequestAgentLazyAttachesScopedAPIKeyAgent(t *testing.T) {
	space := newTestUserSpace(t, "app-user")
	resolver := &testAgentInjectorResolver{space: space}
	srv := &Server{resolver: resolver}

	ctx := auth.WithIdentity(context.Background(), auth.Identity{
		UserID:       "app-user",
		AuthMethod:   "apikey",
		APIKeyType:   users.APIKeyTypeAgent,
		APIKeyAgents: []string{"research-agent"},
	})

	ag := srv.resolveRequestAgent(requestWithContext(ctx, "app-user"), space, "research-agent")
	if ag == nil {
		t.Fatal("resolveRequestAgent returned nil")
	}
	if got := ag.Name(); got != "research-agent" {
		t.Fatalf("agent name = %q, want research-agent", got)
	}
	if resolver.ensureUser != "app-user" || resolver.ensureAgent != "research-agent" {
		t.Fatalf("EnsureAgent called with user=%q agent=%q", resolver.ensureUser, resolver.ensureAgent)
	}
}

func TestResolveRequestAgentRejectsOutOfScopeAPIKeyAgent(t *testing.T) {
	space := newTestUserSpace(t, "app-user")
	resolver := &testAgentInjectorResolver{space: space}
	srv := &Server{resolver: resolver}

	ctx := auth.WithIdentity(context.Background(), auth.Identity{
		UserID:       "app-user",
		AuthMethod:   "apikey",
		APIKeyType:   users.APIKeyTypeAgent,
		APIKeyAgents: []string{"other-agent"},
	})

	if ag := srv.resolveRequestAgent(requestWithContext(ctx, "app-user"), space, "research-agent"); ag != nil {
		t.Fatalf("resolveRequestAgent returned unauthorized agent %q", ag.Name())
	}
	if resolver.ensureAgent != "" {
		t.Fatalf("EnsureAgent should not run for out-of-scope key, got %q", resolver.ensureAgent)
	}
}

func TestResolveRequestAgentDoesNotLazyAttachRegularSession(t *testing.T) {
	space := newTestUserSpace(t, "session-user")
	resolver := &testAgentInjectorResolver{space: space}
	srv := &Server{resolver: resolver}

	ctx := auth.WithIdentity(context.Background(), auth.Identity{
		UserID:     "session-user",
		AuthMethod: "session",
		Role:       users.RoleUser,
	})

	if ag := srv.resolveRequestAgent(requestWithContext(ctx, "session-user"), space, "research-agent"); ag != nil {
		t.Fatalf("resolveRequestAgent returned session-attached agent %q", ag.Name())
	}
	if resolver.ensureAgent != "" {
		t.Fatalf("EnsureAgent should not run for regular session, got %q", resolver.ensureAgent)
	}
}

func newTestUserSpace(t *testing.T, userID string) *UserSpaceView {
	t.Helper()
	t.Setenv("FASTCLAW_HOME", t.TempDir())
	mgr, err := agentpkg.NewManager(nil, nil, bus.New(), agentpkg.WithUserID(userID))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return &UserSpaceView{UserID: userID, Agents: mgr}
}

func requestWithContext(ctx context.Context, userID string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return req.WithContext(config.WithUserID(ctx, userID))
}
