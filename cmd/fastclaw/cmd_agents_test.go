package main

import "testing"

func TestAgentsInitNoStartSkipsGateway(t *testing.T) {
	t.Setenv("FASTCLAW_HOME", t.TempDir())
	t.Setenv("FASTCLAW_STORAGE_TYPE", "sqlite")
	t.Setenv("FASTCLAW_STORAGE_DSN", "")

	called := 0
	original := ensureGatewayAfterAgentInit
	ensureGatewayAfterAgentInit = func() { called++ }
	t.Cleanup(func() { ensureGatewayAfterAgentInit = original })

	cmd := agentsInitCmd()
	cmd.SetArgs([]string{"aiphabee-template", "--no-start"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("agents init --no-start: %v", err)
	}
	if called != 0 {
		t.Fatalf("gateway start hook called %d times; want 0", called)
	}
}

func TestAgentsInitStartsGatewayByDefault(t *testing.T) {
	t.Setenv("FASTCLAW_HOME", t.TempDir())
	t.Setenv("FASTCLAW_STORAGE_TYPE", "sqlite")
	t.Setenv("FASTCLAW_STORAGE_DSN", "")

	called := 0
	original := ensureGatewayAfterAgentInit
	ensureGatewayAfterAgentInit = func() { called++ }
	t.Cleanup(func() { ensureGatewayAfterAgentInit = original })

	cmd := agentsInitCmd()
	cmd.SetArgs([]string{"default-template"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("agents init: %v", err)
	}
	if called != 1 {
		t.Fatalf("gateway start hook called %d times; want 1", called)
	}
}

func TestAgentsInitEnsureIsIdempotent(t *testing.T) {
	t.Setenv("FASTCLAW_HOME", t.TempDir())
	t.Setenv("FASTCLAW_STORAGE_TYPE", "sqlite")
	t.Setenv("FASTCLAW_STORAGE_DSN", "")

	for attempt := 1; attempt <= 2; attempt++ {
		cmd := agentsInitCmd()
		cmd.SetArgs([]string{
			"AiphaBee Template",
			"--id", "agt_aiphabee_template",
			"--ensure",
			"--no-start",
		})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("agents init --ensure attempt %d: %v", attempt, err)
		}
	}
}
