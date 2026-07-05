// Added by SalesKo: admin erase-user cascade endpoint.

package api

import (
	"net/http"
	"os"
	"time"

	"github.com/fastclaw-ai/fastclaw/internal/auth"
	"github.com/fastclaw-ai/fastclaw/internal/config"
	"github.com/fastclaw-ai/fastclaw/internal/sandbox"
	"github.com/fastclaw-ai/fastclaw/internal/store"
	"github.com/fastclaw-ai/fastclaw/internal/users"
)

// eraseSandboxResult is the sandbox-kill section of the erase receipt.
// Both slices are always non-nil (empty, not null) so the JSON shape is
// identical between a fresh erase with nothing to kill and an idempotent
// replay — see HandleEraseAppUser's normalizeSandboxResult step.
type eraseSandboxResult struct {
	Killed []string              `json:"killed"`
	Failed []sandbox.KillFailure `json:"failed"`
}

// eraseDiskResult is the on-disk cleanup section of the erase receipt.
type eraseDiskResult struct {
	DirsRemoved []string `json:"dirsRemoved"`
}

// eraseReceipt is the response body for POST /v1/users/{externalId}/erase.
type eraseReceipt struct {
	ExternalID string                 `json:"externalId"`
	UserID     string                 `json:"userId"`
	Erased     bool                   `json:"erased"`
	Sandboxes  eraseSandboxResult     `json:"sandboxes"`
	DB         store.DeleteUserCounts `json:"db"`
	Disk       eraseDiskResult        `json:"disk"`
	At         time.Time              `json:"at"`
}

