package auth

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"sync"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	executor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/pool"
)

type poolTestExecutor struct {
	mu     sync.Mutex
	calls  []string
	fail   bool
	chunks chan executor.StreamChunk
}

func (*poolTestExecutor) Identifier() string { return "pool-test" }
func (e *poolTestExecutor) Execute(_ context.Context, a *Auth, _ executor.Request, _ executor.Options) (executor.Response, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, a.ID)
	if e.fail {
		return executor.Response{}, &Error{HTTPStatus: 503, Code: "unavailable", Message: "upstream unavailable"}
	}
	return executor.Response{Payload: []byte(a.ID)}, nil
}
func (e *poolTestExecutor) CountTokens(ctx context.Context, a *Auth, r executor.Request, o executor.Options) (executor.Response, error) {
	return e.Execute(ctx, a, r, o)
}
func (e *poolTestExecutor) ExecuteStream(ctx context.Context, a *Auth, r executor.Request, o executor.Options) (*executor.StreamResult, error) {
	if _, err := e.Execute(ctx, a, r, o); err != nil {
		return nil, err
	}
	if e.chunks != nil {
		return &executor.StreamResult{Chunks: e.chunks}, nil
	}
	ch := make(chan executor.StreamChunk, 1)
	ch <- executor.StreamChunk{Payload: []byte("ok")}
	close(ch)
	return &executor.StreamResult{Chunks: ch}, nil
}
func (*poolTestExecutor) Refresh(_ context.Context, a *Auth) (*Auth, error) { return a, nil }
func (*poolTestExecutor) HttpRequest(context.Context, *Auth, *http.Request) (*http.Response, error) {
	return nil, errors.New("unexpected direct execution")
}

func poolTestConfig() *config.Config {
	enabled := true
	return &config.Config{SDKConfig: config.SDKConfig{
		AccountPools: config.AccountPoolsConfig{Enabled: true, Pools: []config.AccountPool{
			{ID: "basic", Name: "Basic", Enabled: &enabled}, {ID: "premium", Name: "Premium", Enabled: &enabled}, {ID: "empty", Name: "Empty", Enabled: &enabled},
		}},
		PoolClients: []config.PoolClient{{ID: "client", APIKey: "test-key", AllowedPools: []string{"*"}}},
	}}
}
func poolTestContext(id string) context.Context {
	return pool.WithScope(context.Background(), pool.Scope{ID: id, Provider: "config-inline", Principal: "test-key"})
}
func newPoolTestManager(t *testing.T) (*Manager, *poolTestExecutor) {
	t.Helper()
	m := NewManager(nil, &RoundRobinSelector{}, nil)
	m.SetConfig(poolTestConfig())
	m.SetRetryConfig(0, 0, 0)
	e := &poolTestExecutor{}
	m.RegisterExecutor(e)
	for _, item := range []struct{ id, pool string }{{"pool-a1", "basic"}, {"pool-a2", "basic"}, {"pool-b1", "premium"}, {"pool-unassigned", ""}} {
		a := &Auth{ID: item.id, Provider: e.Identifier(), PoolID: item.pool, Status: StatusActive, Metadata: map[string]any{"pool_id": item.pool}}
		if _, err := m.Register(context.Background(), a); err != nil {
			t.Fatal(err)
		}
		registry.GetGlobalRegistry().RegisterClient(a.ID, e.Identifier(), []*registry.ModelInfo{{ID: "pool-test-model", Object: "model"}})
		t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(a.ID) })
	}
	t.Cleanup(m.StopAutoRefresh)
	return m, e
}

func TestAccountPoolsExecutionIsolation(t *testing.T) {
	for _, mode := range []string{"execute", "count", "stream"} {
		t.Run(mode, func(t *testing.T) {
			m, e := newPoolTestManager(t)
			call := func(ctx context.Context, opts executor.Options) error {
				req := executor.Request{Model: "pool-test-model"}
				switch mode {
				case "execute":
					_, err := m.Execute(ctx, []string{e.Identifier()}, req, opts)
					return err
				case "count":
					_, err := m.ExecuteCount(ctx, []string{e.Identifier()}, req, opts)
					return err
				default:
					result, err := m.ExecuteStream(ctx, []string{e.Identifier()}, req, opts)
					if result != nil {
						for range result.Chunks {
						}
					}
					return err
				}
			}
			for i := 0; i < 3; i++ {
				if err := call(poolTestContext("basic"), executor.Options{}); err != nil {
					t.Fatal(err)
				}
				if err := call(poolTestContext("premium"), executor.Options{}); err != nil {
					t.Fatal(err)
				}
			}
			want := []string{"pool-a1", "pool-b1", "pool-a2", "pool-b1", "pool-a1", "pool-b1"}
			if !reflect.DeepEqual(e.calls, want) {
				t.Fatalf("calls=%v want=%v", e.calls, want)
			}
			before := len(e.calls)
			for _, ctx := range []context.Context{context.Background(), poolTestContext("empty"), poolTestContext("unknown"), pool.WithScope(context.Background(), pool.Scope{ID: "basic", Provider: "config-inline", Principal: "wrong-key"})} {
				if err := call(ctx, executor.Options{}); err == nil {
					t.Fatal("unauthorized or empty pool succeeded")
				}
			}
			if err := call(poolTestContext("basic"), executor.Options{Metadata: map[string]any{executor.PinnedAuthMetadataKey: "pool-b1", "pool_id": "premium"}}); err == nil {
				t.Fatal("foreign pin succeeded")
			}
			if len(e.calls) != before {
				t.Fatalf("denied requests reached executor: %v", e.calls)
			}
		})
	}
}

