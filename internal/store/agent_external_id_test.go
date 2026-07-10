package store

import (
	"context"
	"testing"
)

func TestCreateAgentIdempotentUsesOwnerExternalID(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	ctx := context.Background()

	first, created, err := db.CreateAgentIdempotent(ctx, &AgentRecord{
		ID:         "agt_first",
		UserID:     "u_owner",
		ExternalID: "aiphabee:workspace:user",
		Name:       "Research Agent",
	})
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	if !created || first.ID != "agt_first" {
		t.Fatalf("first create = (%+v, %v), want agt_first created", first, created)
	}

	second, created, err := db.CreateAgentIdempotent(ctx, &AgentRecord{
		ID:         "agt_duplicate",
		UserID:     "u_owner",
		ExternalID: "aiphabee:workspace:user",
		Name:       "Ignored duplicate",
	})
	if err != nil {
		t.Fatalf("repeat create: %v", err)
	}
	if created || second.ID != first.ID {
		t.Fatalf("repeat create = (%+v, %v), want original not-created", second, created)
	}

	other, created, err := db.CreateAgentIdempotent(ctx, &AgentRecord{
		ID:         "agt_other_owner",
		UserID:     "u_other",
		ExternalID: "aiphabee:workspace:user",
		Name:       "Other owner's Agent",
	})
	if err != nil {
		t.Fatalf("other owner create: %v", err)
	}
	if !created || other.ID != "agt_other_owner" {
		t.Fatalf("other owner create = (%+v, %v), want independent Agent", other, created)
	}
}

func TestAgentExternalIDMigrationIsIdempotent(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		if err := db.Migrate(ctx); err != nil {
			t.Fatalf("Migrate iteration %d: %v", i, err)
		}
	}
	has, err := db.tableHasColumn(ctx, "agents", "external_id")
	if err != nil {
		t.Fatalf("tableHasColumn: %v", err)
	}
	if !has {
		t.Fatal("agents.external_id missing after migration")
	}
}
