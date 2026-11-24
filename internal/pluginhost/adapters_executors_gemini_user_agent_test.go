package pluginhost

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoeForge/moego/json"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

func TestGeminiCLIInferenceUserAgentRPCUsesUALabProfile(t *testing.T) {
	const agyUA = "google-genai-sdk/1.79.1 gl-go/go1.28-20261102-RC01"
	ctx := t.Context()
	host := New()
	host.runtimeConfig = &config.Config{
		GeminiHeaderDefaults: config.GeminiHeaderDefaults{
			UserAgent: agyUA,
			APIClient: agyUA,
		},
	}
	wire := &capturePluginClient{}
	executor := &rpcProviderExecutor{&rpcPluginAdapter{host: host, client: wire}}
	formats := []sdktranslator.Format{sdktranslator.FormatGemini}
	adapter := newCurrentExecutorAdapterForTest(host, "gemini-cli", executor, formats, formats)
	adapter.provider = "gemini-cli"
	auth := &coreauth.Auth{
		Provider:   "gemini-cli",
		Metadata:   map[string]any{"headers": map[string]any{"user-agent": "legacy-meta", "X-Other": "kept"}},
		Attributes: map[string]string{"header: UsEr-AGent ": "stale", "header:X-Another": "also-kept"},
	}
	req := coreexecutor.Request{Model: "gemini-pro", Format: sdktranslator.FormatGemini, Payload: []byte(`{"contents":[]}`)}
	opts := coreexecutor.Options{SourceFormat: sdktranslator.FormatGemini, Headers: http.Header{"User-Agent": {"client-identity"}}}
	if _, err := adapter.Execute(ctx, auth, req, opts); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.CountTokens(ctx, auth, req, opts); err != nil {
		t.Fatal(err)
	}
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := adapter.ExecuteStream(streamCtx, auth, req, opts)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	for range stream.Chunks {
	}
	for _, method := range []string{pluginabi.MethodExecutorExecute, pluginabi.MethodExecutorExecuteStream, pluginabi.MethodExecutorCountTokens} {
		var sent rpcExecutorRequest
		if err := json.Unmarshal(wire.requests[method], &sent); err != nil {
			t.Fatal(err)
		}
		t.Run(method, func(t *testing.T) {
			checkGeminiRPCUserAgent(t, sent.AuthAttributes, sent.AuthMetadata, agyUA)
		})
	}
	for _, path := range []string{"/v1internal:generateContent", "/v1internal:streamGenerateContent", "/v1internal:countTokens", "/v1internal:loadCodeAssist"} {
		raw := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"gemini-pro"}`))
		if path == "/v1internal:countTokens" {
			raw.Method = ""
		}
		raw.Header.Set("User-Agent", "raw-client")
		resp, err := adapter.HttpRequest(ctx, auth, raw)
		if err != nil {
			t.Fatal(err)
		}
		if err := resp.Body.Close(); err != nil {
			t.Fatal(err)
		}
		var sent rpcExecutorHTTPRequest
		if err := json.Unmarshal(wire.requests[pluginabi.MethodExecutorHTTPRequest], &sent); err != nil {
			t.Fatal(err)
		}
		if path == "/v1internal:loadCodeAssist" {
			if got := sent.Metadata["headers"].(map[string]any)["user-agent"]; got != "legacy-meta" {
				t.Errorf("control plane identity changed: %v", got)
			}
		} else {
			t.Run(path, func(t *testing.T) { checkGeminiRPCUserAgent(t, sent.Attributes, sent.Metadata, agyUA) })
		}
	}
	if got := auth.Metadata["headers"].(map[string]any)["user-agent"]; got != "legacy-meta" || auth.Attributes["header: UsEr-AGent "] != "stale" {
		t.Fatal("modified shared credential")
	}
}

func checkGeminiRPCUserAgent(t *testing.T, attrs map[string]string, metadata map[string]any, want string) {
	t.Helper()
	value := ""
	for k, v := range attrs {
		name, ok := strings.CutPrefix(k, "header:")
		if ok && strings.EqualFold(strings.TrimSpace(name), "User-Agent") {
			if v != want {
				t.Errorf("stale credential attribute %q: %q", k, v)
			}
			value = v
		}
	}
	headers := metadata["headers"].(map[string]any)
	for k := range headers {
		if strings.EqualFold(strings.TrimSpace(k), "User-Agent") {
			t.Errorf("legacy credential metadata User-Agent was not stripped: %q", k)
		}
	}
	if value != want || attrs["header:X-Goog-Api-Client"] != want || headers["X-Other"] != "kept" || attrs["header:X-Another"] != "also-kept" {
		t.Fatalf("serialized UA = %q, APIClient = %q, want %q (headers=%v, attributes=%v)", value, attrs["header:X-Goog-Api-Client"], want, headers, attrs)
	}
}

func TestGeminiCLIGlobalUserAgentRPCRejectsAuto(t *testing.T) {
	for _, tc := range []struct{ provider, setting, want string }{
		{"gemini-cli", "", ""},
		{"other-plugin", "fixed-agent", ""},
		{"gemini-cli", "fixed-agent", "fixed-agent"},
		{"gemini-cli", "auto", ""},
	} {
		t.Run(tc.provider+"/"+tc.setting, func(t *testing.T) {
			host := New()
			host.runtimeConfig = &config.Config{GeminiHeaderDefaults: config.GeminiHeaderDefaults{UserAgent: tc.setting}}
			wire := &capturePluginClient{}
			executor := &rpcProviderExecutor{&rpcPluginAdapter{host: host, client: wire}}
			formats := []sdktranslator.Format{sdktranslator.FormatGemini}
			adapter := newCurrentExecutorAdapterForTest(host, "test-plugin", executor, formats, formats)
			adapter.provider = tc.provider
			req := coreexecutor.Request{Model: "gemini-pro(high)", Format: sdktranslator.FormatGemini, Payload: []byte(`{"contents":[]}`)}
			if _, err := adapter.Execute(t.Context(), &coreauth.Auth{Provider: tc.provider}, req, coreexecutor.Options{SourceFormat: sdktranslator.FormatGemini}); err != nil {
				t.Fatal(err)
			}
			var sent rpcExecutorRequest
			if err := json.Unmarshal(wire.requests[pluginabi.MethodExecutorExecute], &sent); err != nil {
				t.Fatal(err)
			}
			if got := sent.AuthAttributes["header:User-Agent"]; got != tc.want {
				t.Fatalf("identity = %q, want %q", got, tc.want)
			}
		})
	}
}
