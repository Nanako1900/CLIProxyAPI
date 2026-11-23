package helps

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

func TestConfiguredUserAgentPrecedenceAndScope(t *testing.T) {
	cfg := &config.Config{CodexHeaderDefaults: config.CodexHeaderDefaults{UserAgent: "configured-codex/1.0"}, ClaudeHeaderDefaults: config.ClaudeHeaderDefaults{UserAgent: "configured-claude/1.0"}}
	for _, tc := range []struct {
		name, provider, apiKey, override, want string
	}{
		{"codex OAuth", "codex", "", "", "configured-codex/1.0"},
		{"codex API key scope", "codex", "secret", "", "native"},
		{"codex credential", "codex", "secret", "credential/2.0", "credential/2.0"},
		{"claude shared", "claude", "secret", "", "configured-claude/1.0"},
		{"claude credential", "claude", "secret", "credential/2.0", "credential/2.0"},
		{"mapped credential", "codex", "secret", "$X-Test-Identity", "mapped/3.0"},
		{"missing mapped credential", "claude", "secret", "$Missing", "configured-claude/1.0"},
		{"invalid credential", "claude", "secret", "bad\r\nInjected: yes", "configured-claude/1.0"},
		{"other provider", "gemini", "", "", "native"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			headers := http.Header{"User-Agent": {"native"}, "Authorization": {"Bearer secret"}, "Content-Encoding": {"zstd"}, "Originator": {"keep"}}
			auth := &cliproxyauth.Auth{Attributes: map[string]string{"api_key": tc.apiKey, "header:User-Agent": tc.override}}
			ApplyConfiguredUserAgent(context.Background(), headers, cfg, auth, tc.provider, http.Header{"X-Test-Identity": {"mapped/3.0"}})
			if headers.Get("User-Agent") != tc.want {
				t.Fatalf("UA = %q, want %q", headers.Get("User-Agent"), tc.want)
			}
			if headers.Get("Authorization") != "Bearer secret" || headers.Get("Content-Encoding") != "zstd" || headers.Get("Originator") != "keep" || len(headers) != 4 {
				t.Fatal("non-UA headers changed")
			}
		})
	}
	// Clearing the setting keeps the entire native header map, including casing.
	headers := http.Header{"user-agent": {"native"}}
	ApplyConfiguredUserAgent(context.Background(), headers, nil, nil, "claude", nil)
	if headers["user-agent"][0] != "native" || len(headers) != 1 {
		t.Fatal("empty override changed native identity")
	}
}

func TestConfiguredClaudeUserAgentKeepsClientRequestFields(t *testing.T) {
	for _, tc := range []struct{ name, configured, current, want string }{
		{"request field", "claude-cli/2.1.353 (external, cli)", "claude-cli/2.1.300 (external, cli, workload/cron)", "claude-cli/2.1.353 (external, cli, workload/cron)"},
		{"client order kept", "claude-cli/2.1.353 (external, cli)", "claude-cli/2.1.353 (external, cli, future-feature/value, agent-sdk/0.2.133)", "claude-cli/2.1.353 (external, cli, future-feature/value, agent-sdk/0.2.133)"},
		{"configured field wins", "claude-cli/2.1.353 (external, cli, agent-sdk/0.3.0)", "claude-cli/2.1.353 (external, cli, agent-sdk/0.2.133, Workload/cron)", "claude-cli/2.1.353 (external, cli, agent-sdk/0.3.0, Workload/cron)"},
		{"configured entrypoint wins", "claude-cli/2.1.353 (external, claude-vscode)", "claude-cli/2.1.353 (external, cli, request-phase/background)", "claude-cli/2.1.353 (external, claude-vscode, request-phase/background)"},
		{"no client fields", "claude-cli/2.1.353 (external, cli)", "claude-cli/2.1.300 (external, cli)", "claude-cli/2.1.353 (external, cli)"},
		{"non Claude Code configured", "custom-client/1.0", "claude-cli/2.1.300 (external, cli, workload/cron)", "custom-client/1.0"},
		{"non Claude Code client", "claude-cli/2.1.353 (external, cli)", "Mozilla/5.0 (X11; Linux)", "claude-cli/2.1.353 (external, cli)"},
		{"invalid client field", "claude-cli/2.1.353 (external, cli)", "claude-cli/2.1.300 (external, cli, workload)", "claude-cli/2.1.353 (external, cli)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := carryClaudeRequestFields(tc.configured, tc.current); got != tc.want {
				t.Fatalf("UA = %q, want %q", got, tc.want)
			}
		})
	}
	cfg := &config.Config{ClaudeHeaderDefaults: config.ClaudeHeaderDefaults{UserAgent: "claude-cli/2.1.353 (external, cli)"}}
	for _, provider := range []string{"claude", "codex"} {
		headers := http.Header{"User-Agent": {"claude-cli/2.1.300 (external, cli, workload/cron)"}}
		ApplyConfiguredUserAgent(context.Background(), headers, cfg, &cliproxyauth.Auth{}, provider, nil)
		want := "claude-cli/2.1.353 (external, cli, workload/cron)"
		if provider == "codex" {
			want = "claude-cli/2.1.300 (external, cli, workload/cron)"
		}
		if got := headers.Get("User-Agent"); got != want {
			t.Fatalf("%s UA = %q, want %q", provider, got, want)
		}
	}
}

