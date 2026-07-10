package users

import (
	"context"
	"errors"
	"testing"

	"github.com/fastclaw-ai/fastclaw/internal/store"
)

func TestEnsureAppUserRejectsDisabledIdentity(t *testing.T) {
	st := openUsersTestStore(t)
	defer st.Close()
	ctx := context.Background()
	accounts, err := NewAccounts(st)
	if err != nil {
		t.Fatalf("NewAccounts: %v", err)
	}

	appUser, err := accounts.EnsureAppUser(ctx, "u_owner", "external-1", "", "k_admin")
	if err != nil {
		t.Fatalf("EnsureAppUser: %v", err)
	}
	if _, err := accounts.Update(ctx, appUser.ID, "", "", StatusDisabled, nil); err != nil {
		t.Fatalf("disable app user: %v", err)
	}
	if _, err := accounts.EnsureAppUser(ctx, "u_owner", "external-1", "", "k_admin"); !errors.Is(err, ErrAccountDisabled) {
		t.Fatalf("EnsureAppUser disabled error = %v, want ErrAccountDisabled", err)
	}
}

func TestDeleteIsIdempotentForAbsentUser(t *testing.T) {
	st := openUsersTestStore(t)
	defer st.Close()
	ctx := context.Background()
	accounts, err := NewAccounts(st)
	if err != nil {
		t.Fatalf("NewAccounts: %v", err)
	}
	appUser, err := accounts.EnsureAppUser(ctx, "u_owner", "external-delete", "", "k_admin")
	if err != nil {
		t.Fatalf("EnsureAppUser: %v", err)
	}
	deleted, err := accounts.Delete(ctx, appUser.ID)
	if err != nil || !deleted {
		t.Fatalf("first delete = (%v, %v), want true nil", deleted, err)
	}
	deleted, err = accounts.Delete(ctx, appUser.ID)
	if err != nil || deleted {
		t.Fatalf("repeat delete = (%v, %v), want false nil", deleted, err)
	}
}

func openUsersTestStore(t *testing.T) *store.DBStore {
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
