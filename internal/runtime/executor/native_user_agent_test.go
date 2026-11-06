package executor

import (
	"context"
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
