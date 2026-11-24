package auth

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/kvstore"
	internallogging "github.com/router-for-me/CLIProxyAPI/v8/internal/logging"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

type failingThenSucceedingExecutor struct {
	id         string
	failAuthID string
	failErr    error
	refreshErr error
}

func (e *failingThenSucceedingExecutor) Identifier() string { return e.id }

func (e *failingThenSucceedingExecutor) Execute(_ context.Context, auth *Auth, _ cliproxyexecutor.Request, _ cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	if auth != nil && auth.ID == e.failAuthID {
		return cliproxyexecutor.Response{}, e.failErr
	}
	return cliproxyexecutor.Response{Payload: []byte(`{"id":"ok"}`)}, nil
}

func (e *failingThenSucceedingExecutor) ExecuteStream(_ context.Context, _ *Auth, _ cliproxyexecutor.Request, _ cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	return nil, errors.New("not implemented")
}

func (e *failingThenSucceedingExecutor) Refresh(_ context.Context, auth *Auth) (*Auth, error) {
	if e.refreshErr != nil {
		return nil, e.refreshErr
	}
	return auth, nil
}

func (e *failingThenSucceedingExecutor) CountTokens(_ context.Context, _ *Auth, _ cliproxyexecutor.Request, _ cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, errors.New("not implemented")
}

func (e *failingThenSucceedingExecutor) HttpRequest(_ context.Context, _ *Auth, _ *http.Request) (*http.Response, error) {
	return nil, errors.New("not implemented")
}

func TestAuthErrorHistoryPersistsAcrossRestartAndPreservesFailover(t *testing.T) {
	tempDir := t.TempDir()
	kvPath := filepath.Join(tempDir, "kv.db")
	if errCfg := kvstore.ConfigureLocal(true, kvPath); errCfg != nil {
		t.Fatalf("ConfigureLocal: %v", errCfg)
	}
	t.Cleanup(func() {
		_ = kvstore.ShutdownLocal()
	})

	ctx := internallogging.WithRequestID(context.Background(), "req-failover-12345678")
	m1 := NewManager(nil, &RoundRobinSelector{}, nil)
	exec := &failingThenSucceedingExecutor{
		id:         "claude",
		failAuthID: "acct-a.json",
		failErr: &Error{
			Code:       "rate_limit_error",
			Message:    "upstream 429:\n{\"error\":{\"message\":\"quota exceeded for Bearer sk-secret-token-123\"}}",
			Retryable:  true,
			HTTPStatus: http.StatusTooManyRequests,
		},
	}
	m1.RegisterExecutor(exec)
	if _, errReg := m1.Register(ctx, &Auth{
		ID:       "acct-a.json",
		FileName: "acct-a.json",
		Provider: "claude",
		Status:   StatusActive,
	}); errReg != nil {
		t.Fatalf("Register acct-a: %v", errReg)
	}
	if _, errReg := m1.Register(ctx, &Auth{
		ID:       "acct-b.json",
		FileName: "acct-b.json",
		Provider: "claude",
		Status:   StatusActive,
	}); errReg != nil {
		t.Fatalf("Register acct-b: %v", errReg)
	}
	reg := registry.GetGlobalRegistry()
	reg.RegisterClient("acct-a.json", "claude", []*registry.ModelInfo{{ID: "claude-sonnet-4", Object: "model"}})
	reg.RegisterClient("acct-b.json", "claude", []*registry.ModelInfo{{ID: "claude-sonnet-4", Object: "model"}})
	t.Cleanup(func() {
		reg.UnregisterClient("acct-a.json")
		reg.UnregisterClient("acct-b.json")
	})

	// Execute should fail on acct-a and fail over seamlessly to acct-b, returning success.
	resp, errExec := m1.Execute(ctx, []string{"claude"}, cliproxyexecutor.Request{Model: "claude-sonnet-4"}, cliproxyexecutor.Options{})
	if errExec != nil {
		t.Fatalf("Execute failover failed unexpectedly: %v", errExec)
	}
	if string(resp.Payload) != `{"id":"ok"}` {
		t.Fatalf("unexpected failover payload: %q", string(resp.Payload))
	}

	// acct-a must have its failure recorded with redaction, while acct-b has none.
	histA := m1.AuthErrorHistory(ctx, "acct-a.json", 10)
	if len(histA) != 1 {
		t.Fatalf("expected 1 error record for acct-a, got %d", len(histA))
	}
	if histA[0].HTTPStatus != http.StatusTooManyRequests || histA[0].Code != "rate_limit_error" {
		t.Fatalf("unexpected error record metadata: %+v", histA[0])
	}
	if histA[0].RequestID != "req-failover-12345678" {
		t.Fatalf("expected request_id to be captured, got %q", histA[0].RequestID)
	}
	if strings.Contains(histA[0].Message, "sk-secret-token-123") {
		t.Fatalf("secret leaked in error message: %q", histA[0].Message)
	}
	if !strings.Contains(histA[0].Message, "[REDACTED]") || !strings.Contains(histA[0].Message, "\n") {
		t.Fatalf("expected redacted multiline error detail, got %q", histA[0].Message)
	}
	if len(m1.AuthErrorHistory(ctx, "acct-b.json", 10)) != 0 {
		t.Fatalf("expected 0 error records for healthy acct-b")
	}

	// Even if acct-a later succeeds and clears LastError/StatusMessage, error history remains.
	m1.MarkResult(ctx, Result{
		AuthID:   "acct-a.json",
		Provider: "claude",
		Model:    "claude-sonnet-4",
		Success:  true,
	})
	if summary := m1.AuthErrorSummary(ctx, "acct-a.json"); summary.Total != 1 || summary.Latest == nil {
		t.Fatalf("expected persisted history to survive subsequent success, got %+v", summary)
	}

	// Simulate process restart by reopening bbolt and constructing a brand-new Manager.
	if errShutdown := kvstore.ShutdownLocal(); errShutdown != nil {
		t.Fatalf("ShutdownLocal: %v", errShutdown)
	}
	if errReopen := kvstore.ConfigureLocal(true, kvPath); errReopen != nil {
		t.Fatalf("reopen ConfigureLocal: %v", errReopen)
	}
	m2 := NewManager(nil, &RoundRobinSelector{}, nil)
	reloaded := m2.AuthErrorHistory(context.Background(), "acct-a.json", 10)
	if len(reloaded) != 1 {
		t.Fatalf("expected 1 persisted error record after restart, got %d", len(reloaded))
	}
	if reloaded[0].ID != histA[0].ID || reloaded[0].Model != "claude-sonnet-4" || reloaded[0].HTTPStatus != http.StatusTooManyRequests {
		t.Fatalf("reloaded record mismatch: got %+v want %+v", reloaded[0], histA[0])
	}

	// Clear deletes both memory and persisted kvstore entry.
	m2.ClearAuthErrorHistory(context.Background(), "acct-a.json")
	m3 := NewManager(nil, &RoundRobinSelector{}, nil)
	if afterClear := m3.AuthErrorHistory(context.Background(), "acct-a.json", 10); len(afterClear) != 0 {
		t.Fatalf("expected empty history after clear, got %d", len(afterClear))
	}
}

