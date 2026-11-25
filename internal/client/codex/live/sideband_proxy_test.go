package live

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
)

func TestSidebandDialerInvalidProxyFailsClosedAndHonorsRequestProxy(t *testing.T) {
	t.Parallel()

	var upstreamHit atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHit.Store(true)
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			_ = conn.Close()
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	dialer := newProxyAwareSidebandDialer(
		context.Background(),
		&config.Config{SDKConfig: sdkconfig.SDKConfig{ProxyURL: "invalid-scheme://127.0.0.1:9999"}},
		nil,
	)
	conn, resp, err := dialer.DialContext(context.Background(), wsURL, nil)
	if err == nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		_ = conn.Close()
		t.Fatal("expected sideband dial with invalid proxy to fail closed, got nil error")
	}
	if upstreamHit.Load() {
		t.Fatal("sideband server was contacted directly after invalid proxy configuration (IP leak)")
	}

	// Verify request proxy takes precedence over auth and global proxy.
	reqCtx := coreexecutor.WithRequestProxyURL(context.Background(), "http://req-proxy.example:8081")
	gotProxy := proxyURLForAuth(
		reqCtx,
		&config.Config{SDKConfig: sdkconfig.SDKConfig{ProxyURL: "http://global.example:8080"}},
		&auth.Auth{ProxyURL: "http://auth.example:8080"},
	)
	if gotProxy != "http://req-proxy.example:8081" {
		t.Fatalf("proxyURLForAuth = %q, want http://req-proxy.example:8081", gotProxy)
	}
}

func TestDirectRealtimeHeadersDoNotInjectHardcodedOriginatorAndRespectUALab(t *testing.T) {
	t.Parallel()

	// Without caller Originator, directRealtimeHeaders must not inject hardcoded "Codex Desktop".
	headers := directRealtimeHeaders(http.Header{"OpenAI-Alpha": {"v1"}})
	if got := headers.Get("Originator"); got != "" {
		t.Fatalf("directRealtimeHeaders injected Originator = %q, want empty", got)
	}

	// Unconfigured Codex UALab must explicitly set empty User-Agent to suppress Go-http-client.
	sanitizeCodexRealtimeWebsocketHeaders(headers, &config.Config{}, nil)
	if vals, ok := headers["User-Agent"]; !ok || len(vals) != 1 || vals[0] != "" {
		t.Fatalf("unconfigured User-Agent header = %#v (ok=%v), want explicit empty string", vals, ok)
	}

	// Configured Codex UALab (and per-account override) must apply cleanly.
	const newUA = "codex-tui/0.172.0 (Mac OS 27.3.0; arm64)"
	cfg := &config.Config{
		CodexHeaderDefaults: config.CodexHeaderDefaults{
			UserAgent:  "codex-tui/0.160.0 (old)",
			Originator: "codex_tui_old",
		},
	}
	cred := &auth.Auth{
		Provider: "codex",
		Attributes: map[string]string{
			"header:User-Agent": newUA,
			"header:Originator": "codex_tui_new",
		},
	}
	sanitizeCodexRealtimeWebsocketHeaders(headers, cfg, cred)
	if headers.Get("User-Agent") != newUA || headers.Get("Originator") != "codex_tui_new" {
		t.Fatalf("sanitized WS headers = UA:%q Originator:%q, want %q / codex_tui_new", headers.Get("User-Agent"), headers.Get("Originator"), newUA)
	}
}
