package auth

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
)

type codexDeviceRoundTripFunc func(*http.Request) (*http.Response, error)

func (f codexDeviceRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestCodexDeviceUserAgentAndHeaderEnforcement(t *testing.T) {
	t.Parallel()

	const customUA = "codex-tui/0.172.0 (Mac OS 27.3.0; arm64) Apple_Terminal/489.11 (codex-tui; 0.172.0)"

	if got := codexDeviceUserAgent(&config.Config{CodexHeaderDefaults: config.CodexHeaderDefaults{UserAgent: customUA}}); got != customUA {
		t.Fatalf("codexDeviceUserAgent = %q, want %q", got, customUA)
	}
	if got := codexDeviceUserAgent(&config.Config{CodexHeaderDefaults: config.CodexHeaderDefaults{UserAgent: "auto"}}); got != "" {
		t.Fatalf("codexDeviceUserAgent(auto) = %q, want empty", got)
	}

	// Verify configured UA is sent on usercode request.
	var gotUA string
	client := &http.Client{
		Transport: codexDeviceRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			gotUA = req.Header.Get("User-Agent")
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"device_auth_id":"id-1","user_code":"CODE","interval":"5"}`)),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	}
	if _, err := requestCodexDeviceUserCode(context.Background(), client, customUA); err != nil {
		t.Fatalf("requestCodexDeviceUserCode failed: %v", err)
	}
	if gotUA != customUA {
		t.Fatalf("request User-Agent = %q, want %q", gotUA, customUA)
	}

	// Verify empty UA explicitly suppresses Go-http-client.
	var gotHeader []string
	var hasKey bool
	clientEmpty := &http.Client{
		Transport: codexDeviceRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			gotHeader, hasKey = req.Header["User-Agent"]
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"device_auth_id":"id-1","user_code":"CODE","interval":"5"}`)),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	}
	if _, err := requestCodexDeviceUserCode(context.Background(), clientEmpty, ""); err != nil {
		t.Fatalf("requestCodexDeviceUserCode(empty) failed: %v", err)
	}
	if !hasKey || len(gotHeader) != 1 || gotHeader[0] != "" {
		t.Fatalf("empty UA header = %#v (present=%v), want explicit empty string", gotHeader, hasKey)
	}
}

func TestCodexDeviceFlowInvalidProxyFailsClosed(t *testing.T) {
	t.Parallel()

	authenticator := NewCodexAuthenticator()
	_, err := authenticator.loginWithDeviceFlow(context.Background(), &config.Config{
		SDKConfig: sdkconfig.SDKConfig{ProxyURL: "invalid-scheme://127.0.0.1:9999"},
	}, &LoginOptions{NoBrowser: true})
	if err == nil {
		t.Fatal("expected invalid proxy URL to fail closed, got nil error")
	}
	if !strings.Contains(err.Error(), "invalid proxy") {
		t.Fatalf("error = %v, want invalid proxy error", err)
	}
}
