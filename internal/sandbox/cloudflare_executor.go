package sandbox

import (
	"bufio"
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

const maxCloudflareResponseBytes = 2 << 20

var cloudflareSandboxIDPattern = regexp.MustCompile(`^ab-[a-f0-9]{32}$`)

// CloudflareExecutor implements Executor through AiphaBee's authenticated
// Cloudflare Sandbox Bridge. The run token is captured from request context at
// pool creation and is never exposed through config, prompts, or logs.
type CloudflareExecutor struct {
	baseURL   string
	token     string
	sandboxID string
	client    *http.Client

	closeMu sync.Mutex
	closed  bool
}

type cloudflareCreateResponse struct {
	ID string `json:"id"`
}

func newCloudflareExecutor(ctx context.Context, baseURL, token string, client *http.Client) (*CloudflareExecutor, error) {
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("cloudflare sandbox: run authorization is required")
	}
	if client == nil {
		client = &http.Client{}
	}
	executor := &CloudflareExecutor{baseURL: strings.TrimRight(baseURL, "/"), token: token, client: client}
	response, err := executor.do(ctx, http.MethodPost, "/v1/sandbox", nil, "")
	if err != nil {
		return nil, fmt.Errorf("cloudflare sandbox create: %w", err)
	}
	defer response.Body.Close()
	if err := expectCloudflareStatus(response, http.StatusCreated); err != nil {
		return nil, fmt.Errorf("cloudflare sandbox create: %w", err)
	}
	payload, err := readCloudflareBody(response.Body)
	if err != nil {
		return nil, fmt.Errorf("cloudflare sandbox create response: %w", err)
	}
	var created cloudflareCreateResponse
	if err := json.Unmarshal(payload, &created); err != nil {
		return nil, fmt.Errorf("cloudflare sandbox create response: %w", err)
	}
	if !cloudflareSandboxIDPattern.MatchString(created.ID) {
		return nil, fmt.Errorf("cloudflare sandbox create response: invalid id")
	}
	executor.sandboxID = created.ID
	return executor, nil
}

func (e *CloudflareExecutor) do(
	ctx context.Context,
	method string,
	path string,
	body io.Reader,
	contentType string,
) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, e.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+e.token)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	return e.client.Do(request)
}

func expectCloudflareStatus(response *http.Response, allowed ...int) error {
	for _, status := range allowed {
		if response.StatusCode == status {
			return nil
		}
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxCloudflareResponseBytes))
	return fmt.Errorf("bridge returned HTTP %d", response.StatusCode)
}

