package provider

import (
	"context"
	"encoding/json"
	"io"
	"testing"
)

func TestOpenAIToolChoiceNamedFunction(t *testing.T) {
	p := NewOpenAI("key", "https://api.example.test/v1")
	req, err := p.buildRequest(
		context.Background(),
		[]Message{{Role: "user", Content: "run the required tool"}},
		[]Tool{{
			Type: "function",
			Function: ToolFunction{
				Name:        "mcp_salesko_graph_read_job_context",
				Description: "Read bounded context.",
				Parameters:  map[string]any{"type": "object"},
			},
		}},
		"zai/glm-5.2",
		0,
		0,
		true,
		ChatOptions{ToolChoice: &ToolChoice{Name: "mcp_salesko_graph_read_job_context"}},
	)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	choice, ok := parsed["tool_choice"].(map[string]any)
	if !ok {
		t.Fatalf("tool_choice = %#v, want object", parsed["tool_choice"])
	}
	fn, ok := choice["function"].(map[string]any)
	if !ok || fn["name"] != "mcp_salesko_graph_read_job_context" {
		t.Fatalf("tool_choice.function = %#v", choice["function"])
	}
}
