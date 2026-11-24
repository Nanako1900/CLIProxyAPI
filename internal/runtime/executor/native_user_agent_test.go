package executor

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

func TestNativeUserAgentReachesWireAfterNativeAndModelRules(t *testing.T) {
	for _, kind := range []string{"codex-http", "codex-compact", "codex-stream", "codex-websocket", "codex-websocket-stream", "codex-image", "codex-image-stream", "claude-http", "claude-stream", "claude-count"} {
		t.Run(kind, func(t *testing.T) {
			headers := make(chan http.Header, 4)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				headers <- r.Header.Clone()
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTeapot)
				_, _ = w.Write([]byte(`{"error":{"message":"identity probe"}}`))
			}))
			defer server.Close()
			cfg := &config.Config{}
			ctx := context.Background()
			auth := &cliproxyauth.Auth{ID: "identity-test", Provider: "codex", ProxyURL: "direct", Attributes: map[string]string{"api_key": "identity-test-key", "base_url": server.URL, "header:User-Agent": "codex-updated/9.9.9"}}
			req := coreexecutor.Request{Model: "gpt-5.6-luna", Payload: []byte(`{"model":"gpt-5.6-luna","input":"hello"}`)}
			opts := coreexecutor.Options{SourceFormat: sdktranslator.FormatOpenAIResponse, Headers: http.Header{"User-Agent": {"client-old"}}}
			var errCall error
			switch kind {
			case "codex-http", "codex-compact":
				if kind == "codex-compact" {
					opts.Alt = "responses/compact"
				}
				_, errCall = NewCodexExecutor(cfg).Execute(ctx, auth, req, opts)
			case "codex-stream":
				opts.Stream = true
				_, errCall = NewCodexExecutor(cfg).ExecuteStream(ctx, auth, req, opts)
			case "codex-websocket":
				_, errCall = NewCodexWebsocketsExecutor(cfg).Execute(ctx, auth, req, opts)
			case "codex-websocket-stream":
				opts.Stream = true
				_, errCall = NewCodexWebsocketsExecutor(cfg).ExecuteStream(ctx, auth, req, opts)
			case "codex-image", "codex-image-stream":
				req = coreexecutor.Request{Model: "gpt-image-1.5", Payload: []byte(`{"model":"gpt-image-1.5","prompt":"test"}`)}
				opts = codexOpenAIImageTestOptions(codexImagesGenerationsPath, kind == "codex-image-stream")
				if opts.Stream {
					_, errCall = NewCodexExecutor(cfg).ExecuteStream(ctx, auth, req, opts)
				} else {
					_, errCall = NewCodexExecutor(cfg).Execute(ctx, auth, req, opts)
				}
			default:
				auth.Provider = "claude"
				auth.Attributes["header:User-Agent"] = "claude-updated/9.9.9"
				req = coreexecutor.Request{Model: "claude-sonnet-4-5", Payload: []byte(`{"model":"claude-sonnet-4-5","max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`)}
				opts.SourceFormat = sdktranslator.FormatClaude
				switch kind {
				case "claude-http":
					_, errCall = NewClaudeExecutor(cfg).Execute(ctx, auth, req, opts)
				case "claude-stream":
					opts.Stream = true
					_, errCall = NewClaudeExecutor(cfg).ExecuteStream(ctx, auth, req, opts)
				case "claude-count":
					// Custom origins use local estimation; exercise the upstream builder directly.
					_, errCall = NewClaudeExecutor(cfg).countTokensUpstream(ctx, auth, req, opts)
				}
			}
			if errCall == nil {
				t.Fatal("expected upstream probe rejection")
			}
			select {
			case got := <-headers:
				if got.Get("User-Agent") != auth.Provider+"-updated/9.9.9" {
					t.Fatalf("final identity = %q / %q", got.Get("User-Agent"), got.Get("Originator"))
				}
				if got.Get("Authorization") != "Bearer identity-test-key" {
					t.Fatal("credential header changed")
				}
				if kind == "codex-websocket" || kind == "codex-websocket-stream" {
					if got.Get("Upgrade") != "websocket" {
						t.Fatal("websocket path was not exercised")
					}
				}
			default:
				t.Fatalf("request did not reach upstream: %v", errCall)
			}
		})
	}
}

