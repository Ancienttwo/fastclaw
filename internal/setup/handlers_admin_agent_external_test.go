package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fastclaw-ai/fastclaw/internal/auth"
	"github.com/fastclaw-ai/fastclaw/internal/store"
	"github.com/fastclaw-ai/fastclaw/internal/users"
)

func TestCreateUserAgentIsIdempotentByExternalID(t *testing.T) {
	st, err := store.NewDBStore("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("NewDBStore: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	accounts, _ := users.NewAccounts(st)
	target, err := accounts.EnsureAppUser(context.Background(), "u_owner", "app-user", "", "k_admin")
	if err != nil {
		t.Fatalf("EnsureAppUser: %v", err)
	}
	if err := st.SaveAgent(context.Background(), &store.AgentRecord{
		ID:     "agt_template",
		UserID: "u_template_owner",
		Name:   "Research template",
	}); err != nil {
		t.Fatalf("save template: %v", err)
	}
	srv := &Server{accounts: accounts, dataStore: st}
	identity := auth.Identity{
		UserID:     "u_owner",
		Role:       users.RoleSuperAdmin,
		AuthMethod: "apikey",
		APIKeyType: users.APIKeyTypeAdmin,
	}

	call := func() (string, bool) {
		t.Helper()
		body := []byte(`{"name":"Dedicated Research","forkFrom":"agt_template","externalId":"aiphabee:workspace:user"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/users/"+target.ID+"/agents", bytes.NewReader(body))
		req.SetPathValue("id", target.ID)
		req = req.WithContext(auth.WithIdentity(req.Context(), identity))
		res := httptest.NewRecorder()
		srv.handleCreateUserAgent(res, req)
		if res.Code != http.StatusCreated && res.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
		}
		var payload struct {
			Agent struct {
				ID string `json:"id"`
			} `json:"agent"`
			Created bool `json:"created"`
		}
		if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return payload.Agent.ID, payload.Created
	}

	firstID, firstCreated := call()
	secondID, secondCreated := call()
	if firstID == "" || secondID != firstID || !firstCreated || secondCreated {
		t.Fatalf("first=(%q,%v) second=(%q,%v)", firstID, firstCreated, secondID, secondCreated)
	}
	agents, err := st.ListAgents(context.Background(), target.ID)
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	if len(agents) != 1 || agents[0].ExternalID != "aiphabee:workspace:user" {
		t.Fatalf("agents=%+v, want one mapped Agent", agents)
	}
}

func TestCreateUserAgentRejectsOversizedExternalID(t *testing.T) {
	st, err := store.NewDBStore("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("NewDBStore: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	accounts, _ := users.NewAccounts(st)
	target, err := accounts.EnsureAppUser(context.Background(), "u_owner", "app-user", "", "k_admin")
	if err != nil {
		t.Fatalf("EnsureAppUser: %v", err)
	}
	srv := &Server{accounts: accounts, dataStore: st}
	identity := auth.Identity{
		UserID:     "u_owner",
		Role:       users.RoleSuperAdmin,
		AuthMethod: "apikey",
		APIKeyType: users.APIKeyTypeAdmin,
	}
	body, err := json.Marshal(map[string]string{
		"externalId": "aiphabee:" + string(bytes.Repeat([]byte{'x'}, 201)),
		"name":       "Dedicated Research",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/users/"+target.ID+"/agents", bytes.NewReader(body))
	req.SetPathValue("id", target.ID)
	req = req.WithContext(auth.WithIdentity(req.Context(), identity))
	res := httptest.NewRecorder()

	srv.handleCreateUserAgent(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestCreateUserAgentResumeWritesModelToExistingAgent(t *testing.T) {
	st, err := store.NewDBStore("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("NewDBStore: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	accounts, _ := users.NewAccounts(st)
	target, err := accounts.EnsureAppUser(context.Background(), "u_owner", "app-user", "", "k_admin")
	if err != nil {
		t.Fatalf("EnsureAppUser: %v", err)
	}
	const existingAgentID = "agt_existing"
	if err := st.SaveAgent(context.Background(), &store.AgentRecord{
		ID:         existingAgentID,
		UserID:     target.ID,
		ExternalID: "aiphabee:resume",
		Name:       "Dedicated Research",
		Config: map[string]interface{}{
			"provisioningComplete":   false,
			"provisioningTemplateId": "",
		},
	}); err != nil {
		t.Fatalf("SaveAgent: %v", err)
	}
	srv := &Server{accounts: accounts, dataStore: st}
	identity := auth.Identity{
		UserID:     "u_owner",
		Role:       users.RoleSuperAdmin,
		AuthMethod: "apikey",
		APIKeyType: users.APIKeyTypeAdmin,
	}
	body := []byte(`{"name":"Dedicated Research","model":"research-model","externalId":"aiphabee:resume"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/users/"+target.ID+"/agents", bytes.NewReader(body))
	req.SetPathValue("id", target.ID)
	req = req.WithContext(auth.WithIdentity(req.Context(), identity))
	res := httptest.NewRecorder()

	srv.handleCreateUserAgent(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	model := srv.agentScopeModel(req, existingAgentID)
	if model != "research-model" {
		t.Fatalf("model=%q, want research-model on %s", model, existingAgentID)
	}
}
