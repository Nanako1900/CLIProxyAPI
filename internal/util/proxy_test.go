package util

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
)

func TestSetProxyFailsClosedOnInvalidProxyURL(t *testing.T) {
	t.Parallel()

	var upstreamHit atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamHit.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := SetProxy(&config.SDKConfig{ProxyURL: "invalid-scheme://127.0.0.1:9999"}, &http.Client{})
	resp, err := client.Get(srv.URL)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("expected request with invalid proxy URL to fail closed, got nil error")
	}
	if upstreamHit.Load() {
		t.Fatal("upstream was contacted directly after invalid proxy configuration (IP leak)")
	}
}
