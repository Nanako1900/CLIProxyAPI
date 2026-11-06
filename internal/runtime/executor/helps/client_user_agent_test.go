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
