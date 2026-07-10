package config

import "testing"

func TestLoadEnvCloudflareSandboxBridge(t *testing.T) {
	t.Setenv("FASTCLAW_SANDBOX_BACKEND", "cloudflare")
	t.Setenv("FASTCLAW_SANDBOX_CLOUDFLARE_URL", "https://bridge.example.test")

	env := LoadEnv()
	if !env.Sandbox.Enabled || env.Sandbox.Backend != "cloudflare" {
		t.Fatalf("sandbox env = enabled:%v backend:%q", env.Sandbox.Enabled, env.Sandbox.Backend)
	}
	var cfg Config
	env.ApplyToConfig(&cfg)
	if cfg.Sandbox.CloudflareURL != "https://bridge.example.test" {
		t.Fatalf("CloudflareURL = %q", cfg.Sandbox.CloudflareURL)
	}
}
