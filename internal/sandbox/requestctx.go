package sandbox

import (
	"context"
	"strings"
)

const AiphaBeeSandboxAuthorizationHeader = "X-AiphaBee-Sandbox-Authorization"

type authorizationCtxKey struct{}

// WithAuthorization carries a run-scoped sandbox token through the agent turn.
// The value is intentionally context-only: it must never enter Params, the LLM
// prompt, persisted session messages, or structured logs.
func WithAuthorization(ctx context.Context, token string) context.Context {
	token = strings.TrimSpace(token)
	if token == "" {
		return ctx
	}
	return context.WithValue(ctx, authorizationCtxKey{}, token)
}

// AuthorizationFromContext returns the run-scoped token for remote sandbox
// creation. Callers must treat the value as a secret and never include it in
// errors or logs.
func AuthorizationFromContext(ctx context.Context) string {
	if value, ok := ctx.Value(authorizationCtxKey{}).(string); ok {
		return value
	}
	return ""
}
