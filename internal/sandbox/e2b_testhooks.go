// Added by SalesKo: exported E2B endpoint override for cross-package
// integration tests (see internal/runtime's regression test for the
// admin erase-user cascade's ctx-tagging fix).

package sandbox

// SetE2BEndpointsForTest redirects both the sandbox-management API
// (create via e2bBaseURL, used by newE2BExecutor/deleteE2BSandbox) and
// the per-sandbox envd exec endpoint (envdURL, used by Hydrate/Exec/
// ReadFile/WriteFile) to baseURL — typically a local httptest.Server —
// for the duration of a test.
//
// Exists because e2bBaseURL and e2bEnvdURLFunc are unexported: a test in
// THIS package can mutate them directly, but a test in another package
// that constructs a real *E2BExecutorPool (e.g. internal/runtime, which
// borrows the gateway's shared pool for its preview path and needs an
// end-to-end regression test proving sandboxes it creates get a durable
// binding + metadata tag) has no other way to keep that pool's HTTP
// calls off the real E2B API.
//
// Returns a restore func; callers should defer it (or register it via
// t.Cleanup) so a later test in the same binary doesn't inherit a
// dangling override.
func SetE2BEndpointsForTest(baseURL string) (restore func()) {
	origBase := e2bBaseURL
	origEnvd := e2bEnvdURLFunc
	e2bBaseURL = baseURL
	e2bEnvdURLFunc = func(sandboxID string) string { return baseURL }
	return func() {
		e2bBaseURL = origBase
		e2bEnvdURLFunc = origEnvd
	}
}
