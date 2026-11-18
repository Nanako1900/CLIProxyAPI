package auth

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

type fableRecordingExecutor struct{ poolTestExecutor }

func (*fableRecordingExecutor) Identifier() string { return "claude" }

const testFable = "claude-fable-5-1"
const testSonnet = "claude-sonnet-5"

func callFableTestModel(ctx context.Context, m *Manager, kind, model string, opts executor.Options) error {
	req := executor.Request{Model: model}
	providers := []string{"claude"}
	switch kind {
	case "stream":
		r, err := m.ExecuteStream(ctx, providers, req, opts)
		if r != nil {
			for range r.Chunks {
			}
		}
		return err
	case "count":
		_, err := m.ExecuteCount(ctx, providers, req, opts)
		return err
	default:
		_, err := m.Execute(ctx, providers, req, opts)
		return err
	}
}

func TestClaudeFableEntitlementRouting(t *testing.T) {
	for _, mode := range []string{"unscoped", "pool"} {
		t.Run(mode, func(t *testing.T) {
			m := NewManager(nil, nil, nil)
			t.Cleanup(m.StopAutoRefresh)
			e := new(fableRecordingExecutor)
			m.RegisterExecutor(e)
			cfg := poolTestConfig()
			cfg.AccountPools.Enabled = mode != "unscoped"
			m.SetConfig(cfg)
			m.SetRetryConfig(0, 0, 1)
			m.SetOAuthModelAlias(map[string][]config.OAuthModelAlias{"claude": {{Name: testFable, Alias: "creative"}}})
			ctx := context.Background()
			if mode != "unscoped" {
				ctx = poolTestContext("basic")
			}
			register := func(id, plan, pool, priority string) *Auth {
				a := &Auth{ID: id, Provider: "claude", Status: StatusActive, Metadata: map[string]any{"access_token": "example", "plan_type": plan, "pool_id": pool}, Attributes: map[string]string{"priority": priority}}
				if _, err := m.Register(ctx, a); err != nil {
					t.Fatal(err)
				}
				reg := registry.GetGlobalRegistry()
				reg.RegisterClient(a.ID, "claude", []*registry.ModelInfo{{ID: testFable}, {ID: testSonnet}, {ID: "creative"}})
				t.Cleanup(func() { reg.UnregisterClient(id) })
				m.ReconcileRegistryModelStates(ctx, id)
				return a
			}
			pro := register("fable-test-pro", "pro", "basic", "20")
			max := register("fable-test-max", "max", "basic", "0")
			if mode != "unscoped" {
				register("fable-test-other-pool", "max", "premium", "100")
			}

			call := func(kind, model string) error {
				return callFableTestModel(ctx, m, kind, model, executor.Options{})
			}
			for _, kind := range []string{"execute", "stream", "count"} {
				if err := call(kind, "creative(high)"); err != nil {
					t.Fatalf("%s: %v", kind, err)
				}
			}
			if err := call("execute", testSonnet); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(e.calls, []string{max.ID, max.ID, max.ID, pro.ID}) {
				t.Fatalf("dispatches: %v", e.calls)
			}
			if !registry.GetGlobalRegistry().IsModelSuspendedForClient(pro.ID, "creative") || !slices.Contains(registry.GetGlobalRegistry().GetModelProviders("creative"), "claude") {
				t.Fatal("expected excluded alias to stay routable but unavailable on Pro")
			}
			until := time.Now().Add(time.Hour)
			current, _ := m.GetByID(max.ID)
			current.ModelStates = map[string]*ModelState{testFable: {Unavailable: true, NextRetryAfter: until, Quota: QuotaState{Exceeded: true, NextRecoverAt: until}}}
			if _, err := m.Update(ctx, current); err != nil {
				t.Fatal(err)
			}
			if err := call("execute", testFable); statusCodeFromError(err) != http.StatusTooManyRequests {
				t.Fatalf("expected real Max cooldown, got %v", err)
			}
			current, _ = m.GetByID(max.ID)
			current.Metadata["plan_type"] = "pro"
			if _, err := m.Update(ctx, current); err != nil {
				t.Fatal(err)
			}
			for _, kind := range []string{"execute", "stream", "count"} {
				err := call(kind, "creative(high)")
				ae, ok := errors.AsType[*Error](err)
				if !ok || ae.Code != ErrorCodePoolModelNotSupported || ae.StatusCode() != 503 || !ae.IsRequestScoped() {
					t.Fatalf("%s: expected local capability error, got %v", kind, err)
				}
			}
			if len(e.calls) != 4 {
				t.Fatalf("unsupported requests reached executor: %v", e.calls)
			}
			saved, _ := m.GetByID(pro.ID)
			if saved.LastError != nil || saved.Quota.Exceeded || saved.ModelStates[testFable] != nil {
				t.Fatalf("skipped Pro acquired failure state")
			}
		})
	}
}

