package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fastclaw-ai/fastclaw/internal/auth"
	"github.com/fastclaw-ai/fastclaw/internal/store"
	"github.com/fastclaw-ai/fastclaw/internal/users"
)

func TestChatBodyDisabledAppUserFailsClosed(t *testing.T) {
	st, err := store.NewDBStore("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("NewDBStore: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	accounts, _ := users.NewAccounts(st)
	appUser, err := accounts.EnsureAppUser(context.Background(), "u_owner", "external-body", "", "k_admin")
	if err != nil {
		t.Fatalf("EnsureAppUser: %v", err)
	}
	if _, err := accounts.Update(context.Background(), appUser.ID, "", "", users.StatusDisabled, nil); err != nil {
		t.Fatalf("disable app user: %v", err)
	}
	resolver, err := auth.NewResolver(st)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	srv := &Server{authResolver: resolver}
	body := []byte(`{"model":"test","messages":[{"role":"user","content":"hello"}],"user":"external-body"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req = req.WithContext(auth.WithIdentity(req.Context(), auth.Identity{
		UserID:     "u_owner",
		AuthMethod: "apikey",
		APIKeyID:   "k_admin",
		APIKeyType: users.APIKeyTypeAdmin,
	}))
	res := httptest.NewRecorder()

	srv.HandleChatCompletions(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d body=%s, want 403", res.Code, res.Body.String())
	}
}