func readCloudflareBody(reader io.Reader) ([]byte, error) {
	content, err := io.ReadAll(io.LimitReader(reader, maxCloudflareResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(content) > maxCloudflareResponseBytes {
		return nil, fmt.Errorf("bridge response exceeded %d bytes", maxCloudflareResponseBytes)
	}
	return content, nil
}

func (e *CloudflareExecutor) Exec(ctx context.Context, command string, timeout time.Duration) (string, error) {
	if timeout <= 0 || timeout > 10*time.Minute {
		return "", fmt.Errorf("cloudflare sandbox exec: timeout must be between 1ns and 10m")
	}
	body, err := json.Marshal(map[string]any{
		"argv":       []string{"sh", "-lc", command},
		"cwd":        "/workspace",
		"timeout_ms": timeout.Milliseconds(),
	})
	if err != nil {
		return "", fmt.Errorf("cloudflare sandbox exec request: %w", err)
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout+5*time.Second)
	defer cancel()
	response, err := e.do(
		execCtx,
		http.MethodPost,
		"/v1/sandbox/"+url.PathEscape(e.sandboxID)+"/exec",
		bytes.NewReader(body),
		"application/json",
	)
	if err != nil {
		if errors.Is(execCtx.Err(), context.DeadlineExceeded) || errors.Is(execCtx.Err(), context.Canceled) {
			return "", fmt.Errorf("cloudflare sandbox exec: %w", execCtx.Err())
		}
		return "", fmt.Errorf("cloudflare sandbox exec: %w", err)
	}
	defer response.Body.Close()
	if err := expectCloudflareStatus(response, http.StatusOK); err != nil {
		return "", fmt.Errorf("cloudflare sandbox exec: %w", err)
	}
	return parseCloudflareExecSSE(io.LimitReader(response.Body, maxCloudflareResponseBytes))
}

func parseCloudflareExecSSE(reader io.Reader) (string, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), maxCloudflareResponseBytes)
	event := ""
	data := ""
	var output strings.Builder
	exitSeen := false
	exitCode := 0

	flush := func() error {
		if event == "" {
			data = ""
			return nil
		}
		switch event {
		case "stdout", "stderr":
			decoded, err := base64.StdEncoding.DecodeString(data)
			if err != nil {
				return fmt.Errorf("cloudflare sandbox exec: invalid %s frame", event)
			}
			output.Write(decoded)
		case "exit":
			var result struct {
				ExitCode int `json:"exit_code"`
			}
			if err := json.Unmarshal([]byte(data), &result); err != nil {
				return fmt.Errorf("cloudflare sandbox exec: invalid exit frame")
			}
			exitSeen = true
			exitCode = result.ExitCode
		case "error":
			return fmt.Errorf("cloudflare sandbox exec: bridge reported an execution error")
		default:
			return fmt.Errorf("cloudflare sandbox exec: unexpected SSE event %q", event)
		}
		event = ""
		data = ""
		return nil
	}

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				return output.String(), err
			}
			continue
		}
		if strings.HasPrefix(line, "event: ") {
			event = strings.TrimPrefix(line, "event: ")
		}
		if strings.HasPrefix(line, "data: ") {
			data = strings.TrimPrefix(line, "data: ")
		}
	}
	if err := scanner.Err(); err != nil {
		return output.String(), fmt.Errorf("cloudflare sandbox exec stream: %w", err)
	}
	if event != "" {
		if err := flush(); err != nil {
			return output.String(), err
		}
	}
	if !exitSeen {
		return output.String(), fmt.Errorf("cloudflare sandbox exec: missing terminal exit event")
	}
	if exitCode != 0 {
		return output.String(), fmt.Errorf("cloudflare sandbox exec: exit code %d", exitCode)
	}
	return output.String(), nil
}

func (e *CloudflareExecutor) ReadFile(ctx context.Context, path string) (string, error) {
	response, err := e.do(
		ctx,
		http.MethodGet,
		"/v1/sandbox/"+url.PathEscape(e.sandboxID)+"/file/"+url.PathEscape(strings.TrimPrefix(path, "/")),
		nil,
		"",
	)
	if err != nil {
		return "", fmt.Errorf("cloudflare sandbox read file: %w", err)
	}
	defer response.Body.Close()
	if err := expectCloudflareStatus(response, http.StatusOK); err != nil {
		return "", fmt.Errorf("cloudflare sandbox read file: %w", err)
	}
	content, err := readCloudflareBody(response.Body)
	if err != nil {
		return "", fmt.Errorf("cloudflare sandbox read file: %w", err)
	}
	return string(content), nil
}

func (e *CloudflareExecutor) WriteFile(ctx context.Context, path, content string) (string, error) {
	response, err := e.do(
		ctx,
		http.MethodPut,
		"/v1/sandbox/"+url.PathEscape(e.sandboxID)+"/file/"+url.PathEscape(strings.TrimPrefix(path, "/")),
		strings.NewReader(content),
		"text/plain; charset=utf-8",
	)
	if err != nil {
		return "", fmt.Errorf("cloudflare sandbox write file: %w", err)
	}
	defer response.Body.Close()
	if err := expectCloudflareStatus(response, http.StatusNoContent); err != nil {
		return "", fmt.Errorf("cloudflare sandbox write file: %w", err)
	}
	return fmt.Sprintf("Wrote %d bytes to %s", len(content), path), nil
}

