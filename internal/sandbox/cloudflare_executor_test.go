package sandbox

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeCloudflareBridge struct {
	mu              sync.Mutex
	token           string
	sandboxID       string
	fileContent     string
	destroyed       bool
	deleteCalls     int
	execDelay       time.Duration
	createDelay     time.Duration
	createCalls     int
	createActive    int
	maxCreateActive int
}

func (f *fakeCloudflareBridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+f.token {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method == http.MethodPost && r.URL.Path == "/v1/sandbox" {
		f.mu.Lock()
		f.createCalls++
		f.createActive++
		if f.createActive > f.maxCreateActive {
			f.maxCreateActive = f.createActive
		}
		delay := f.createDelay
		f.mu.Unlock()
		time.Sleep(delay)
		f.mu.Lock()
		f.createActive--
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"id":%q}`, f.sandboxID)
		return
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/exec"):
		delay := f.execDelay
		f.mu.Unlock()
		select {
		case <-r.Context().Done():
			f.mu.Lock()
			return
		case <-time.After(delay):
		}
		f.mu.Lock()
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: stdout\ndata: %s\n\n", base64.StdEncoding.EncodeToString([]byte("exec-ok\n")))
		fmt.Fprint(w, "event: exit\ndata: {\"exit_code\":0}\n\n")
	case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/file/"):
		body, _ := io.ReadAll(r.Body)
		f.fileContent = string(body)
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/file/"):
		fmt.Fprint(w, f.fileContent)
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/files"):
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"files":[{"path":"/workspace/smoke.txt"}]}`)
	case r.Method == http.MethodDelete && r.URL.Path == "/v1/sandbox/"+f.sandboxID:
		f.destroyed = true
		f.deleteCalls++
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/running"):
		w.Header().Set("Content-Type", "application/json")
		if f.destroyed {
			fmt.Fprint(w, `{"running":false,"terminal":true}`)
			return
		}
		fmt.Fprint(w, `{"running":true,"terminal":false}`)
	default:
		http.NotFound(w, r)
	}
}