func TestClaudeAPIKeyUserAgentKeepsRequestFields(t *testing.T) {
	const incoming = "claude-cli/2.1.321 (external, cli, workload/cron)"
	const fixed = "claude-cli/2.1.353 (external, cli)"
	const merged = "claude-cli/2.1.353 (external, cli, workload/cron)"
	const withDollar = "claude-cli/2.1.353 (external, cli, source/$literal)"
	for _, tc := range []struct {
		name, shared, key, caller, want string
		attrName, mapped, cloakMode     string
		stream, oauth                   bool
	}{
		{name: "plain key passthrough", caller: incoming, want: incoming},
		{name: "shared fixed", shared: fixed, caller: incoming, want: merged},
		{name: "credential fixed", shared: "operator/1", key: fixed, caller: incoming, want: merged},
		{name: "shared auto", shared: "auto", caller: incoming, want: incoming},
		{name: "credential auto", shared: "operator/1", key: "auto", caller: incoming, want: incoming},
		{name: "credential literal", shared: fixed, key: "operator/1", caller: incoming, want: "operator/1"},
		{name: "credential mapping", shared: "operator/1", key: "$X-Custom-UA", mapped: fixed, caller: incoming, want: fixed},
		{name: "case variant mapping", attrName: "HEADER:User-Agent", key: "$X-Custom-UA", mapped: fixed, caller: incoming, want: fixed, stream: true},
		{name: "missing mapping native fallback", key: "$Missing-UA", caller: incoming, want: incoming},
		{name: "missing mapping shared fallback", shared: fixed, key: "$Missing-UA", caller: incoming, want: merged},
		{name: "oversized mapping native fallback", key: "$X-Custom-UA", mapped: strings.Repeat("a", 1025), caller: incoming, want: incoming},
		{name: "oversized mapping shared fallback", shared: fixed, key: "$X-Custom-UA", mapped: strings.Repeat("a", 1025), caller: incoming, want: merged},
		{name: "literal dollar", key: withDollar, caller: incoming, want: "claude-cli/2.1.353 (external, cli, source/$literal, workload/cron)"},
		{name: "credential fixed without caller UA", key: fixed, want: fixed},
		{name: "OAuth protected profile", key: fixed, caller: incoming, oauth: true, want: fixed},
		{name: "privacy mode", key: fixed, caller: incoming, cloakMode: "uncloak", want: fixed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seen := make(chan string, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen <- r.UserAgent()
				if r.Header.Get("Accept") == "text/event-stream" {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"msg_test","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`)
			}))
			defer upstream.Close()
			attrs := map[string]string{"api_key": "local-test-key", "base_url": upstream.URL, "cloak_mode": tc.cloakMode}
			if tc.key != "" {
				name := tc.attrName
				if name == "" {
					name = "header:User-Agent"
				}
				attrs[name] = tc.key
			}
			auth := &cliproxyauth.Auth{ID: t.Name(), Provider: "claude", ProxyURL: "direct", Attributes: attrs}
			if tc.oauth {
				attrs["api_key"] = "sk-ant-oat-local-test"
				auth.Metadata = claudeOAuthTestMetadata()
			}
			cfg := &config.Config{ClaudeHeaderDefaults: config.ClaudeHeaderDefaults{UserAgent: tc.shared}}
			req := coreexecutor.Request{Model: "claude-sonnet-4-6", Payload: []byte(`{"model":"claude-sonnet-4-6","max_tokens":16,"messages":[{"role":"user","content":"test"}]}`)}
			opts := coreexecutor.Options{SourceFormat: sdktranslator.FormatClaude, OriginalRequest: req.Payload, Headers: make(http.Header)}
			if tc.mapped != "" {
				opts.Headers.Set("X-Custom-UA", tc.mapped)
			}
			if tc.caller != "" {
				opts.Headers.Set("User-Agent", tc.caller)
			}
			checkUpstreamUA := func(t *testing.T) {
				t.Helper()
				select {
				case got := <-seen:
					if got != tc.want {
						t.Errorf("upstream User-Agent = %q, want %q", got, tc.want)
					}
				default:
					t.Fatal("request did not reach the upstream")
				}
			}
			e := NewClaudeExecutor(cfg)
			t.Run("messages", func(t *testing.T) {
				if _, err := e.Execute(t.Context(), auth, req, opts); err != nil {
					t.Fatal(err)
				}
				checkUpstreamUA(t)
			})
			if tc.stream {
				t.Run("stream", func(t *testing.T) {
					streamOpts := opts
					streamOpts.Stream = true
					stream, err := e.ExecuteStream(t.Context(), auth, req, streamOpts)
					if err != nil {
						t.Fatal(err)
					}
					for chunk := range stream.Chunks {
						if chunk.Err != nil {
							t.Fatal(chunk.Err)
						}
					}
					checkUpstreamUA(t)
				})
			}
			t.Run("raw", func(t *testing.T) {
				raw, err := http.NewRequestWithContext(t.Context(), http.MethodGet, upstream.URL, nil)
				if err != nil {
					t.Fatal(err)
				}
				raw.Header = opts.Headers.Clone()
				ctx := t.Context()
				if tc.mapped != "" {
					ctx = newClaudeHeaderTestRequest(t, opts.Headers).Context()
				}
				resp, err := e.HttpRequest(ctx, auth, raw)
				if err != nil {
					t.Fatal(err)
				}
				if err := resp.Body.Close(); err != nil {
					t.Fatal(err)
				}
				checkUpstreamUA(t)
			})
		})
	}
}

