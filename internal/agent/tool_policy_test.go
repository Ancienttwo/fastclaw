package agent

import (
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
