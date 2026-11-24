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
	if len(statuses) != 3 || statuses[0].Provider != "claude" || statuses[1].Provider != "codex" {
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
	if !claude.ForceHTTP1 || len(claude.HeaderOrder) != len(claudeCodeMessagesHeaderOrder) || claude.HeaderOrder[0] != "Accept" {
		t.Fatalf("expected Claude status to expose wire header order and forceHttp1, got forceHttp1=%v order=%v", claude.ForceHTTP1, claude.HeaderOrder)
	}
	claude.HeaderOrder[0] = "mutated"
	if ClientUserAgentStatuses(cfg)[0].HeaderOrder[0] != "Accept" {
		t.Fatal("status response aliases claudeCodeMessagesHeaderOrder")
	}
	cfg.Home.Enabled = true
	if ClientUserAgentStatuses(cfg)[0].Auto.Enabled {
		t.Fatal("Home mode incorrectly reports automatic queries enabled")
	}
}

func TestGeminiUserAgentStatus(t *testing.T) {
	const agyUA = "google-genai-sdk/1.79.1 gl-go/go1.28-20261102-RC01"
	for _, tc := range []struct {
		yaml     string
		wantMode string
		wantUA   string
	}{
		{yaml: "upstream: {gemini: {header-defaults: {user-agent: ''}}}", wantMode: "unconfigured", wantUA: ""},
		{yaml: "upstream: {gemini: {header-defaults: {user-agent: '" + agyUA + "', api-client: '" + agyUA + "'}}}", wantMode: "ualab", wantUA: agyUA},
	} {
		cfg, err := config.ParseConfigBytes([]byte(tc.yaml))
		if err != nil {
			t.Fatal(err)
		}
		statuses := ClientUserAgentStatuses(cfg)
		if len(statuses) != 3 || statuses[2].Provider != "gemini" {
			t.Fatal("missing Gemini status")
		}
		gemini := statuses[2]
		if gemini.Auto.Enabled || gemini.Auto.RegistryURL != "" {
			t.Fatalf("Gemini auto must be disabled: %+v", gemini)
		}
		if gemini.Mode != tc.wantMode || gemini.ResolvedUserAgent != tc.wantUA {
			t.Fatalf("unexpected Gemini status: %+v", gemini)
		}
	}
}

func TestCodexUserAgentStatus(t *testing.T) {
	const codexUA = "codex-tui/0.172.0 (Mac OS 27.3.0; arm64) Apple_Terminal/489.11 (codex-tui; 0.172.0)"
	for _, tc := range []struct {
		yaml     string
		wantMode string
		wantUA   string
	}{
		{yaml: "oauth: {providers: {codex: {header-defaults: {user-agent: ''}}}}", wantMode: "unconfigured", wantUA: ""},
		{yaml: "oauth: {providers: {codex: {header-defaults: {user-agent: '" + codexUA + "', originator: 'codex-tui', beta-features: 'remote_compaction_v2', accept-encoding: 'zstd,gzip'}}}}", wantMode: "ualab", wantUA: codexUA},
	} {
		cfg, err := config.ParseConfigBytes([]byte(tc.yaml))
		if err != nil {
			t.Fatal(err)
		}
		statuses := ClientUserAgentStatuses(cfg)
		if len(statuses) != 3 || statuses[1].Provider != "codex" {
			t.Fatal("missing Codex status")
		}
		codex := statuses[1]
		if codex.Auto.Enabled || codex.Auto.RegistryURL != "" || codex.DefaultUserAgent != "" {
			t.Fatalf("Codex auto/default must be disabled: %+v", codex)
		}
		if codex.Mode != tc.wantMode || codex.ResolvedUserAgent != tc.wantUA {
			t.Fatalf("unexpected Codex status: %+v", codex)
		}
	}
}
