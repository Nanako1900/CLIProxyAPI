package cliproxy

import (
	"context"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	executor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/pool"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
)

func TestAccountPoolsProductionBuilderWithoutScheduler(t *testing.T) {
	enabled := true
	cfg := &config.Config{AuthDir: t.TempDir(), SDKConfig: config.SDKConfig{
		APIKeys: []string{"pool-builder-key"}, LegacyKeyPools: []string{"basic"},
		AccountPools: config.AccountPoolsConfig{Enabled: true, Pools: []config.AccountPool{{ID: "basic", Name: "Basic", Enabled: &enabled}}},
	}}
	service, err := NewBuilder().WithConfig(cfg).WithConfigPath(t.TempDir() + "/config.yaml").Build()
	if err != nil {
		t.Fatal(err)
	}
	m := service.coreManager
	t.Cleanup(m.StopAutoRefresh)
	if service.pluginHost == nil || service.pluginHost.HasScheduler() {
		t.Fatal("expected the default host without a scheduler capability")
	}
	e := serviceTestSDKExecutor{}
	m.RegisterExecutor(e)
	for _, member := range []struct{ id, pool string }{{"builder-pool-account", "basic"}, {"builder-unassigned", ""}} {
		_, errRegister := m.Register(context.Background(), &coreauth.Auth{ID: member.id, Provider: e.Identifier(), PoolID: member.pool, Status: coreauth.StatusActive})
		if errRegister != nil {
			t.Fatal(errRegister)
		}
		registry.GetGlobalRegistry().RegisterClient(member.id, e.Identifier(), []*registry.ModelInfo{{ID: "builder-pool-model"}})
		t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(member.id) })
	}
	ctx := pool.WithScope(context.Background(), pool.Scope{ID: "basic", Provider: "config-inline", Principal: "pool-builder-key"})
	selected, err := m.SelectAuth(ctx, e.Identifier(), "builder-pool-model", executor.Options{})
	if err != nil || selected == nil || selected.ID != "builder-pool-account" {
		t.Fatalf("builder selection: auth=%v err=%v", selected, err)
	}
	if _, errExecute := m.Execute(ctx, []string{e.Identifier()}, executor.Request{Model: "builder-pool-model"}, executor.Options{}); errExecute != nil {
		t.Fatalf("builder execution: %v", errExecute)
	}
}