func TestCloudflareExecutorLifecycle(t *testing.T) {
	bridge := &fakeCloudflareBridge{token: "run-token", sandboxID: "ab-0123456789abcdef0123456789abcdef"}
	server := httptest.NewServer(bridge)
	defer server.Close()

	pool := NewCloudflareExecutorPool(server.URL, 5*time.Second)
	ctx := WithAuthorization(context.Background(), bridge.token)
	executor, err := pool.Get(ctx, "agent-1", "", "session-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if executor.Backend() != "cloudflare" {
		t.Fatalf("Backend = %q", executor.Backend())
	}

	if _, err := executor.WriteFile(ctx, "/workspace/smoke.txt", "artifact"); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	content, err := executor.ReadFile(ctx, "/workspace/smoke.txt")
	if err != nil || content != "artifact" {
		t.Fatalf("ReadFile = %q, %v", content, err)
	}
	listing, err := executor.ListDir(ctx, "/workspace")
	if err != nil || !strings.Contains(listing, "smoke.txt") {
		t.Fatalf("ListDir = %q, %v", listing, err)
	}
	output, err := executor.Exec(ctx, "printf exec-ok", time.Second)
	if err != nil || output != "exec-ok\n" {
		t.Fatalf("Exec = %q, %v", output, err)
	}
	if err := pool.Release("agent-1", "", "session-1"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	bridge.mu.Lock()
	destroyed, deletes := bridge.destroyed, bridge.deleteCalls
	bridge.mu.Unlock()
	if !destroyed || deletes != 1 {
		t.Fatalf("destroy readback = destroyed:%v deletes:%d", destroyed, deletes)
	}
}

func TestCloudflareExecutorRequiresAndPinsRunAuthorization(t *testing.T) {
	bridge := &fakeCloudflareBridge{token: "run-token", sandboxID: "ab-0123456789abcdef0123456789abcdef"}
	server := httptest.NewServer(bridge)
	defer server.Close()
	pool := NewCloudflareExecutorPool(server.URL, 5*time.Second)

	if _, err := pool.Get(context.Background(), "agent", "", "session"); err == nil {
		t.Fatal("Get without authorization succeeded")
	}
	if _, err := pool.Get(WithAuthorization(context.Background(), bridge.token), "agent", "", "session"); err != nil {
		t.Fatalf("Get with authorization: %v", err)
	}
	if _, err := pool.Get(WithAuthorization(context.Background(), "different-token"), "agent", "", "session"); err == nil {
		t.Fatal("Get accepted a different token for an active scope")
	}
}

func TestCloudflareExecutorTimeoutLeavesDestroyToExternalOwner(t *testing.T) {
	bridge := &fakeCloudflareBridge{
		token:     "run-token",
		sandboxID: "ab-0123456789abcdef0123456789abcdef",
		execDelay: 250 * time.Millisecond,
	}
	server := httptest.NewServer(bridge)
	defer server.Close()
	pool := NewCloudflareExecutorPool(server.URL, time.Second)
	ctx := WithAuthorization(context.Background(), bridge.token)
	executor, err := pool.Get(ctx, "agent", "", "session")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if _, err := executor.Exec(timeoutCtx, "sleep 10", time.Second); err == nil {
		t.Fatal("Exec timeout succeeded")
	}
	bridge.mu.Lock()
	destroyed, deletes := bridge.destroyed, bridge.deleteCalls
	bridge.mu.Unlock()
	if destroyed || deletes != 0 {
		t.Fatalf("timeout provider cleanup = destroyed:%v deletes:%d", destroyed, deletes)
	}
	if !pool.ForgetExternallyManaged("agent", "", "session") {
		t.Fatal("external lifecycle forget was rejected")
	}
}

func TestCloudflareExecutorPoolCreatesDifferentScopesConcurrently(t *testing.T) {
	bridge := &fakeCloudflareBridge{
		token: "run-token", sandboxID: "ab-0123456789abcdef0123456789abcdef", createDelay: 75 * time.Millisecond,
	}
	server := httptest.NewServer(bridge)
	defer server.Close()
	pool := NewCloudflareExecutorPool(server.URL, time.Second)
	ctx := WithAuthorization(context.Background(), bridge.token)

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, session := range []string{"session-1", "session-2"} {
		wg.Add(1)
		go func(sessionID string) {
			defer wg.Done()
			_, err := pool.Get(ctx, "agent", "", sessionID)
			errs <- err
		}(session)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
	}
	bridge.mu.Lock()
	maxActive := bridge.maxCreateActive
	bridge.mu.Unlock()
	if maxActive != 2 {
		t.Fatalf("max concurrent creates = %d, want 2", maxActive)
	}
}

func TestCloudflareExecutorPoolCoalescesSameScopeAndForgetsToken(t *testing.T) {
	bridge := &fakeCloudflareBridge{
		token: "run-token", sandboxID: "ab-0123456789abcdef0123456789abcdef", createDelay: 50 * time.Millisecond,
	}
	server := httptest.NewServer(bridge)
	defer server.Close()
	pool := NewCloudflareExecutorPool(server.URL, time.Second)
	ctx := WithAuthorization(context.Background(), bridge.token)

	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := pool.Get(ctx, "agent", "", "session"); err != nil {
				t.Errorf("Get: %v", err)
			}
		}()
	}
	wg.Wait()
	bridge.mu.Lock()
	creates := bridge.createCalls
	bridge.mu.Unlock()
	if creates != 1 {
		t.Fatalf("create calls = %d, want 1", creates)
	}
	if !pool.ForgetExternallyManaged("agent", "", "session") {
		t.Fatal("external lifecycle forget was rejected")
	}
	if _, err := pool.Get(ctx, "agent", "", "session"); err != nil {
		t.Fatalf("Get after forget: %v", err)
	}
	bridge.mu.Lock()
	creates = bridge.createCalls
	bridge.mu.Unlock()
	if creates != 2 {
		t.Fatalf("create calls after forget = %d, want 2", creates)
	}
}

func TestParseCloudflareExecSSERejectsMissingTerminalEvent(t *testing.T) {
	data := "event: stdout\ndata: " + base64.StdEncoding.EncodeToString([]byte("partial")) + "\n\n"
	output, err := parseCloudflareExecSSE(strings.NewReader(data))
	if err == nil || output != "partial" {
		t.Fatalf("parse result = %q, %v", output, err)
	}
}