func (e *CloudflareExecutor) ListDir(ctx context.Context, path string) (string, error) {
	query := url.Values{"path": []string{path}}.Encode()
	response, err := e.do(
		ctx,
		http.MethodGet,
		"/v1/sandbox/"+url.PathEscape(e.sandboxID)+"/files?"+query,
		nil,
		"",
	)
	if err != nil {
		return "", fmt.Errorf("cloudflare sandbox list dir: %w", err)
	}
	defer response.Body.Close()
	if err := expectCloudflareStatus(response, http.StatusOK); err != nil {
		return "", fmt.Errorf("cloudflare sandbox list dir: %w", err)
	}
	listing, err := readCloudflareBody(response.Body)
	if err != nil {
		return "", fmt.Errorf("cloudflare sandbox list dir: %w", err)
	}
	return string(listing), nil
}

func (e *CloudflareExecutor) Close() error {
	e.closeMu.Lock()
	defer e.closeMu.Unlock()
	if e.closed {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	path := "/v1/sandbox/" + url.PathEscape(e.sandboxID)
	response, err := e.do(ctx, http.MethodDelete, path, nil, "")
	if err != nil {
		return fmt.Errorf("cloudflare sandbox destroy: %w", err)
	}
	response.Body.Close()
	if err := expectCloudflareStatus(response, http.StatusNoContent); err != nil {
		return fmt.Errorf("cloudflare sandbox destroy: %w", err)
	}

	statusResponse, err := e.do(ctx, http.MethodGet, path+"/running", nil, "")
	if err != nil {
		return fmt.Errorf("cloudflare sandbox destroy readback: %w", err)
	}
	defer statusResponse.Body.Close()
	if err := expectCloudflareStatus(statusResponse, http.StatusOK); err != nil {
		return fmt.Errorf("cloudflare sandbox destroy readback: %w", err)
	}
	var status struct {
		Running  bool `json:"running"`
		Terminal bool `json:"terminal"`
	}
	payload, err := readCloudflareBody(statusResponse.Body)
	if err != nil {
		return fmt.Errorf("cloudflare sandbox destroy readback: %w", err)
	}
	if err := json.Unmarshal(payload, &status); err != nil {
		return fmt.Errorf("cloudflare sandbox destroy readback: %w", err)
	}
	if status.Running || !status.Terminal {
		return fmt.Errorf("cloudflare sandbox destroy readback: sandbox is not terminal")
	}
	e.closed = true
	return nil
}

func (e *CloudflareExecutor) Backend() string { return "cloudflare" }

func (e *CloudflareExecutor) IsRemoteWorkspace() {}

// CloudflareExecutorPool holds one run-scoped executor per FastClaw sandbox
// scope. Reusing a scope with a different token is rejected to prevent a new
// run from inheriting a prior run's sandbox identity.
type CloudflareExecutorPool struct {
	baseURL   string
	client    *http.Client
	configErr error

	mu        sync.Mutex
	executors map[string]*CloudflareExecutor
	inflight  map[string]*cloudflareExecutorCreation
}

type cloudflareExecutorCreation struct {
	done     chan struct{}
	err      error
	executor *CloudflareExecutor
	token    string
}

func NewCloudflareExecutorPool(baseURL string, requestTimeout time.Duration) *CloudflareExecutorPool {
	if requestTimeout <= 0 {
		requestTimeout = 30 * time.Second
	}
	pool := &CloudflareExecutorPool{
		baseURL:   strings.TrimRight(baseURL, "/"),
		client:    &http.Client{Timeout: requestTimeout},
		executors: make(map[string]*CloudflareExecutor),
		inflight:  make(map[string]*cloudflareExecutorCreation),
	}
	parsed, err := url.Parse(pool.baseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && !isLoopbackBridge(parsed)) {
		pool.configErr = fmt.Errorf("cloudflare sandbox bridge URL must use https (http is allowed only for loopback tests)")
	}
	return pool
}

func isLoopbackBridge(parsed *url.URL) bool {
	if parsed.Scheme != "http" {
		return false
	}
	host := parsed.Hostname()
	return host == "127.0.0.1" || host == "::1" || host == "localhost"
}

func (p *CloudflareExecutorPool) Get(ctx context.Context, agentID, projectID, sessionID string) (Executor, error) {
	if p.configErr != nil {
		return nil, p.configErr
	}
	token := AuthorizationFromContext(ctx)
	if token == "" {
		return nil, fmt.Errorf("cloudflare sandbox: %s header is required", AiphaBeeSandboxAuthorizationHeader)
	}
	key := poolKey(agentID, projectID, sessionID)
	p.mu.Lock()
	if existing := p.executors[key]; existing != nil {
		if !constantTimeStringEqual(existing.token, token) {
			p.mu.Unlock()
			return nil, fmt.Errorf("cloudflare sandbox: run authorization changed for an active scope")
		}
		p.mu.Unlock()
		return existing, nil
	}
	if pending := p.inflight[key]; pending != nil {
		if !constantTimeStringEqual(pending.token, token) {
			p.mu.Unlock()
			return nil, fmt.Errorf("cloudflare sandbox: run authorization changed for an active scope")
		}
		done := pending.done
		p.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("cloudflare sandbox create: %w", ctx.Err())
		case <-done:
			if pending.err != nil {
				return nil, pending.err
			}
			return pending.executor, nil
		}
	}
	pending := &cloudflareExecutorCreation{done: make(chan struct{}), token: token}
	p.inflight[key] = pending
	p.mu.Unlock()

	executor, err := newCloudflareExecutor(ctx, p.baseURL, token, p.client)
	p.mu.Lock()
	pending.err = err
	pending.executor = executor
	delete(p.inflight, key)
	if err == nil {
		p.executors[key] = executor
	}
	close(pending.done)
	p.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return executor, nil
}

