package cliproxy

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

func TestRoundTripperForDirectBypassesProxy(t *testing.T) {
	t.Parallel()

	provider := newDefaultRoundTripperProvider()
	rt := provider.RoundTripperFor(&coreauth.Auth{ProxyURL: "direct"})
	transport, ok := rt.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T, want *http.Transport", rt)
	}
	if transport.Proxy != nil {
		t.Fatal("expected direct transport to disable proxy function")
	}
}

func TestRoundTripperForInvalidProxyFailsClosed(t *testing.T) {
	t.Parallel()

	var upstreamHit atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamHit.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	provider := newDefaultRoundTripperProvider()
	rt := provider.RoundTripperFor(&coreauth.Auth{ProxyURL: "invalid-scheme://127.0.0.1:9999"})
	if rt == nil {
		t.Fatal("expected non-nil fail-closed RoundTripper for invalid proxy URL")
	}
	client := &http.Client{Transport: rt}
	resp, err := client.Get(srv.URL)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("expected request with invalid auth.ProxyURL to fail closed, got nil error")
	}
	if upstreamHit.Load() {
		t.Fatal("upstream was contacted directly after invalid auth.ProxyURL (IP leak)")
	}
}
