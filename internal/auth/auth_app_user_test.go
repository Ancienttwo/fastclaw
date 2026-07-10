package auth

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/fastclaw-ai/fastclaw/internal/store"
	"github.com/fastclaw-ai/fastclaw/internal/users"
)

func TestDisabledAppUserSwitchFailsClosed(t *testing.T) {
	st := openAuthTestStore(t)
	defer st.Close()
	ctx := context.Background()
	accounts, _ := users.NewAccounts(st)
	appUser, err := accounts.EnsureAppUser(ctx, "u_owner", "external-disabled", "", "k_admin")
	if err != nil {
		t.Fatalf("EnsureAppUser: %v", err)
	}
	if _, err := accounts.Update(ctx, appUser.ID, "", "", users.StatusDisabled, nil); err != nil {
		t.Fatalf("disable app user: %v", err)
	}
	resolver, err := NewResolver(st)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	owner := Identity{
		UserID:     "u_owner",
		AuthMethod: "apikey",
		APIKeyID:   "k_admin",
		APIKeyType: users.APIKeyTypeAdmin,
	}
	if _, err := resolver.SwitchToAppUser(ctx, owner, "external-disabled"); !errors.Is(err, users.ErrAccountDisabled) {
		t.Fatalf("SwitchToAppUser error = %v, want ErrAccountDisabled", err)
	}
}

func TestHeaderSwitchErrorDoesNotKeepOwnerIdentity(t *testing.T) {
	st := openAuthTestStore(t)
	defer st.Close()
	ctx := context.Background()
	accounts, _ := users.NewAccounts(st)
	owner, err := accounts.Create(ctx, users.CreateInput{
		Username: "owner",
		Email:    "owner@example.test",
		Password: "test-password",
		Role:     users.RoleSuperAdmin,
	})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	keys, _ := users.NewAPIKeys(st)
	key, token, err := keys.Create(ctx, owner.ID, "admin", users.APIKeyTypeAdmin, nil)
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	appUser, err := accounts.EnsureAppUser(ctx, owner.ID, "external-header", "", key.ID)
	if err != nil {
		t.Fatalf("EnsureAppUser: %v", err)
	}
	if _, err := accounts.Update(ctx, appUser.ID, "", "", users.StatusDisabled, nil); err != nil {
		t.Fatalf("disable app user: %v", err)
	}

	resolver, err := NewResolver(st)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set(EndUserHeader, "external-header")
	if ident, err := resolver.resolve(req); err == nil {
		t.Fatalf("resolve returned owner identity %+v instead of failing closed", ident)
	}
}

func openAuthTestStore(t *testing.T) *store.DBStore {
	t.Helper()
	st, err := store.NewDBStore("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("NewDBStore: %v", err)
	}
	if err := st.Migrate(context.Background()); err != nil {
		st.Close()
		t.Fatalf("Migrate: %v", err)
	}
	return st
}