func TestAuthErrorHistoryBoundsAndRefreshRecording(t *testing.T) {
	ctx := context.Background()
	m := NewManager(nil, &RoundRobinSelector{}, nil)
	baseTime := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	step := 0
	m.ensureErrorHistory().nowFunc = func() time.Time {
		step++
		return baseTime.Add(time.Duration(step) * time.Second)
	}

	exec := &failingThenSucceedingExecutor{
		id:         "codex",
		refreshErr: &Error{Code: "invalid_grant", Message: "refresh_token=tok_secret_999 revoked", HTTPStatus: http.StatusUnauthorized},
	}
	m.RegisterExecutor(exec)
	if _, errReg := m.Register(ctx, &Auth{
		ID:       "codex-1.json",
		FileName: "codex-1.json",
		Provider: "codex",
		Status:   StatusActive,
		Metadata: map[string]any{"refresh_token": "rt-old"},
	}); errReg != nil {
		t.Fatalf("Register: %v", errReg)
	}

	if _, errRefresh := m.ForceRefreshAuth(ctx, "codex-1.json"); errRefresh == nil {
		t.Fatalf("expected refresh failure")
	}
	hist := m.AuthErrorHistory(ctx, "codex-1.json", 10)
	if len(hist) != 1 {
		t.Fatalf("expected 1 refresh error record, got %d", len(hist))
	}
	if hist[0].Phase != "refresh" || hist[0].HTTPStatus != http.StatusUnauthorized {
		t.Fatalf("unexpected refresh error record: %+v", hist[0])
	}
	if strings.Contains(hist[0].Message, "tok_secret_999") {
		t.Fatalf("refresh token leaked in error record: %q", hist[0].Message)
	}

	// Append more than max capacity and verify newest-first ordering and clamping.
	for i := 0; i < authErrorHistoryMaxPerAuth+10; i++ {
		m.MarkResult(ctx, Result{
			AuthID:   "codex-1.json",
			Provider: "codex",
			Model:    "gpt-6",
			Phase:    "execute",
			Success:  false,
			Error:    &Error{Code: "upstream_error", Message: "boom", HTTPStatus: http.StatusBadGateway},
		})
	}
	all := m.AuthErrorHistory(ctx, "codex-1.json", 100)
	if len(all) != authErrorHistoryMaxPerAuth {
		t.Fatalf("expected clamped length %d, got %d", authErrorHistoryMaxPerAuth, len(all))
	}
	if !all[0].Timestamp.After(all[len(all)-1].Timestamp) {
		t.Fatalf("expected newest-first ordering: first=%s last=%s", all[0].Timestamp, all[len(all)-1].Timestamp)
	}
}
