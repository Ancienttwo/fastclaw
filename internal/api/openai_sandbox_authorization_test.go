package api

import (
	"net/http/httptest"
	"testing"

	"github.com/fastclaw-ai/fastclaw/internal/sandbox"
)

func TestWithSandboxAuthorizationMovesHeaderToContext(t *testing.T) {
	request := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	request.Header.Set(sandbox.AiphaBeeSandboxAuthorizationHeader, "run-scoped-secret")

	updated := withSandboxAuthorization(request)
	if got := updated.Header.Get(sandbox.AiphaBeeSandboxAuthorizationHeader); got != "" {
		t.Fatalf("authorization header retained after context transfer: %q", got)
	}
	if got := sandbox.AuthorizationFromContext(updated.Context()); got != "run-scoped-secret" {
		t.Fatalf("context authorization = %q, want run-scoped-secret", got)
	}
}

func TestWithSandboxAuthorizationLeavesMissingTokenAbsent(t *testing.T) {
	request := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	updated := withSandboxAuthorization(request)
	if got := sandbox.AuthorizationFromContext(updated.Context()); got != "" {
		t.Fatalf("unexpected context authorization: %q", got)
	}
}
