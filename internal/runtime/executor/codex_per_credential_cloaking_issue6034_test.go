package executor

import (
	"context"
	"net/http"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

func boolPtr(b bool) *bool {
	return &b
}

func TestCodexV8HistoricalCloakingAliasAffectsBothAuthKinds(t *testing.T) {
	for _, tc := range []struct {
		name, settings, keyOption string
		wantAPI                   bool
	}{
		{"legacy global", "codex: {disable-codex-cloaking: true}\n", "", true},
		{"historical alias", "oauth: {providers: {codex: {disable-codex-cloaking: true}}}\n", "", true},
		{"explicit key override", "oauth: {providers: {codex: {disable-codex-cloaking: true}}}\n", ", disable-codex-cloaking: false", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := tc.settings + "api-keys: {codex: [{name: independent, base-url: 'https://example.invalid/v1', keys: [{api-key: test-key" + tc.keyOption + "}]}]}\n"
			cfg, err := config.ParseConfigBytes([]byte(raw))
			if err != nil {
				t.Fatal(err)
			}
			oauth := &cliproxyauth.Auth{Provider: "codex", Metadata: map[string]any{"access_token": "test-oauth"}}
			if !isCodexCloakingDisabled(cfg, oauth) {
				t.Fatal("OAuth setting was not applied")
			}
			key := &cliproxyauth.Auth{Provider: "codex", Attributes: map[string]string{"api_key": "test-key", "base_url": "https://example.invalid/v1"}}
			if isCodexCloakingDisabled(cfg, key) != tc.wantAPI {
				t.Fatal("wrong API-key cloaking policy")
			}
			if !cfg.Codex.DisableCodexCloaking {
				t.Fatal("shared configuration was mutated")
			}
		})
	}
}

func TestCodexIdentityStrictlyUsesUALabAndIgnoresLegacyCredentialOverrides(t *testing.T) {
	const ualabUA = "codex-tui/0.172.0 (Mac OS 27.3.0; arm64) Apple_Terminal/489.11 (codex-tui; 0.172.0)"
	cfg := &config.Config{
		Codex: config.CodexConfig{
			DisableCodexCloaking: true,
		},
		CodexHeaderDefaults: config.CodexHeaderDefaults{
			UserAgent:      ualabUA,
			Originator:     "codex-tui",
			BetaFeatures:   "remote_compaction_v2",
			AcceptEncoding: "zstd,gzip",
		},
	}

	auth := &cliproxyauth.Auth{
		Provider: "codex",
		Attributes: map[string]string{
			"api_key":                "custom-key",
			"base_url":               "https://example.com/v1",
			"header:User-Agent":      "CustomAgent/1.0",
			"header:Originator":      "CustomOriginator",
			"header:X-Keep-Custom":   "keep-me",
			"codex_disable_cloaking": "true",
		},
	}

	req, err := http.NewRequest(http.MethodPost, "https://example.com/v1/responses", nil)
	if err != nil {
		t.Fatalf("http.NewRequest failed: %v", err)
	}

	applyCodexHeadersFromSources(req, auth, "test-token", false, cfg, nil)

	if got := req.Header.Get("User-Agent"); got != ualabUA {
		t.Errorf("HTTP User-Agent = %q, want %q", got, ualabUA)
	}
	if got := req.Header.Get("Originator"); got != "codex-tui" {
		t.Errorf("HTTP Originator = %q, want codex-tui", got)
	}
	if got := req.Header.Get("X-Codex-Beta-Features"); got != "remote_compaction_v2" {
		t.Errorf("HTTP X-Codex-Beta-Features = %q, want remote_compaction_v2", got)
	}
	if got := req.Header.Get("Accept-Encoding"); got != "zstd,gzip" {
		t.Errorf("HTTP Accept-Encoding = %q, want zstd,gzip", got)
	}
	if got := req.Header.Get("X-Keep-Custom"); got != "keep-me" {
		t.Errorf("non-identity custom header lost: %q", got)
	}

	wsHeaders := applyCodexWebsocketHeaders(context.Background(), nil, auth, "test-token", cfg, true)
	if got := wsHeaders.Get("User-Agent"); got != ualabUA {
		t.Errorf("WebSocket User-Agent = %q, want %q", got, ualabUA)
	}
	if got := wsHeaders.Get("Originator"); got != "codex-tui" {
		t.Errorf("WebSocket Originator = %q, want codex-tui", got)
	}
	if got := wsHeaders.Get("X-Keep-Custom"); got != "keep-me" {
		t.Errorf("WebSocket non-identity custom header lost: %q", got)
	}

	// Unconfigured Codex does not inject any built-in default UA or Originator.
	reqEmpty, err := http.NewRequest(http.MethodPost, "https://example.com/v1/responses", nil)
	if err != nil {
		t.Fatalf("http.NewRequest failed: %v", err)
	}
	applyCodexHeadersFromSources(reqEmpty, auth, "test-token", false, &config.Config{}, nil)
	if got := reqEmpty.Header.Get("User-Agent"); got != "" {
		t.Errorf("unconfigured HTTP User-Agent = %q, want empty", got)
	}
	if got := reqEmpty.Header.Get("Originator"); got != "" {
		t.Errorf("unconfigured HTTP Originator = %q, want empty", got)
	}
}
