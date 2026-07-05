package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestDeleteUserWithCountsAccuracy seeds one row in every table
// DeleteUser's cascade touches (agents, sessions, session_messages,
// session_events, cron_jobs, agent_files, configs, apikeys +
// apikey_agents, web_sessions), then asserts DeleteUserWithCounts
// reports exactly 1 for each — and that the rows are actually gone, not
// just counted. This is the receipt admin erase-user surfaces to the
// caller, so a wrong count here would silently misreport what got
// deleted.
func TestDeleteUserWithCountsAccuracy(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	ctx := context.Background()

	owner := &UserRecord{
		ID: "u_owner", Username: "owner", Email: "owner@test.local",
		Role: "user", Status: "active", AgentQuota: -1,
	}
	if err := db.CreateUser(ctx, owner); err != nil {
		t.Fatalf("create user: %v", err)
	}

	agentRec := &AgentRecord{ID: "agt_1", UserID: owner.ID, Name: "agent one"}
	if err := db.SaveAgent(ctx, agentRec); err != nil {
		t.Fatalf("save agent: %v", err)
	}

	sess := &SessionRecord{Messages: []SessionMessage{{Role: "user", Content: "hi", Timestamp: time.Now()}}}
	if err := db.SaveSession(ctx, owner.ID, agentRec.ID, "sess_1", sess); err != nil {
		t.Fatalf("save session: %v", err)
	}
	if err := db.AppendSessionMessage(ctx, owner.ID, agentRec.ID, "sess_1",
		SessionMessage{Role: "user", Content: "hi", Timestamp: time.Now()}); err != nil {
		t.Fatalf("append session message: %v", err)
	}
	if _, err := db.AppendSessionEvent(ctx, owner.ID, agentRec.ID, "sess_1", "content", []byte(`{}`)); err != nil {
		t.Fatalf("append session event: %v", err)
	}

	cron := &CronJobRecord{
		ID: "cron_1", UserID: owner.ID, AgentID: agentRec.ID, Name: "reminder",
		Type: "once", Schedule: time.Now().Format(time.RFC3339), Message: "hi",
		Channel: "web", ChatID: "c1", Enabled: true,
	}
	if err := db.SaveCronJob(ctx, cron); err != nil {
		t.Fatalf("save cron: %v", err)
	}

	if err := db.SaveAgentFile(ctx, agentRec.ID, owner.ID, "NOTES.md", []byte("hi")); err != nil {
		t.Fatalf("save agent file: %v", err)
	}

	cfg := &ConfigRecord{Kind: KindSetting, UserID: owner.ID, Name: "prefs", Data: map[string]interface{}{"a": 1}}
	if err := db.SaveConfig(ctx, cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	apikey := &APIKeyRecord{ID: "ak_1", UserID: owner.ID, Name: "key", KeyHash: "hash1", Type: "user"}
	if err := db.CreateAPIKey(ctx, apikey); err != nil {
		t.Fatalf("create apikey: %v", err)
	}
	if err := db.SetAPIKeyAgents(ctx, apikey.ID, []string{agentRec.ID}); err != nil {
		t.Fatalf("set apikey agents: %v", err)
	}

	if err := db.CreateWebSession(ctx, &WebSessionRecord{SID: "sid_1", UserID: owner.ID, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("create web session: %v", err)
	}

	counts, err := db.DeleteUserWithCounts(ctx, owner.ID)
	if err != nil {
		t.Fatalf("DeleteUserWithCounts: %v", err)
	}

	want := DeleteUserCounts{
		Agents: 1, AgentFiles: 1, Sessions: 1, SessionMessages: 1,
		SessionEvents: 1, CronJobs: 1, APIKeyAgents: 1, WebSessions: 1,
		APIKeys: 1, Configs: 1,
	}
	if counts != want {
		t.Errorf("counts = %+v, want %+v", counts, want)
	}

	if _, err := db.GetUser(ctx, owner.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("user row still present after DeleteUserWithCounts (err=%v)", err)
	}
	if _, err := db.GetAgent(ctx, agentRec.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("agent row still present after DeleteUserWithCounts (err=%v)", err)
	}

	// A second call against the now-deleted user is safe and reports an
	// all-zero receipt rather than erroring — every DELETE just affects
	// 0 rows. This is the DB-layer half of the endpoint's idempotent
	// replay contract (the other half is the erased_users tombstone,
	// covered by TestErasedUserTombstone).
	counts2, err := db.DeleteUserWithCounts(ctx, owner.ID)
	if err != nil {
		t.Fatalf("second DeleteUserWithCounts: %v", err)
	}
	if counts2 != (DeleteUserCounts{}) {
		t.Errorf("second call counts = %+v, want all-zero", counts2)
	}
}

// TestDeleteUserWithCountsCatchesForeignAgentSessions verifies the
// user-level phase of the cascade (not just the owned-agent phase): a
// session this user holds on an agent they do NOT own (the public-agent
// chatter case) must still be counted and removed.
func TestDeleteUserWithCountsCatchesForeignAgentSessions(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	ctx := context.Background()

	chatter := &UserRecord{ID: "u_chatter", Username: "chatter", Email: "chatter@test.local", Role: "app_user", Status: "active", AgentQuota: -1}
	if err := db.CreateUser(ctx, chatter); err != nil {
		t.Fatalf("create chatter: %v", err)
	}
	otherOwner := &UserRecord{ID: "u_other", Username: "other", Email: "other@test.local", Role: "user", Status: "active", AgentQuota: -1}
	if err := db.CreateUser(ctx, otherOwner); err != nil {
		t.Fatalf("create other owner: %v", err)
	}
	foreignAgent := &AgentRecord{ID: "agt_foreign", UserID: otherOwner.ID, Name: "public agent", IsPublic: true}
	if err := db.SaveAgent(ctx, foreignAgent); err != nil {
		t.Fatalf("save foreign agent: %v", err)
	}

	sess := &SessionRecord{Messages: []SessionMessage{{Role: "user", Content: "hi", Timestamp: time.Now()}}}
	if err := db.SaveSession(ctx, chatter.ID, foreignAgent.ID, "sess_x", sess); err != nil {
		t.Fatalf("save session: %v", err)
	}

	counts, err := db.DeleteUserWithCounts(ctx, chatter.ID)
	if err != nil {
		t.Fatalf("DeleteUserWithCounts: %v", err)
	}
	if counts.Sessions != 1 {
		t.Errorf("Sessions = %d, want 1 (chatter's session on a foreign-owned agent)", counts.Sessions)
	}
	// The foreign agent itself must survive — chatter didn't own it.
	if _, err := db.GetAgent(ctx, foreignAgent.ID); err != nil {
		t.Errorf("foreign agent should survive chatter's erase, got err=%v", err)
	}
}

// TestErasedUserTombstone covers the RecordErasedUser / GetErasedUser
// round trip the erase-user endpoint's idempotent-replay path depends
// on: unknown pair → ErrNotFound; recorded pair → readable; re-recording
// (upsert) overwrites rather than erroring.
func TestErasedUserTombstone(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	ctx := context.Background()

	if _, err := db.GetErasedUser(ctx, "owner_1", "ext_1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetErasedUser on unknown pair: err = %v, want ErrNotFound", err)
	}

	first := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	if err := db.RecordErasedUser(ctx, "owner_1", "ext_1", "u_erased", first); err != nil {
		t.Fatalf("RecordErasedUser: %v", err)
	}
	rec, err := db.GetErasedUser(ctx, "owner_1", "ext_1")
	if err != nil {
		t.Fatalf("GetErasedUser: %v", err)
	}
	if rec.UserID != "u_erased" || !rec.ErasedAt.Equal(first) {
		t.Errorf("tombstone = %+v, want userID=u_erased erasedAt=%v", rec, first)
	}

	// Scoped to (ownerUserID, externalID) — a different owner with the
	// same externalID must NOT resolve; this is the same isolation
	// invariant GetUserByExternal enforces before erase.
	if _, err := db.GetErasedUser(ctx, "owner_2", "ext_1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetErasedUser cross-owner leak: err = %v, want ErrNotFound", err)
	}

	// Upsert: re-recording the same pair overwrites, doesn't conflict.
	second := first.Add(time.Minute)
	if err := db.RecordErasedUser(ctx, "owner_1", "ext_1", "u_erased", second); err != nil {
		t.Fatalf("RecordErasedUser (re-record): %v", err)
	}
	rec2, err := db.GetErasedUser(ctx, "owner_1", "ext_1")
	if err != nil {
		t.Fatalf("GetErasedUser after re-record: %v", err)
	}
	if !rec2.ErasedAt.Equal(second) {
		t.Errorf("erasedAt after re-record = %v, want %v", rec2.ErasedAt, second)
	}
}