func TestAccountPoolsRetryDoesNotEscape(t *testing.T) {
	m, e := newPoolTestManager(t)
	e.fail = true
	_, err := m.Execute(poolTestContext("basic"), []string{e.Identifier()}, executor.Request{Model: "pool-test-model"}, executor.Options{})
	if err == nil {
		t.Fatal("expected upstream failure")
	}
	if len(e.calls) != 2 {
		t.Fatalf("expected both pool credentials, got %v", e.calls)
	}
	for _, id := range e.calls {
		if id != "pool-a1" && id != "pool-a2" {
			t.Fatalf("retry escaped: %s", id)
		}
	}
}

func TestAccountPoolsAdmissionRevalidatesPolicyAndMembership(t *testing.T) {
	m, _ := newPoolTestManager(t)
	selected, _ := m.GetByID("pool-a1")
	cfg := poolTestConfig()
	cfg.PoolClients[0].AllowedPools = []string{"premium"}
	m.SetConfig(cfg)
	if release, err := m.admitPool(poolTestContext("basic"), selected); err == nil {
		release()
		t.Fatal("revoked grant admitted")
	}
	m.SetConfig(poolTestConfig())
	moved := selected.Clone()
	moved.Metadata["pool_id"] = "premium"
	moved.PoolID = "premium"
	if _, err := m.Update(context.Background(), moved); err != nil {
		t.Fatal(err)
	}
	if release, err := m.admitPool(poolTestContext("basic"), selected); err == nil {
		release()
		t.Fatal("stale selection admitted")
	}
	release, err := m.admitPool(poolTestContext("premium"), moved)
	if err != nil {
		t.Fatal(err)
	}
	release()
	release()
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.poolActive) != 0 {
		t.Fatal("double release corrupted lease count")
	}
}

func TestAccountPoolsStreamFencesMembershipUntilExecutorCloses(t *testing.T) {
	m, e := newPoolTestManager(t)
	e.chunks = make(chan executor.StreamChunk)
	selected, _ := m.GetByID("pool-a1")
	ctx, cancel := context.WithCancel(poolTestContext("basic"))
	defer cancel()
	stream, err := m.streamInPool(ctx, e, selected, executor.Request{}, executor.Options{})
	if err != nil {
		t.Fatal(err)
	}
	moved := selected.Clone()
	moved.PoolID = "premium"
	moved.Metadata["pool_id"] = "premium"
	if _, err = m.Update(context.Background(), moved); err == nil {
		t.Fatal("active stream moved")
	}
	cancel()
	if _, err = m.Update(context.Background(), moved); err == nil {
		t.Fatal("cancellation released a live executor")
	}
	m.Remove(context.Background(), selected.ID)
	if _, err = m.Register(context.Background(), moved); err == nil {
		t.Fatal("remove/register bypassed active lease")
	}
	close(e.chunks)
	for range stream.Chunks {
	}
	if _, err = m.Register(context.Background(), moved); err != nil {
		t.Fatal(err)
	}
}

func TestAccountPoolsMoveFencesAdmissionsDuringStorage(t *testing.T) {
	m, _ := newPoolTestManager(t)
	entered, finish := make(chan struct{}), make(chan struct{})
	m.SetStore(&callbackAuthStore{save: func(*Auth) error { close(entered); <-finish; return errors.New("disk unavailable") }})
	selected, _ := m.GetByID("pool-a1")
	moved := selected.Clone()
	moved.Metadata["pool_id"] = "premium"
	done := make(chan error, 1)
	go func() { _, err := m.Update(context.Background(), moved); done <- err }()
	<-entered
	if release, err := m.admitPool(poolTestContext("basic"), selected); err == nil {
		release()
		t.Fatal("admitted while membership persistence was in progress")
	}
	close(finish)
	if err := <-done; err == nil {
		t.Fatal("storage failure was ignored")
	}
	current, _ := m.GetByID(selected.ID)
	if current.AccountPoolID() != "basic" {
		t.Fatal("failed move published membership")
	}
	if release, err := m.admitPool(poolTestContext("basic"), current); err == nil {
		release()
		t.Fatal("uncertain storage state was not quarantined")
	}
}