func TestNativeUserAgentClaudeOAuthProfileOverride(t *testing.T) {
	ctx := context.Background()
	req := httptest.NewRequest(http.MethodPost, "https://api.anthropic.com/v1/messages", nil).WithContext(ctx)
	auth := &cliproxyauth.Auth{ID: "identity-oauth", Metadata: map[string]any{"access_token": "sk-ant-oat01-test"}, Attributes: map[string]string{"header:User-Agent": "claude-updated/9.9.9"}}
	if errHeaders := applyClaudeHeaders(req, auth, "sk-ant-oat01-test", true, nil, []byte(`{"model":"claude-sonnet-4-5","messages":[]}`), &config.Config{}, nil, false, "test-session"); errHeaders != nil {
		t.Fatal(errHeaders)
	}
	if req.Header.Get("User-Agent") != "claude-updated/9.9.9" {
		t.Fatal("OAuth fingerprint overwrote configured identity")
	}
}

func TestNativeUserAgentSDKPrepareResolvesAuto(t *testing.T) {
	for _, provider := range []string{"claude", "codex"} {
		t.Run(provider, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "https://example.com/identity", nil)
			auth := &cliproxyauth.Auth{Provider: provider, Attributes: map[string]string{"api_key": "local-test", "header:User-Agent": "auto"}}
			var errPrepare error
			if provider == "codex" {
				errPrepare = NewCodexExecutor(&config.Config{}).PrepareRequest(req, auth)
			} else {
				errPrepare = NewClaudeExecutor(&config.Config{}).PrepareRequest(req, auth)
			}
			if errPrepare != nil {
				t.Fatal(errPrepare)
			}
			if ua := req.Header.Get("User-Agent"); !strings.HasPrefix(ua, provider+"-") || ua == "auto" {
				t.Fatalf("SDK request identity was not resolved: %q", ua)
			}
		})
	}
}

