package helps

import (
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
)

func TestClientUserAgentLookupStatusPreservesSuccessAndIgnoresObsoleteCompletion(t *testing.T) {
	cache := &clientVersionCache{versions: map[string]string{"claude": "2.1.280"}}
	first := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	second := first.Add(time.Hour)
	cache.recordLookup("claude", "checking", first, first)
	cache.recordLookup("claude", "ok", first, first.Add(time.Second))
	cache.recordLookup("claude", "checking", second, second)
	cache.recordLookup("claude", "failed", second, second.Add(time.Second))
	cache.recordLookup("claude", "cancelled", first, second.Add(2*time.Second))
	state := cache.lookups["claude"]
	if state.Result != "failed" || !state.LastAttempt.Equal(second) || !state.LastSuccess.Equal(first.Add(time.Second)) {
		t.Fatalf("incorrect lookup history: %+v", state)
	}
	if cache.get("claude") != "2.1.280" {
		t.Fatal("status observation changed the cached version")
	}
}

func TestClientUserAgentStatusesReflectConfiguredBaselineWithoutChangingIt(t *testing.T) {
	cfg := &config.Config{ClaudeHeaderDefaults: config.ClaudeHeaderDefaults{
		UserAgent: "auto", PackageVersion: "0.123.0", RuntimeVersion: "v26.5.0", OS: "Linux", Arch: "x64", Timeout: "300",
	}}
	statuses := ClientUserAgentStatuses(cfg)
	if len(statuses) != 2 || statuses[0].Provider != "claude" || statuses[1].Provider != "codex" {
		t.Fatalf("unexpected providers: %+v", statuses)
	}
	claude := statuses[0]
	if !claude.Auto.Enabled || claude.Mode != "auto" || claude.BillingVersion != DefaultClaudeVersion(cfg) {
		t.Fatalf("unexpected Claude status: %+v", claude)
	}
	if claude.BaselineHeaders["X-Stainless-Package-Version"] != "0.123.0" || claude.BaselineHeaders["X-Stainless-OS"] != "Linux" || claude.BaselineHeaders["X-Stainless-Timeout"] != "300" {
		t.Fatal("status does not reflect configured baseline")
	}
	claude.BaselineHeaders["X-Stainless-OS"] = "mutated"
	if ClientUserAgentStatuses(cfg)[0].BaselineHeaders["X-Stainless-OS"] != "Linux" {
		t.Fatal("status response aliases runtime configuration")
	}
	cfg.Home.Enabled = true
	if ClientUserAgentStatuses(cfg)[0].Auto.Enabled {
		t.Fatal("Home mode incorrectly reports automatic queries enabled")
	}
}