func TestClaudeFableEntitlementMetadata(t *testing.T) {
	m := NewManager(nil, nil, nil)
	for _, tc := range []struct {
		name, provider, key string
		meta                map[string]any
		allowed             bool
	}{
		{"unknown", "claude", "", nil, false},
		{"legacy Pro", "claude", "", map[string]any{"subscriptionType": "Pro", "claude_fable_enabled": true}, false},
		{"Max", "claude", "", map[string]any{"subscription_type": "claude_max"}, true},
		{"explicitly enabled", "claude", "", map[string]any{"claude_fable_enabled": true}, true},
		{"explicitly disabled", "claude", "", map[string]any{"plan_type": "max", "claude_fable_enabled": false}, false},
		{"API key", "claude", "sk-ant-api-example", nil, true},
		{"setup token", "claude", "sk-ant-oat-example", nil, false},
		{"other provider", "vertex", "", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &Auth{Provider: tc.provider, Metadata: tc.meta, Prefix: "tenant", Attributes: map[string]string{"api_key": tc.key}}
			if got := m.ClaudePlanAllowsModel(a, "tenant/"+testFable+"(high)"); got != tc.allowed {
				t.Errorf("allowed=%v, want %v", got, tc.allowed)
			}
			if !m.ClaudePlanAllowsModel(a, "tenant/"+testSonnet) {
				t.Error("non-Fable denied")
			}
		})
	}
}

// Simulate a profile fetched during preparation, after the scheduler picked a Max account.
type fablePrepareExecutor struct {
	fableRecordingExecutor
	downgradeID string
	prepared    int
}

func (e *fablePrepareExecutor) ShouldPrepareRequestAuth(a *Auth) bool { return a.ID == e.downgradeID }
func (e *fablePrepareExecutor) PrepareRequestAuth(_ context.Context, a *Auth) (*Auth, error) {
	e.prepared++
	a.Metadata["plan_type"] = "pro"
	return a, nil
}

func TestClaudeFableEntitlementChangedOnBoundAccount(t *testing.T) {
	for _, kind := range []string{"execute", "count", "stream", "backup-cooling"} {
		t.Run(kind, func(t *testing.T) {
			ctx := poolTestContext("basic")
			m := NewManager(nil, nil, nil)
			t.Cleanup(m.StopAutoRefresh)
			e := new(fablePrepareExecutor)
			m.RegisterExecutor(e)
			cfg := poolTestConfig()
			cfg.Routing.SessionAffinity = true
			cfg.Routing.SessionAffinityRebindOn = SessionAffinityRebindOnNever
			m.SetConfig(cfg)
			m.SetRetryConfig(0, 0, 1)
			reg := registry.GetGlobalRegistry()
			const high = "bound-former-max"
			const backup = "backup-max"
			for id, priority := range map[string]string{high: "1", backup: "0"} {
				a := &Auth{ID: id, Provider: "claude", Status: StatusActive, PoolID: "basic", Attributes: map[string]string{"priority": priority}, Metadata: map[string]any{"plan_type": "max"}}
				if _, err := m.Register(ctx, a); err != nil {
					t.Fatal(err)
				}
				reg.RegisterClient(id, "claude", []*registry.ModelInfo{{ID: testFable}})
				t.Cleanup(func() { reg.UnregisterClient(id) })
			}

			opts := executor.Options{Metadata: map[string]any{executor.DerivedSessionIDMetadataKey: "test-bound-fable"}}
			if err := callFableTestModel(ctx, m, "execute", testFable, opts); err != nil {
				t.Fatal(err)
			}
			if kind == "backup-cooling" {
				until := time.Now().Add(time.Hour)
				a, _ := m.GetByID(backup)
				a.ModelStates = map[string]*ModelState{testFable: {Unavailable: true, NextRetryAfter: until, Quota: QuotaState{Exceeded: true, NextRecoverAt: until}}}
				if _, err := m.Update(ctx, a); err != nil {
					t.Fatal(err)
				}
			}
			e.downgradeID = high
			err := callFableTestModel(ctx, m, kind, testFable, opts)
			wantCalls := []string{high, backup}
			if kind == "backup-cooling" {
				if statusCodeFromError(err) != http.StatusTooManyRequests {
					t.Fatalf("expected real backup cooldown, got %v", err)
				}
				wantCalls = []string{high}
			} else if err != nil {
				t.Fatal(err)
			}
			if e.prepared != 1 || !slices.Equal(e.calls, wantCalls) {
				t.Fatalf("prepared=%d dispatches=%v err=%v", e.prepared, e.calls, err)
			}
			if !reg.IsModelSuspendedForClient(high, testFable) {
				t.Fatal("catalog did not reflect the downgrade")
			}
			saved, _ := m.GetByID(high)
			if saved.LastError != nil || saved.Quota.Exceeded {
				t.Fatal("local skip penalized the account")
			}
		})
	}
}
