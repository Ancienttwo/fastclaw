package config

import "testing"

func TestMergedAgentConfigCarriesPromptModeAndBuiltinToolsDefaults(t *testing.T) {
	cfg := &Config{
		Agents: AgentsConfig{
			Defaults: AgentDefaults{
				PromptMode:   PromptModeCustomize,
				BuiltinTools: []string{"web_fetch", "web_search"},
			},
		},
	}

	got := cfg.MergedAgentConfig(AgentEntry{ID: "agent-a"})
	if got.PromptMode != PromptModeCustomize {
		t.Fatalf("PromptMode = %q, want %q", got.PromptMode, PromptModeCustomize)
	}
	if len(got.BuiltinTools) != 2 || got.BuiltinTools[0] != "web_fetch" || got.BuiltinTools[1] != "web_search" {
		t.Fatalf("BuiltinTools = %#v, want web_fetch/web_search", got.BuiltinTools)
	}
}

func TestMergedAgentConfigPreservesEmptyBuiltinToolsOverride(t *testing.T) {
	cfg := &Config{
		Agents: AgentsConfig{
			Defaults: AgentDefaults{BuiltinTools: []string{"web_fetch"}},
		},
	}

	got := cfg.MergedAgentConfig(AgentEntry{ID: "agent-a", BuiltinTools: []string{}})
	if got.BuiltinTools == nil {
		t.Fatal("BuiltinTools is nil, want explicit empty override")
	}
	if len(got.BuiltinTools) != 0 {
		t.Fatalf("BuiltinTools = %#v, want empty override", got.BuiltinTools)
	}
}

func TestMergedAgentConfigFileBuiltinToolsOverridesEntry(t *testing.T) {
	orig := AgentFileConfigLoader
	defer func() { AgentFileConfigLoader = orig }()
	AgentFileConfigLoader = func(agentID, home string) (AgentFileConfig, bool) {
		return AgentFileConfig{BuiltinTools: []string{"web_search"}}, true
	}

	cfg := &Config{}
	got := cfg.MergedAgentConfig(AgentEntry{ID: "agent-a", BuiltinTools: []string{"web_fetch"}})
	if len(got.BuiltinTools) != 1 || got.BuiltinTools[0] != "web_search" {
		t.Fatalf("BuiltinTools = %#v, want file override web_search", got.BuiltinTools)
	}
}