func constantTimeStringEqual(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func (p *CloudflareExecutorPool) Release(agentID, projectID, sessionID string) error {
	key := poolKey(agentID, projectID, sessionID)
	p.mu.Lock()
	if p.inflight[key] != nil {
		p.mu.Unlock()
		return fmt.Errorf("cloudflare sandbox release: creation in progress")
	}
	executor := p.executors[key]
	p.mu.Unlock()
	if executor == nil {
		return nil
	}
	if err := executor.Close(); err != nil {
		return err
	}
	p.mu.Lock()
	if p.executors[key] == executor {
		delete(p.executors, key)
	}
	p.mu.Unlock()
	return nil
}

func (p *CloudflareExecutorPool) ForgetExternallyManaged(agentID, projectID, sessionID string) bool {
	key := poolKey(agentID, projectID, sessionID)
	p.mu.Lock()
	if p.inflight[key] != nil {
		p.mu.Unlock()
		return false
	}
	executor := p.executors[key]
	if executor != nil {
		delete(p.executors, key)
	}
	p.mu.Unlock()
	if executor == nil {
		return true
	}
	executor.closeMu.Lock()
	executor.closed = true
	executor.token = ""
	executor.closeMu.Unlock()
	return true
}

func (p *CloudflareExecutorPool) CloseAll() {
	p.mu.Lock()
	executors := p.executors
	p.executors = make(map[string]*CloudflareExecutor)
	p.mu.Unlock()
	for _, executor := range executors {
		executor.closeMu.Lock()
		executor.closed = true
		executor.token = ""
		executor.closeMu.Unlock()
	}
}

func (p *CloudflareExecutorPool) Backend() string { return "cloudflare" }

var (
	_ Executor                      = (*CloudflareExecutor)(nil)
	_ RemoteWorkspace               = (*CloudflareExecutor)(nil)
	_ ExecutorPool                  = (*CloudflareExecutorPool)(nil)
	_ ExternallyManagedExecutorPool = (*CloudflareExecutorPool)(nil)
)