// HandleEraseAppUser handles POST /v1/users/{externalId}/erase.
//
// GDPR-style cascade delete for one app_user provisioned under the
// caller's api_key owner account:
//
//  1. Kill every E2B sandbox durably bound to the user (KillForUser,
//     reading the sandbox_bindings table — the in-memory pool map has no
//     user affinity and doesn't survive a restart or a sibling replica
//     handling the original request). A partial sandbox-kill failure does
//     NOT abort the cascade — see the 207 branch at the bottom.
//  2. Delete every DB row store.DeleteUser already cascades through
//     (agents, sessions, session_messages, session_events, cron_jobs,
//     agent_files, configs, apikeys, web_sessions, the user row itself),
//     via the count-reporting DeleteUserWithCounts variant. A DB error
//     here DOES abort (fail-closed, 500, nothing recorded as erased) —
//     the transaction rolled back, so a retry sees the original state.
//  3. Best-effort remove each formerly-owned agent's on-disk home
//     directory (~/.fastclaw/agents/<id>/agent — skills/ + memory/logs/;
//     DeleteUser only ever touches the DB, never this on-disk cache).
//  4. Record an erased-user tombstone so a replayed request is
//     idempotent (see below) instead of re-running (impossible — the
//     user row is gone) or 404ing as if it never existed.
//
// Authenticated by api_key only, matching HandleProvisionAppUser. The
// externalId path segment resolves to a user_id scoped to the calling
// api_key's OWNER account via store.GetUserByExternal — an externalId
// belonging to a different owner is indistinguishable from "doesn't
// exist" under that query (it filters on owner_user_id), so both 404.
// This endpoint deliberately does NOT lazily mint a row the way
// HandleProvisionAppUser does.
//
// The request identity must not have already been switched to an
// app_user by the X-Fastclaw-End-User header — that would key the
// ownership lookup off some OTHER app_user's id instead of the real
// api_key owner, breaking the "only erase your own account's users"
// invariant. Callers must hit this endpoint without that header.
//
// Idempotent: once erased, the user row is gone, so a replay can't
// re-resolve externalId via GetUserByExternal. The handler falls back to
// the erased_users tombstone recorded at the end of a successful first
// run: found → same-shape 200 with zero-value db/sandbox/disk fields;
// not found (never existed, or belongs to a different owner) → 404.
func (s *Server) HandleEraseAppUser(w http.ResponseWriter, r *http.Request) {
	ident, ok := auth.FromContext(r.Context())
	if !ok || ident.AuthMethod != "apikey" || ident.APIKeyID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"error": map[string]string{"message": "api_key required", "type": "authentication_error"},
		})
		return
	}
	// See the doc comment above: a header-switched app_user identity
	// must not be able to reach this endpoint at all, since ident.UserID
	// would then resolve ownership against the wrong account.
	if ident.Role == users.RoleAppUser {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error": map[string]string{"message": "erase must be called without X-Fastclaw-End-User", "type": "permission_error"},
		})
		return
	}
	if s.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": map[string]string{"message": "store not configured", "type": "server_error"},
		})
		return
	}

	externalID := r.PathValue("externalId")
	if externalID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]string{"message": "externalId is required", "type": "invalid_request_error"},
		})
		return
	}

	ctx := r.Context()
	ownerUserID := ident.UserID

	target, err := s.store.GetUserByExternal(ctx, ownerUserID, externalID)
	if err != nil {
		// Replay of an already-completed erase: the user row is gone,
		// but the tombstone recorded at the end of the first run is
		// still there. Return the same-shape zero-value receipt instead
		// of 404ing as if this externalId had never belonged to this
		// owner.
		if tomb, terr := s.store.GetErasedUser(ctx, ownerUserID, externalID); terr == nil {
			writeJSON(w, http.StatusOK, eraseReceipt{
				ExternalID: externalID,
				UserID:     tomb.UserID,
				Erased:     true,
				Sandboxes:  eraseSandboxResult{Killed: []string{}, Failed: []sandbox.KillFailure{}},
				DB:         store.DeleteUserCounts{},
				Disk:       eraseDiskResult{DirsRemoved: []string{}},
				At:         tomb.ErasedAt,
			})
			return
		}
		// Covers both "never existed" and "belongs to a different
		// owner" — GetUserByExternal filters on (ownerUserID,
		// externalID), so a foreign user's row simply doesn't match and
		// looks identical to "not found" from here. That's deliberate:
		// an api_key must not be able to probe for another account's
		// end-users.
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error": map[string]string{"message": "no such user for this api_key owner", "type": "not_found_error"},
		})
		return
	}

	userID := target.ID

	// 1. Kill E2B sandboxes first, while the binding rows this user owns
	// still exist. A partial failure here does not abort the cascade —
	// it's surfaced via the 207 branch below instead of silently
	// dropped.
	sb := eraseSandboxResult{Killed: []string{}, Failed: []sandbox.KillFailure{}}
	if s.sandboxPool != nil {
		if uk, ok := s.sandboxPool.(sandbox.UserKiller); ok {
			killed, failed, kerr := uk.KillForUser(ctx, userID)
			if kerr != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{
					"error": map[string]string{"message": kerr.Error(), "type": "server_error"},
				})
				return
			}
			if killed != nil {
				sb.Killed = killed
			}
			if failed != nil {
				sb.Failed = failed
			}
		}
	}

	// 2. Snapshot owned agent IDs before the DB cascade removes the
	// rows — the on-disk cleanup below needs the id list, and
	// DeleteUserWithCounts only returns counts, not identifiers.
	ownedAgents, _ := s.store.ListAgents(ctx, userID)

	// 3. DB cascade. Fail-closed: abort before recording anything as
	// erased so a retry sees the original (rolled-back) state rather
	// than a false "erased".
	counts, err := s.store.DeleteUserWithCounts(ctx, userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": map[string]string{"message": err.Error(), "type": "server_error"},
		})
		return
	}

	// 4. Best-effort on-disk cleanup. DeleteUser/DeleteUserWithCounts
	// only ever touch the DB — agent_files' skills/ + memory/logs
	// materialize on disk at ~/.fastclaw/agents/<id>/agent (see
	// gateway.ensureAgentHome) and are never cleaned up there. A
	// removal failure is logged into the receipt, not surfaced as an
	// endpoint failure — a stray directory is an ops cleanup task, not
	// a reason to tell the caller the user wasn't erased.
	dirsRemoved := []string{}
	for _, ag := range ownedAgents {
		home, herr := config.AgentHomeDir(ag.ID)
		if herr != nil {
			continue
		}
		if _, statErr := os.Stat(home); statErr != nil {
			continue
		}
		if rmErr := os.RemoveAll(home); rmErr == nil {
			dirsRemoved = append(dirsRemoved, home)
		}
	}

	// 5. Tombstone so a replay is idempotent (see doc comment above).
	now := time.Now().UTC()
	if terr := s.store.RecordErasedUser(ctx, ownerUserID, externalID, userID, now); terr != nil {
		// The cascade itself already committed — don't fail the
		// response over bookkeeping for a *future* replay. Surfacing
		// this as a 5xx would tell the caller "not erased" when it
		// demonstrably was.
		sb.Failed = append(sb.Failed, sandbox.KillFailure{
			SandboxID: "",
			Error:     "erased-user tombstone write failed (a replay of this request may 404 instead of returning the idempotent receipt): " + terr.Error(),
		})
	}

	receipt := eraseReceipt{
		ExternalID: externalID,
		UserID:     userID,
		Erased:     true,
		Sandboxes:  sb,
		DB:         counts,
		Disk:       eraseDiskResult{DirsRemoved: dirsRemoved},
		At:         now,
	}

	if len(sb.Failed) > 0 {
		writeJSON(w, http.StatusMultiStatus, receipt)
		return
	}
	writeJSON(w, http.StatusOK, receipt)
}
