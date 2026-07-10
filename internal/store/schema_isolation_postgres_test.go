package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
)

func TestTableHasColumnPostgresUsesCurrentSchema(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("FASTCLAW_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("FASTCLAW_TEST_POSTGRES_DSN is required for the PostgreSQL schema-isolation test")
	}

	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open PostgreSQL admin connection: %v", err)
	}
	defer admin.Close()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	targetSchema := "fastclaw_schema_" + suffix
	otherSchema := "other_schema_" + suffix
	ctx := context.Background()

	for _, schema := range []string{targetSchema, otherSchema} {
		if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+pq.QuoteIdentifier(schema)); err != nil {
			t.Fatalf("create schema %s: %v", schema, err)
		}
	}
	defer func() {
		for _, schema := range []string{targetSchema, otherSchema} {
			if _, err := admin.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS "+pq.QuoteIdentifier(schema)+" CASCADE"); err != nil {
				t.Errorf("drop schema %s: %v", schema, err)
			}
		}
	}()

	if _, err := admin.ExecContext(ctx, "CREATE TABLE "+pq.QuoteIdentifier(targetSchema)+`.agent_files (
		agent_id TEXT NOT NULL,
		filename TEXT NOT NULL,
		content TEXT NOT NULL DEFAULT '',
		updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (agent_id, filename)
	)`); err != nil {
		t.Fatalf("create target legacy table: %v", err)
	}
	if _, err := admin.ExecContext(ctx, "CREATE TABLE "+pq.QuoteIdentifier(otherSchema)+`.agent_files (
		agent_id TEXT NOT NULL,
		user_id TEXT NOT NULL DEFAULT '',
		filename TEXT NOT NULL,
		PRIMARY KEY (agent_id, user_id, filename)
	)`); err != nil {
		t.Fatalf("create colliding table: %v", err)
	}

	scopedDSN := postgresDSNWithSearchPath(t, dsn, targetSchema)
	db, err := NewDBStore("postgres", scopedDSN)
	if err != nil {
		t.Fatalf("open schema-scoped FastClaw store: %v", err)
	}
	defer db.Close()

	hasUserID, err := db.tableHasColumn(ctx, "agent_files", "user_id")
	if err != nil {
		t.Fatalf("probe target schema: %v", err)
	}
	if hasUserID {
		t.Fatal("target schema incorrectly inherited agent_files.user_id from another schema")
	}

	if err := db.migrateAgentFilesUserID(ctx); err != nil {
		t.Fatalf("migrate target legacy table: %v", err)
	}
	hasUserID, err = db.tableHasColumn(ctx, "agent_files", "user_id")
	if err != nil {
		t.Fatalf("probe migrated target schema: %v", err)
	}
	if !hasUserID {
		t.Fatal("target schema did not gain agent_files.user_id")
	}
}

func postgresDSNWithSearchPath(t *testing.T, dsn, schema string) string {
	t.Helper()
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Scheme == "" {
		t.Fatalf("FASTCLAW_TEST_POSTGRES_DSN must be a PostgreSQL URL: %v", err)
	}
	query := parsed.Query()
	query.Set("search_path", schema+",pg_catalog")
	parsed.RawQuery = query.Encode()
	return parsed.String()
}
