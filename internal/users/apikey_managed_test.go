package users

import (
	"context"
	"errors"
	"testing"
)

func TestEnsureAdminTokenIsIdempotentAndRotatesManagedSecret(t *testing.T) {
	st := openUsersTestStore(t)
	defer st.Close()
	ctx := context.Background()
	accounts, err := NewAccounts(st)
	if err != nil {
		t.Fatalf("NewAccounts: %v", err)
	}
	owner, err := accounts.Create(ctx, CreateInput{
		Username: "admin", Email: "admin@example.test", Password: "correct-horse-battery-staple", Role: RoleSuperAdmin,
	})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	keys, err := NewAPIKeys(st)
	if err != nil {
		t.Fatalf("NewAPIKeys: %v", err)
	}
	token1 := "fc_" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	token2 := "fc_" + "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"

	first, created, err := keys.EnsureAdminToken(ctx, owner.ID, "aiphabee-control", token1)
	if err != nil || !created {
		t.Fatalf("first ensure = (%v, %v), want created: %v", created, first, err)
	}
	second, created, err := keys.EnsureAdminToken(ctx, owner.ID, "aiphabee-control", token2)
	if err != nil || created {
		t.Fatalf("second ensure = (%v, %v), want updated: %v", created, second, err)
	}
	if second.ID != first.ID {
		t.Fatalf("API key id changed: %s -> %s", first.ID, second.ID)
	}
	if _, err := keys.LookupByToken(ctx, token1); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old token lookup error = %v, want ErrInvalidCredentials", err)
	}
	resolved, err := keys.LookupByToken(ctx, token2)
	if err != nil {
		t.Fatalf("new token lookup: %v", err)
	}
	if resolved.APIKey.ID != first.ID || resolved.APIKey.Type != APIKeyTypeAdmin {
		t.Fatalf("resolved key = %#v", resolved.APIKey)
	}
	list, err := keys.List(ctx, owner.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = (%d, %v), want one key", len(list), err)
	}
}

func TestEnsureAdminTokenRejectsWeakManagedSecret(t *testing.T) {
	st := openUsersTestStore(t)
	defer st.Close()
	keys, err := NewAPIKeys(st)
	if err != nil {
		t.Fatalf("NewAPIKeys: %v", err)
	}
	if _, _, err := keys.EnsureAdminToken(context.Background(), "u_owner", "control", "fc_short"); err == nil {
		t.Fatal("weak managed token was accepted")
	}
}