func TestNativeUserAgentCodexOAuthDefaults(t *testing.T) {
	for _, value := range []string{"operator-codex/9.9.9", "auto"} {
		for _, websocket := range []bool{false, true} {
			t.Run(value+map[bool]string{false: "/http", true: "/websocket"}[websocket], func(t *testing.T) {
				headers := make(chan http.Header, 2)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					headers <- r.Header.Clone()
					w.WriteHeader(http.StatusTeapot)
				}))
				defer server.Close()
				cfg := &config.Config{CodexHeaderDefaults: config.CodexHeaderDefaults{UserAgent: value}}
				auth := &cliproxyauth.Auth{
					ID: "native-ua-oauth", Provider: "codex", ProxyURL: "direct",
					Attributes: map[string]string{"base_url": server.URL},
					Metadata:   map[string]any{"access_token": "local-oauth-token"},
				}
				req := coreexecutor.Request{Model: "gpt-5.6-luna", Payload: []byte(`{"model":"gpt-5.6-luna","input":"hello"}`)}
				opts := coreexecutor.Options{SourceFormat: sdktranslator.FormatOpenAIResponse}
				var errCall error
				if websocket {
					_, errCall = NewCodexWebsocketsExecutor(cfg).Execute(t.Context(), auth, req, opts)
				} else {
					_, errCall = NewCodexExecutor(cfg).Execute(t.Context(), auth, req, opts)
				}
				select {
				case got := <-headers:
					ua := got.Get("User-Agent")
					if value == "auto" {
						if !strings.HasPrefix(ua, "codex-tui/") {
							t.Fatalf("auto did not resolve to a client identity: %q", ua)
						}
					} else if ua != value {
						t.Fatalf("OAuth default = %q, want %q", ua, value)
					}
					if got.Get("Authorization") != "Bearer local-oauth-token" {
						t.Fatal("OAuth credentials changed")
					}
					if websocket && got.Get("Upgrade") != "websocket" {
						t.Fatal("WebSocket handshake not exercised")
					}
				default:
					t.Fatalf("OAuth request did not reach upstream: %v", errCall)
				}
			})
		}
	}
}