func TestAutomaticUserAgentPreservesIdentityAndNeverDowngrades(t *testing.T) {
	for _, tc := range []struct{ provider, baseline, version, want string }{
		{"codex", "codex-tui/0.154.0 (Linux; aarch64) terminal/2 (codex-tui; 0.154.0)", "0.167.0", "codex-tui/0.167.0 (Linux; aarch64) terminal/2 (codex-tui; 0.167.0)"},
		{"claude", "claude-cli/2.1.280 (external, claude-vscode)", "2.1.319", "claude-cli/2.1.319 (external, claude-vscode)"},
		{"claude", "claude-cli/3.0.0 (external, cli)", "2.1.319", "claude-cli/3.0.0 (external, cli)"},
		{"codex", "auto", "0.154.0", DefaultCodexUserAgent},
		{"codex", "codex-tui/0.154.0-beta", "0.154.0", DefaultCodexUserAgent},
	} {
		if got := replaceClientUserAgentVersion(tc.provider, tc.baseline, tc.version); got != tc.want {
			t.Fatalf("version replacement = %q, want %q", got, tc.want)
		}
	}
	for _, bad := range []string{"1.2", "1.2.3-beta", "01.2.3", "1.2.3\r\n", "9999999999999999.2.3", "+1.2.3"} {
		if _, valid := parseClientVersion(bad); valid {
			t.Fatalf("accepted invalid version %q", bad)
		}
	}
}

func TestAutomaticUserAgentDoesNotChangeMeasuredClaudeProfile(t *testing.T) {
	baseline := defaultClaudeDeviceProfile(&config.Config{})
	auto := defaultClaudeDeviceProfile(&config.Config{ClaudeHeaderDefaults: config.ClaudeHeaderDefaults{UserAgent: "auto"}})
	if auto != baseline {
		t.Fatal("automatic UA changed the measured software baseline")
	}
}

type userAgentRegistryTransport func(*http.Request) (*http.Response, error)

