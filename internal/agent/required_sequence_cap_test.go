package agent

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/fastclaw-ai/fastclaw/internal/agent/tools"
	"github.com/fastclaw-ai/fastclaw/internal/bus"
	"github.com/fastclaw-ai/fastclaw/internal/provider"
	"github.com/fastclaw-ai/fastclaw/internal/session"
)

// TestCompleteRequiredSequenceAtCapConvergesOrphanJob reproduces the orphan
// state at the iteration cap: the model wandered on exploratory tools and
// never advanced the required sequence (next == 0, no read result in the
// transcript). The cap-drain must synthesize + execute BOTH required tools so
// submit_proposal runs (job converges) with the deterministic fallback badge,
// instead of leaving the job orphaned.
func TestCompleteRequiredSequenceAtCapConvergesOrphanJob(t *testing.T) {
	const readTool = "mcp_salesko_graph_read_job_context"
	const submitTool = "mcp_salesko_graph_submit_proposal"

	reg := tools.NewRegistry(t.TempDir(), t.TempDir())

	var mu sync.Mutex
	var readCalls, submitCalls int
	var submitArgs map[string]any

	reg.Register(readTool, "read job context", nil, func(_ context.Context, _ json.RawMessage) (string, error) {
		mu.Lock()
		readCalls++
		mu.Unlock()
		return `{
			"baseFrame": {"graph": {"nodes": [{"id": "node-1", "type": "person", "label": "Ada"}]}},
			"currentFrameVersion": 3,
			"targetEntity": {"id": "canon-1", "latestNodeId": "node-1"}
		}`, nil
	})
	reg.Register(submitTool, "submit proposal", nil, func(_ context.Context, args json.RawMessage) (string, error) {
		mu.Lock()
		submitCalls++
		_ = json.Unmarshal(args, &submitArgs)
		mu.Unlock()
		return `{"ok": true, "proposalId": "prop-1"}`, nil
	})

	a := &Agent{
		name:              "agent-test",
		registry:          reg,
		engine:            newSDKEngine("test"),
		sessions:          session.NewManager(t.TempDir()),
		maxToolIterations: 3,
		workspacePath:     t.TempDir(),
	}
	sess := a.sessions.Get("web", "", "chat-cap", "")

	msg := bus.InboundMessage{
		Channel: "web",
		ChatID:  "chat-cap",
		Params: map[string]any{
			"job_id":          "job-1",
			"tenant_id":       "tenant-1",
			"frame_record_id": "frame-1",
			"dataset_scope":   "default",
		},
	}

	policy := newRequiredToolSequencePolicy([]string{readTool, submitTool}, 1)
	if policy.complete() {
		t.Fatal("precondition: policy should be incomplete before cap drain")
	}

	total := 0
	messages := []provider.Message{{Role: "user", Content: "research this contact"}}
	messages = a.completeRequiredSequenceAtCap(context.Background(), sess, msg, policy, messages, &total)

	if !policy.complete() {
		t.Fatalf("required sequence still incomplete after cap drain (nextTool=%q)", policy.nextTool())
	}

	mu.Lock()
	defer mu.Unlock()
	if readCalls != 1 {
		t.Fatalf("read_job_context executed %d times, want 1", readCalls)
	}
	if submitCalls != 1 {
		t.Fatalf("submit_proposal executed %d times, want 1 (job orphans at 0)", submitCalls)
	}
	if total != 2 {
		t.Fatalf("totalToolCalls delta = %d, want 2", total)
	}
	warnings, _ := submitArgs["warnings"].([]any)
	if !hasWarning(warnings, "deterministic_submit_fallback_used") {
		t.Fatalf("submit args missing deterministic_submit_fallback_used warning: %v", submitArgs["warnings"])
	}
	if got, _ := submitArgs["stop_reason"].(string); got != "provider_limit" {
		t.Fatalf("stop_reason = %q, want provider_limit", got)
	}
	pcs, ok := submitArgs["proposed_change_set"].(map[string]any)
	if !ok {
		t.Fatalf("submit args missing proposed_change_set: %#v", submitArgs)
	}
	if cmds, _ := pcs["commands"].([]any); len(cmds) == 0 {
		t.Fatal("proposed_change_set has no commands — not a legal proposal")
	}
	last := messages[len(messages)-1]
	if last.Role != "tool" || last.Name != submitTool {
		t.Fatalf("last message = {role:%q name:%q}, want tool/%s", last.Role, last.Name, submitTool)
	}
}

// TestCompleteRequiredSequenceAtCapNoopWhenComplete confirms the drain is a
// safe no-op when the required sequence already completed (defensive; the
// call site only invokes it when incomplete).
func TestCompleteRequiredSequenceAtCapNoopWhenComplete(t *testing.T) {
	const submitTool = "mcp_salesko_graph_submit_proposal"
	reg := tools.NewRegistry(t.TempDir(), t.TempDir())
	var submitCalls int
	reg.Register(submitTool, "", nil, func(_ context.Context, _ json.RawMessage) (string, error) {
		submitCalls++
		return "{}", nil
	})
	a := &Agent{
		name:              "agent-test",
		registry:          reg,
		engine:            newSDKEngine("test"),
		sessions:          session.NewManager(t.TempDir()),
		maxToolIterations: 3,
		workspacePath:     t.TempDir(),
	}
	sess := a.sessions.Get("web", "", "chat-done", "")
	policy := newRequiredToolSequencePolicy([]string{submitTool}, 1)
	policy.markSuccessfulTool(submitTool)
	if !policy.complete() {
		t.Fatal("precondition: policy should be complete")
	}
	total := 0
	in := []provider.Message{{Role: "user", Content: "x"}}
	out := a.completeRequiredSequenceAtCap(context.Background(), sess, bus.InboundMessage{}, policy, in, &total)
	if submitCalls != 0 {
		t.Fatalf("submit executed %d times on a complete sequence, want 0", submitCalls)
	}
	if len(out) != len(in) || total != 0 {
		t.Fatalf("complete sequence mutated state (len %d->%d, total %d)", len(in), len(out), total)
	}
}

func hasWarning(ws []any, want string) bool {
	for _, w := range ws {
		if s, ok := w.(string); ok && s == want {
			return true
		}
	}
	return false
}