func TestGeminiUserAgentNativeUpstream(t *testing.T) {
	for _, provider := range []string{"gemini", "gemini-interactions", "vertex", "vertex-interactions"} {
		for _, action := range []string{"execute", "stream", "count", "raw"} {
			t.Run(provider+"/"+action, func(t *testing.T) {
				cfg, err := config.ParseConfigBytes([]byte("upstream: {gemini: {header-defaults: {user-agent: 'configured/1'}}}"))
				if err != nil {
					t.Fatal(err)
				}
				headers := make(chan http.Header, 1)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					headers <- r.Header.Clone()
					w.WriteHeader(http.StatusTeapot)
				}))
				defer server.Close()
				auth := &cliproxyauth.Auth{ID: "ua-test", Provider: provider, ProxyURL: "direct", Attributes: map[string]string{"api_key": "test", "base_url": server.URL}}
				var e cliproxyauth.ProviderExecutor = NewGeminiExecutor(cfg)
				if strings.HasPrefix(provider, "vertex") {
					auth.Provider = "vertex"
					e = NewGeminiVertexExecutor(cfg)
				}
				req := coreexecutor.Request{Model: "gemini-test", Payload: []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`)}
				opts := coreexecutor.Options{SourceFormat: sdktranslator.FormatGemini}
				if strings.HasSuffix(provider, "-interactions") {
					auth.Attributes["interactions"] = "true"
					req.Payload = []byte(`{"model":"gemini-test","input":"hello"}`)
					opts.SourceFormat = sdktranslator.FormatInteractions
				}
				want := "configured/1"
				switch action {
				case "execute":
					auth.Attributes["header:uSeR-AGENT"] = "auto"
					_, err = e.Execute(t.Context(), auth, req, opts)
				case "stream":
					opts.Stream = true
					auth.Attributes["header:User-Agent"] = "$X-Client-UA"
					opts.Headers = http.Header{"X-Client-Ua": {"mapped/4"}}
					_, err = e.ExecuteStream(t.Context(), auth, req, opts)
				case "count":
					auth.Attributes["header:User-Agent"] = "$Missing"
					_, err = e.CountTokens(t.Context(), auth, req, opts)
				case "raw":
					auth.Attributes["header:User-Agent"] = "auto"
					raw, errNew := http.NewRequest(http.MethodPost, server.URL+"/v1/models/gemini-test:generateContent", nil)
					if errNew != nil {
						t.Fatal(errNew)
					}
					resp, errRaw := e.HttpRequest(t.Context(), auth, raw)
					if errRaw != nil {
						t.Fatal(errRaw)
					}
					resp.Body.Close()
				}
				var got http.Header
				select {
				case got = <-headers:
					if got.Get("X-Goog-Api-Key") != "test" {
						t.Fatal("native credential changed")
					}
				default:
					t.Fatalf("request did not reach native upstream: %v", err)
				}
				if ua := got.Get("User-Agent"); ua != want {
					t.Errorf("User-Agent = %q, want %q", ua, want)
				}
			})
		}
	}
}

func TestGeminiUserAgentRawSDKPreservesOriginal(t *testing.T) {
	for _, tc := range []struct{ provider, path, configured, override string }{
		{"gemini", "/v1/models/gemini-test:generateContent", "", ""},
		{"gemini", "/v1/models/gemini-test:generateContent", "", "bad\nvalue"},
		{"vertex", "/v1/endpoints/openapi/chat/completions", "operator/1", "auto"},
		{"vertex", "/v1/endpoints/openapi/models", "operator/1", "auto"},
	} {
		cfg := &config.Config{GeminiHeaderDefaults: config.GeminiHeaderDefaults{UserAgent: tc.configured}}
		auth := &cliproxyauth.Auth{Provider: tc.provider, Attributes: map[string]string{"api_key": "key", "header:User-Agent": tc.override}}
		req := httptest.NewRequest(http.MethodPost, tc.path, nil)
		req.Header.Set("User-Agent", "existing/4")
		var err error
		if tc.provider == "gemini" {
			err = NewGeminiExecutor(cfg).PrepareRequest(req, auth)
		} else {
			err = NewGeminiVertexExecutor(cfg).PrepareRequest(req, auth)
		}
		if err != nil || req.UserAgent() != "existing/4" {
			t.Errorf("SDK %s %s: got %q, %v", tc.provider, tc.path, req.UserAgent(), err)
		}
	}
}

func TestGeminiUserAgentRawInteractionsBody(t *testing.T) {
	const modelJSON = `{"model":"gemini-2.5-pro","input":"hello"}`
	for _, provider := range []string{"gemini", "vertex"} {
		for _, tc := range []struct {
			name, setting, body, want string
		}{
			{"auto ignored", "auto", modelJSON, "original/1"},
			{"ualab fixed", "google-genai-sdk/1.79.1", modelJSON, "google-genai-sdk/1.79.1"},
			{"unconfigured", "", modelJSON, "original/1"},
		} {
			t.Run(provider+"/"+tc.name, func(t *testing.T) {
				cfg := &config.Config{GeminiHeaderDefaults: config.GeminiHeaderDefaults{UserAgent: tc.setting}}
				auth := &cliproxyauth.Auth{Provider: provider, Attributes: map[string]string{"api_key": "key"}}
				path := "/v1beta/interactions"
				if provider == "vertex" {
					path = "/v1beta1/interactions"
				}
				req, err := http.NewRequest(http.MethodPost, "https://example.invalid"+path, strings.NewReader(tc.body))
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("User-Agent", "original/1")
				length, original := req.ContentLength, req.Body
				if provider == "vertex" {
					err = NewGeminiVertexExecutor(cfg).PrepareRequest(req, auth)
				} else {
					err = NewGeminiInteractionsExecutor(cfg).PrepareRequest(req, auth)
				}
				if err != nil {
					t.Fatal(err)
				}
				if req.UserAgent() != tc.want {
					t.Errorf("got UA %q; want %q", req.UserAgent(), tc.want)
				}
				data, errRead := io.ReadAll(req.Body)
				if errRead != nil || req.Body != original || string(data) != tc.body || req.ContentLength != length {
					t.Error("raw request body or length changed")
				}
			})
		}
	}
}
