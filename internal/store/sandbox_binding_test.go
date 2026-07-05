package store

import (
	"context"
	"testing"
	"time"
)

// TestSandboxBindingLifecycle covers Save/List/Delete for the
// sandbox_bindings table the E2B pool uses to durably record per-sandbox
// user ownership — the admin erase-user cascade's KillForUser reads
// exactly this path to enumerate what to destroy.
func TestSandboxBindingLifecycle(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	ctx := context.Background()

	// Nothing bound yet.
	got, err := db.ListSandboxBindingsByUser(ctx, "u_1")
	if err != nil {
		t.Fatalf("ListSandboxBindingsByUser (empty): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no bindings, got %d", len(got))
	}

	b1 := &SandboxBindingRecord{SandboxID: "sb_1", UserID: "u_1", AgentID: "agt_1", ExecutionRef: "agt_1:s:sess_1"}
	b2 := &SandboxBindingRecord{SandboxID: "sb_2", UserID: "u_1", AgentID: "agt_2", ExecutionRef: "agt_2:s:sess_2"}
	b3 := &SandboxBindingRecord{SandboxID: "sb_3", UserID: "u_2", AgentID: "agt_3", ExecutionRef: "agt_3:s:sess_3"}
	for _, b := range []*SandboxBindingRecord{b1, b2, b3} {
		if err := db.SaveSandboxBinding(ctx, b); err != nil {
			t.Fatalf("SaveSandboxBinding(%s): %v", b.SandboxID, err)
		}
	}

	u1Bindings, err := db.ListSandboxBindingsByUser(ctx, "u_1")
	if err != nil {
		t.Fatalf("ListSandboxBindingsByUser(u_1): %v", err)
	}
	if len(u1Bindings) != 2 {
		t.Fatalf("u_1 bindings = %d, want 2", len(u1Bindings))
	}
	byID := map[string]SandboxBindingRecord{}
	for _, b := range u1Bindings {
		byID[b.SandboxID] = b
	}
	if byID["sb_1"].AgentID != "agt_1" || byID["sb_1"].ExecutionRef != "agt_1:s:sess_1" {
		t.Errorf("sb_1 binding = %+v, fields don't match what was saved", byID["sb_1"])
	}
	if byID["sb_1"].CreatedAt.IsZero() {
		t.Errorf("sb_1 CreatedAt not populated")
	}

	// u_2's binding must not leak into u_1's list.
	if _, ok := byID["sb_3"]; ok {
		t.Errorf("sb_3 (owned by u_2) leaked into u_1's binding list")
	}

	// Upsert: saving the same sandbox_id again updates rather than
	// erroring or duplicating.
	if err := db.SaveSandboxBinding(ctx, &SandboxBindingRecord{SandboxID: "sb_1", UserID: "u_1", AgentID: "agt_1_renamed", ExecutionRef: "agt_1:s:sess_1"}); err != nil {
		t.Fatalf("SaveSandboxBinding (upsert): %v", err)
	}
	u1Again, err := db.ListSandboxBindingsByUser(ctx, "u_1")
	if err != nil {
		t.Fatalf("ListSandboxBindingsByUser after upsert: %v", err)
	}
	if len(u1Again) != 2 {
		t.Fatalf("u_1 bindings after upsert = %d, want 2 (no duplicate row)", len(u1Again))
	}
	for _, b := range u1Again {
		if b.SandboxID == "sb_1" && b.AgentID != "agt_1_renamed" {
			t.Errorf("sb_1 AgentID after upsert = %q, want agt_1_renamed", b.AgentID)
		}
	}

	// Delete is idempotent: clearing an absent row is a no-op, not an error.
	if err := db.DeleteSandboxBinding(ctx, "sb_1"); err != nil {
		t.Fatalf("DeleteSandboxBinding: %v", err)
	}
	if err := db.DeleteSandboxBinding(ctx, "sb_1"); err != nil {
		t.Fatalf("DeleteSandboxBinding (already gone): %v", err)
	}
	u1Final, err := db.ListSandboxBindingsByUser(ctx, "u_1")
	if err != nil {
		t.Fatalf("ListSandboxBindingsByUser after delete: %v", err)
	}
	if len(u1Final) != 1 || u1Final[0].SandboxID != "sb_2" {
		t.Errorf("u_1 bindings after deleting sb_1 = %+v, want only sb_2", u1Final)
	}

	// u_2's binding is untouched by any of the above.
	u2Bindings, err := db.ListSandboxBindingsByUser(ctx, "u_2")
	if err != nil {
		t.Fatalf("ListSandboxBindingsByUser(u_2): %v", err)
	}
	if len(u2Bindings) != 1 || u2Bindings[0].SandboxID != "sb_3" {
		t.Errorf("u_2 bindings = %+v, want only sb_3", u2Bindings)
	}
}

// TestSaveSandboxBindingRequiresSandboxID guards the one validation
// SaveSandboxBinding does — a blank sandbox_id would collide with every
// other blank-id row under a real PK constraint violation instead of a
// clean, callable-from-a-log-line error.
func TestSaveSandboxBindingRequiresSandboxID(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	ctx := context.Background()

	err := db.SaveSandboxBinding(ctx, &SandboxBindingRecord{UserID: "u_1", CreatedAt: time.Now()})
	if err == nil {
		t.Fatal("expected error for missing sandboxID, got nil")
	}
}
