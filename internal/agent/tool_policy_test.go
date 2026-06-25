package agent

import (
	"encoding/json"
	"testing"

	"github.com/fastclaw-ai/fastclaw/internal/provider"
)

func TestRequiredToolSequenceFromParamsKeepsRequestedOrder(t *testing.T) {
	policy := requiredToolSequenceFromParams(
		map[string]any{
			"fastclaw_tool_policy": map[string]any{
				"required_sequence":   []any{"missing_tool", "mcp_salesko_graph_read_job_context", "mcp_salesko_graph_submit_proposal"},
				"max_no_tool_retries": float64(7),
			},
		},
		[]provider.Tool{namedTool("mcp_salesko_graph_read_job_context")},
	)
	if policy == nil {
		t.Fatal("policy is nil")
	}
	if got := policy.nextTool(); got != "missing_tool" {
		t.Fatalf("nextTool = %q", got)
	}
	if policy.noToolRetries != 5 {
		t.Fatalf("noToolRetries = %d, want capped 5", policy.noToolRetries)
	}
	policy.markSuccessfulTool("wrong_tool")
	if got := policy.nextTool(); got != "missing_tool" {
		t.Fatalf("wrong tool advanced sequence to %q", got)
	}
	policy.markSuccessfulTool("missing_tool")
	if got := policy.nextTool(); got != "mcp_salesko_graph_read_job_context" {
		t.Fatalf("nextTool after missing = %q", got)
	}
	if !hasToolName([]provider.Tool{namedTool("mcp_salesko_graph_read_job_context")}, "mcp_salesko_graph_read_job_context") {
		t.Fatal("hasToolName should find registered tool")
	}
}

func TestRequiredToolSequenceFromParamsIgnoresMissingPolicy(t *testing.T) {
	if policy := requiredToolSequenceFromParams(map[string]any{}, []provider.Tool{namedTool("x")}); policy != nil {
		t.Fatalf("policy = %#v, want nil", policy)
	}
	if policy := requiredToolSequenceFromParams(
		map[string]any{"fastclaw_tool_policy": map[string]any{"required_sequence": []any{}}},
		[]provider.Tool{namedTool("x")},
	); policy != nil {
		t.Fatalf("policy = %#v, want nil when sequence is empty", policy)
	}
}

func TestRequiredToolSequenceFromTextReadsSaleskoPayload(t *testing.T) {
	prompt := `Salesko relationship research job.
Use the JSON payload below as the source of truth for this dispatch.
{
  "task": "salesko_relationship_research",
  "required_tools": {
    "first_call": "mcp_salesko_graph_read_job_context",
    "final_call": "mcp_salesko_graph_submit_proposal"
  },
  "tool_arguments": {
    "job_id": "job-1"
  }
}`
	policy := requiredToolSequenceFromText(prompt, []provider.Tool{
		namedTool("mcp_salesko_graph_read_job_context"),
		namedTool("mcp_salesko_graph_submit_proposal"),
	})
	if policy == nil {
		t.Fatal("policy is nil")
	}
	if got := policy.nextTool(); got != "mcp_salesko_graph_read_job_context" {
		t.Fatalf("nextTool = %q", got)
	}
	policy.markSuccessfulTool("mcp_salesko_graph_read_job_context")
	if got := policy.nextTool(); got != "mcp_salesko_graph_submit_proposal" {
		t.Fatalf("nextTool after read = %q", got)
	}
}

func TestRequiredSyntheticToolCallReadsSaleskoArgsFromParams(t *testing.T) {
	call, ok := requiredSyntheticToolCall(
		"mcp_salesko_graph_read_job_context",
		map[string]any{
			"salesko_job_id":  "job-1",
			"tenant_id":       "tenant-1",
			"dataset_scope":   "scope-1",
			"frame_record_id": "frame-1",
		},
		"",
		2,
	)
	if !ok {
		t.Fatal("synthetic tool call was not created")
	}
	if call.ID != "required_2_mcp_salesko_graph_read_job_context" {
		t.Fatalf("ID = %q", call.ID)
	}
	if call.Function.Name != "mcp_salesko_graph_read_job_context" {
		t.Fatalf("name = %q", call.Function.Name)
	}
	var args map[string]string
	if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
		t.Fatalf("arguments are not JSON: %v", err)
	}
	want := map[string]string{
		"job_id":          "job-1",
		"tenant_id":       "tenant-1",
		"dataset_scope":   "scope-1",
		"frame_record_id": "frame-1",
	}
	for key, value := range want {
		if args[key] != value {
			t.Fatalf("args[%s] = %q, want %q in %s", key, args[key], value, call.Function.Arguments)
		}
	}
}

func TestRequiredSyntheticToolCallReadsSaleskoArgsFromPrompt(t *testing.T) {
	prompt := `Salesko relationship research job.
{
  "tool_arguments": {
    "job_id": "job-from-text",
    "tenant_id": "tenant-from-text",
    "dataset_scope": "scope-from-text",
    "frame_record_id": "frame-from-text"
  }
}`
	call, ok := requiredSyntheticToolCall("mcp_salesko_graph_read_job_context", nil, prompt, 1)
	if !ok {
		t.Fatal("synthetic tool call was not created")
	}
	var args map[string]string
	if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
		t.Fatalf("arguments are not JSON: %v", err)
	}
	if args["job_id"] != "job-from-text" || args["frame_record_id"] != "frame-from-text" {
		t.Fatalf("unexpected arguments: %s", call.Function.Arguments)
	}
}

func TestSynthesizeRequiredToolCallClearsRawAssistant(t *testing.T) {
	policy := newRequiredToolSequencePolicy([]string{"mcp_salesko_graph_read_job_context"}, 2)
	resp := &provider.Response{
		Content:      "I will do that.",
		RawAssistant: json.RawMessage(`{"role":"assistant","content":"I will do that."}`),
	}
	ok := synthesizeRequiredToolCallOnNoTool(resp, policy, map[string]any{
		"salesko_job_id":  "job-1",
		"tenant_id":       "tenant-1",
		"frame_record_id": "frame-1",
	}, "", 1)
	if !ok {
		t.Fatal("expected synthetic tool call")
	}
	if resp.Content != "" {
		t.Fatalf("content = %q, want cleared", resp.Content)
	}
	if len(resp.RawAssistant) != 0 {
		t.Fatalf("RawAssistant was not cleared: %s", string(resp.RawAssistant))
	}
	if !resp.HasToolCalls() {
		t.Fatal("response has no tool calls")
	}
}

func TestRequiredSyntheticToolCallDoesNotInventSubmitProposal(t *testing.T) {
	if _, ok := requiredSyntheticToolCall("mcp_salesko_graph_submit_proposal", map[string]any{
		"salesko_job_id":  "job-1",
		"tenant_id":       "tenant-1",
		"frame_record_id": "frame-1",
	}, "", 1); ok {
		t.Fatal("submit proposal should not be synthesized without context/result payload")
	}
}

func namedTool(name string) provider.Tool {
	return provider.Tool{
		Type: "function",
		Function: provider.ToolFunction{
			Name:        name,
			Description: name,
			Parameters:  map[string]any{"type": "object"},
		},
	}
}