func TestAccountPoolsCannotHotDisableIntoGlobalRouting(t *testing.T) {
	m, _ := newPoolTestManager(t)
	cfg := poolTestConfig()
	cfg.AccountPools.Enabled = false
	m.SetConfig(cfg)
	if err := m.checkPoolScope(context.Background()); err == nil {
		t.Fatal("hot disable opened global access")
	}
	cfg = poolTestConfig()
	cfg.Home.Enabled = true
	m.SetConfig(cfg)
	if err := m.checkPoolScope(poolTestContext("basic")); err == nil {
		t.Fatal("Home accepted unsupported pool scope")
	}
}

func TestAccountPoolsLoadAndFailedRefreshCannotBypassFence(t *testing.T) {
	m, _ := newPoolTestManager(t)
	a, _ := m.GetByID("pool-a1")
	release, err := m.admitPool(poolTestContext("basic"), a)
	if err != nil {
		t.Fatal(err)
	}
	m.SetStore(&callbackAuthStore{save: func(*Auth) error { return errors.New("storage failure") }})
	if err = m.Load(context.Background()); err == nil {
		t.Fatal("Load replaced admitted state")
	}
	release()
	moved := a.Clone()
	moved.Metadata["pool_id"] = "premium"
	if _, err = m.Update(context.Background(), moved); err == nil {
		t.Fatal("failed move succeeded")
	}
	current, _ := m.GetByID(a.ID)
	if _, err = m.UpdateRefreshedAuth(context.Background(), current, current.Clone()); err == nil {
		t.Fatal("failed refresh cleared quarantine")
	}
	if release, err = m.admitPool(poolTestContext("basic"), current); err == nil {
		release()
		t.Fatal("quarantine bypassed")
	}
}

func TestAccountPoolsStrategiesHaveIndependentState(t *testing.T) {
	for _, strategy := range []string{"round-robin", "weighted-round-robin", "fill-first"} {
		t.Run(strategy, func(t *testing.T) {
			sequence := func(interleave bool) []string {
				m, e := newPoolTestManager(t)
				cfg := poolTestConfig()
				cfg.AccountPools.Pools[0].Strategy = strategy
				m.SetConfig(cfg)
				out := []string{}
				for i := 0; i < 8; i++ {
					response, err := m.Execute(poolTestContext("basic"), []string{e.Identifier()}, executor.Request{Model: "pool-test-model"}, executor.Options{})
					if err != nil {
						t.Fatal(err)
					}
					out = append(out, string(response.Payload))
					if interleave {
						for j := 0; j <= i; j++ {
							if _, err = m.Execute(poolTestContext("premium"), []string{e.Identifier()}, executor.Request{Model: "pool-test-model"}, executor.Options{}); err != nil {
								t.Fatal(err)
							}
						}
					}
				}
				return out
			}
			alone, mixed := sequence(false), sequence(true)
			if !reflect.DeepEqual(alone, mixed) {
				t.Fatalf("foreign pool changed strategy: %v vs %v", alone, mixed)
			}
			if strategy == "fill-first" {
				for _, id := range mixed {
					if id != "pool-a1" {
						t.Fatalf("pool override not applied: %v", mixed)
					}
				}
			}
		})
	}
}

func TestAccountPoolsRefreshPreservesMembership(t *testing.T) {
	m, _ := newPoolTestManager(t)
	before, _ := m.GetByID("pool-a1")
	updated := before.Clone()
	updated.PoolID = "premium"
	updated.Metadata["pool_id"] = "premium"
	got, err := m.UpdateRefreshedAuth(context.Background(), before, updated)
	if err != nil {
		t.Fatal(err)
	}
	if got.AccountPoolID() != "basic" {
		t.Fatal("refresh changed pool")
	}
}

func TestAccountPoolsRejectUnboundUpstreamContinuation(t *testing.T) {
	m, e := newPoolTestManager(t)
	for _, payload := range []string{
		`{"previous_response_id":"foreign-response"}`,
		`{"conversation":{"id":"foreign-conversation"}}`,
		`{"thread":{"previous_message_id":"foreign-message"}}`,
		`{"previous_interaction_id":"foreign-interaction"}`,
	} {
		req := executor.Request{Model: "pool-test-model", Payload: []byte(payload)}
		if _, err := m.Execute(poolTestContext("basic"), []string{e.Identifier()}, req, executor.Options{}); err == nil {
			t.Fatal("unbound continuation executed")
		}
		if _, err := m.ExecuteStream(poolTestContext("basic"), []string{e.Identifier()}, req, executor.Options{}); err == nil {
			t.Fatal("stream continuation executed")
		}
	}
	if len(e.calls) != 0 {
		t.Fatalf("unbound continuation reached upstream: %v", e.calls)
	}
	req := executor.Request{Payload: []byte(`{"previous_response_id":null}`)}
	if err := checkPoolRequest(poolTestContext("basic"), req); err != nil {
		t.Fatal(err)
	}
	if err := checkPoolRequest(context.Background(), executor.Request{Payload: []byte(`{"previous_response_id":"legacy"}`)}); err != nil {
		t.Fatal("legacy continuation changed")
	}
}