func (f userAgentRegistryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestUserAgentRegistryFailureIsolationAndOptIn(t *testing.T) {
	cache := &clientVersionCache{versions: map[string]string{"codex": "0.154.0", "claude": "2.1.280"}}
	var calls atomic.Int32
	client := &http.Client{Transport: userAgentRegistryTransport(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		if req.URL.Host != "registry.npmjs.org" || req.Header.Get("Authorization") != "" || req.Header.Get("Cookie") != "" {
			t.Error("lookup destination or credential isolation failed")
		}
		status, body := http.StatusOK, `{"version":"0.167.0"}`
		if strings.Contains(req.URL.Path, "anthropic") {
			status, body = http.StatusServiceUnavailable, "unavailable"
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	refreshClientUserAgents(context.Background(), clientUserAgentSettings{}, client, cache)
	if calls.Load() != 0 {
		t.Fatal("disabled auto mode made a network request")
	}
	refreshClientUserAgents(context.Background(), clientUserAgentSettings{Codex: true, Claude: true}, client, cache)
	if cache.get("codex") != "0.167.0" || cache.get("claude") != "2.1.280" || calls.Load() != 2 {
		t.Fatal("failed lookup overwrote cache or blocked the other provider")
	}
	if cache.advance("codex", "0.100.0") || cache.advance("claude", "2.2.0-beta") {
		t.Fatal("cache accepted downgrade or prerelease")
	}
	client.Transport = userAgentRegistryTransport(func(*http.Request) (*http.Response, error) { return nil, fmt.Errorf("offline") })
	refreshClientUserAgents(context.Background(), clientUserAgentSettings{Codex: true}, client, cache)
	if cache.get("codex") != "0.167.0" {
		t.Fatal("offline refresh discarded cached version")
	}
}

func TestUserAgentUpdaterReadsNativeSettings(t *testing.T) {
	cfg := &config.Config{ClaudeHeaderDefaults: config.ClaudeHeaderDefaults{UserAgent: " AUTO "}, CodexKey: []config.CodexKey{{Headers: map[string]string{"user-agent": "auto"}}}}
	settings := userAgentSettings(cfg)
	if !settings.Claude || !settings.Codex {
		t.Fatal("native auto settings were not recognized")
	}
	cfg.Home.Enabled = true
	if settings = userAgentSettings(cfg); settings.Claude || settings.Codex {
		t.Fatal("Home must not start standalone auto lookups")
	}
}

func TestUserAgentUpdaterConfigChangeCancelsLookupAndShutdown(t *testing.T) {
	var current atomic.Pointer[config.Config]
	current.Store(&config.Config{})
	updates := make(chan struct{})
	ticks := make(chan time.Time)
	type startedLookup struct {
		ctx      context.Context
		settings clientUserAgentSettings
	}
	started := make(chan startedLookup, 2)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		runClientUserAgentUpdater(ctx, current.Load, updates, ticks, func(ctx context.Context, settings clientUserAgentSettings) {
			started <- startedLookup{ctx, settings}
			<-ctx.Done()
		})
	}()
	// Starting disabled and an unrelated save must not contact the registry.
	updates <- struct{}{}
	select {
	case <-started:
		t.Fatal("disabled updater started a lookup")
	default:
	}
	current.Store(&config.Config{CodexHeaderDefaults: config.CodexHeaderDefaults{UserAgent: "auto"}})
	updates <- struct{}{}
	first := <-started
	if !first.settings.Codex || first.settings.Claude {
		t.Fatal("incorrect provider enabled by config reload")
	}
	updates <- struct{}{}
	ticks <- time.Time{}
	select {
	case <-started:
		t.Fatal("unrelated save or timer overlapped an active lookup")
	default:
	}
	current.Store(&config.Config{ClaudeHeaderDefaults: config.ClaudeHeaderDefaults{UserAgent: "auto"}})
	updates <- struct{}{}
	second := <-started
	<-first.ctx.Done()
	if !second.settings.Claude || second.settings.Codex {
		t.Fatal("obsolete provider settings survived config reload")
	}
	cancel()
	<-stopped
	<-second.ctx.Done()
}

func TestGeminiUserAgentCredentialAndModel(t *testing.T) {
	cfg := &config.Config{GeminiHeaderDefaults: config.GeminiHeaderDefaults{UserAgent: "operator/1"}}
	attrs := map[string]string{"header:uSeR-agEnt": "attr/1"}
	auth := &cliproxyauth.Auth{Provider: "gemini-cli", Attributes: attrs}
	for _, metadata := range []any{map[string]any{"USER-agent": "$X-Client-UA"}, map[string]string{"USER-agent": "$X-Client-UA"}} {
		auth.Metadata = map[string]any{"headers": metadata}
		got := ResolveGeminiUserAgent(t.Context(), cfg, auth, "", http.Header{"x-client-ua": {"downstream/1"}})
		if got != "downstream/1" {
			t.Fatalf("plugin file header = %q", got)
		}
		got = ResolveGeminiUserAgent(t.Context(), cfg, auth, "", http.Header{"x-client-ua": {"bad\r\nheader"}})
		if got != "operator/1" {
			t.Fatalf("invalid variable value reached the request: %q", got)
		}
	}
	auth.Provider = "vertex"
	if got := ResolveGeminiUserAgent(t.Context(), cfg, auth, "", nil); got != "attr/1" {
		t.Fatalf("native credential header = %q", got)
	}
	for _, model := range []string{"", "bad\r\nmodel", "ä", strings.Repeat("a", 1024)} {
		cfg.GeminiHeaderDefaults.UserAgent = "auto"
		if got := ResolveGeminiUserAgent(t.Context(), cfg, nil, model, nil); !strings.Contains(got, "/unknown (") || !config.ValidUserAgent(got) {
			t.Fatalf("malformed model generated %q", got)
		}
	}
}

func TestGeminiUserAgentCredentialUpdateOptIn(t *testing.T) {
	headers := map[string]string{"user-AGENT": " AuTo "}
	for _, cfg := range []*config.Config{
		{GeminiKey: []config.GeminiKey{{Headers: headers}}},
		{InteractionsKey: []config.GeminiKey{{Headers: headers}}},
		{VertexCompatAPIKey: []config.VertexCompatKey{{Headers: headers}}},
	} {
		settings := userAgentSettings(cfg)
		if !settings.Gemini || settings.Claude || settings.Codex {
			t.Fatalf("incorrect credential settings: %+v", settings)
		}
		cfg.Home.Enabled = true
		if userAgentSettings(cfg).Gemini {
			t.Fatal("Home unexpectedly enabled a background Gemini lookup")
		}
	}
	cache := &clientVersionCache{versions: map[string]string{"gemini": "0.34.0"}}
	for _, version := range []string{"0.51.1", "0.53.0-preview.2", "0.30.0"} {
		client := &http.Client{Transport: userAgentRegistryTransport(func(req *http.Request) (*http.Response, error) {
			if req.URL.String() != "https://registry.npmjs.org/@google/gemini-cli/latest" {
				t.Errorf("unexpected registry request: %s", req.URL)
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"version":"` + version + `"}`))}, nil
		})}
		refreshClientUserAgents(t.Context(), clientUserAgentSettings{Gemini: true}, client, cache)
		if got := cache.get("gemini"); got != "0.51.1" {
			t.Fatalf("cached version = %q after %s", got, version)
		}
	}
}

func TestGeminiUserAgentUsesActualModel(t *testing.T) {
	cfg := &config.Config{GeminiHeaderDefaults: config.GeminiHeaderDefaults{UserAgent: "auto"}}
	for _, tc := range []struct{ model, want string }{
		{"gemini-test(high)", "gemini-test(high)"},
		{"  gateway model(high) ", "gateway model(high)"},
		{"\tmodel", "unknown"},
		{"model\n", "unknown"},
	} {
		if got := ResolveGeminiUserAgent(t.Context(), cfg, nil, tc.model, nil); !strings.Contains(got, "/"+tc.want+" (") {
			t.Errorf("model %q: incorrect identity %q", tc.model, got)
		}
	}
}

func TestGeminiUserAgentVertexRouteUpdateEligibility(t *testing.T) {
	yes, no := true, false
	cases := []struct {
		name string
		key  config.VertexCompatKey
		want bool
	}{
		{"explicit OpenAI only", config.VertexCompatKey{OpenAPI: &yes, Models: []config.VertexCompatModel{{Name: "gemini-test"}}}, false},
		{"OpenAI partner model", config.VertexCompatKey{Models: []config.VertexCompatModel{{Name: "partner/model"}}}, false},
		{"native model override", config.VertexCompatKey{OpenAPI: &yes, Models: []config.VertexCompatModel{{Name: "gemini-test", OpenAPI: &no}}}, true},
		{"Imagen always native", config.VertexCompatKey{OpenAPI: &yes, Models: []config.VertexCompatModel{{Name: "imagen-test"}}}, true},
		{"native Interactions", config.VertexCompatKey{OpenAPI: &yes, Interactions: &yes, Models: []config.VertexCompatModel{{Name: "gemini-test"}}}, true},
		{"mutable model catalog", config.VertexCompatKey{OpenAPI: &yes}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.key.Headers = map[string]string{"User-Agent": "auto"}
			cfg := &config.Config{VertexCompatAPIKey: []config.VertexCompatKey{tc.key}}
			if got := userAgentSettings(cfg).Gemini; got != tc.want {
				t.Errorf("refresh enabled = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestGeminiUserAgentCredentialDuplicateSpellings(t *testing.T) {
	for _, tc := range []struct {
		auth *cliproxyauth.Auth
		want string
	}{
		{&cliproxyauth.Auth{Attributes: map[string]string{"header:User-Agent": "canonical/1", "header:USER-AGENT": "alternate/1", "header:user-agent": "other/1"}}, "canonical/1"},
		{&cliproxyauth.Auth{Attributes: map[string]string{"header:USER-AGENT": "alternate/1", "header:user-agent": "other/1"}}, "alternate/1"},
		{&cliproxyauth.Auth{Provider: "gemini-cli", Metadata: map[string]any{"headers": map[string]any{"User-Agent": "file-canonical/1", "USER-AGENT": "file-alternate/1", "user-agent": "other/1"}}}, "file-canonical/1"},
		{&cliproxyauth.Auth{Provider: "gemini-cli", Metadata: map[string]any{"headers": map[string]string{"USER-AGENT": "file-alternate/1", "user-agent": "other/1"}}}, "file-alternate/1"},
	} {
		for range 30 {
			if got := GeminiCredentialUserAgent(tc.auth); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
				break
			}
		}
	}
}
