package setup

import "testing"

func TestSanitizeBuiltinToolNamesTrimsAndDeduplicates(t *testing.T) {
	got, err := sanitizeBuiltinToolNames([]string{" web_fetch ", "web_search", "web_fetch"})
	if err != nil {
		t.Fatalf("sanitizeBuiltinToolNames returned error: %v", err)
	}
	if len(got) != 2 || got[0] != "web_fetch" || got[1] != "web_search" {
		t.Fatalf("sanitizeBuiltinToolNames = %#v, want trimmed unique names", got)
	}
}

func TestSanitizeBuiltinToolNamesRejectsUnknown(t *testing.T) {
	if _, err := sanitizeBuiltinToolNames([]string{"mcp_salesko_graph_read_job_context"}); err == nil {
		t.Fatal("sanitizeBuiltinToolNames accepted non-builtin MCP tool")
	}
}
