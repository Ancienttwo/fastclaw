package agent

import (
	"testing"

	"github.com/fastclaw-ai/fastclaw/internal/provider"
)

func TestRequiredToolSequenceFromParamsFiltersUnavailableTools(t *testing.T) {
	policy := requiredToolSequenceFromParams(
		map[string]any{
			"fastclaw_tool_policy": map[string]any{
				"required_sequence":   []any{"missing_tool", "mcp_salesko_graph_read_job_context", "mcp_salesko_graph_submit_proposal"},
				"max_no_tool_retries": float64(7),
			},
		},
		[]provider.Tool{
			namedTool("mcp_salesko_graph_read_job_context"),
			namedTool("mcp_salesko_graph_submit_proposal"),
		},
	)
	if policy == nil {
		t.Fatal("policy is nil")
	}
	if got := policy.nextTool(); got != "mcp_salesko_graph_read_job_context" {
		t.Fatalf("nextTool = %q", got)
	}
	if policy.noToolRetries != 5 {
		t.Fatalf("noToolRetries = %d, want capped 5", policy.noToolRetries)
	}
	policy.markSuccessfulTool("wrong_tool")
	if got := policy.nextTool(); got != "mcp_salesko_graph_read_job_context" {
		t.Fatalf("wrong tool advanced sequence to %q", got)
	}
	policy.markSuccessfulTool("mcp_salesko_graph_read_job_context")
	if got := policy.nextTool(); got != "mcp_salesko_graph_submit_proposal" {
		t.Fatalf("nextTool after read = %q", got)
	}
	policy.markSuccessfulTool("mcp_salesko_graph_submit_proposal")
	if !policy.complete() {
		t.Fatal("policy should be complete")
	}
}

func TestRequiredToolSequenceFromParamsIgnoresMissingPolicy(t *testing.T) {
	if policy := requiredToolSequenceFromParams(map[string]any{}, []provider.Tool{namedTool("x")}); policy != nil {
		t.Fatalf("policy = %#v, want nil", policy)
	}
	if policy := requiredToolSequenceFromParams(
		map[string]any{"fastclaw_tool_policy": map[string]any{"required_sequence": []any{"missing"}}},
		[]provider.Tool{namedTool("x")},
	); policy != nil {
		t.Fatalf("policy = %#v, want nil when no requested tools are registered", policy)
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
