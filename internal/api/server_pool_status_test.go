package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoeForge/moego/ojson"
)

func TestAccountPoolRuntimeStatusAndRestart(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "test-pool-management")
	cfg := testServerPoolConfig()
	cfg.AccountPools.Enabled = false
	server := newTestServerWithConfig(t, cfg)
	do := func(path, key string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = "127.0.0.1:12345"
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		rec := httptest.NewRecorder()
		server.engine.ServeHTTP(rec, req)
		return rec
	}
	statusPath := "/v8/management/account-pools/status"
	for _, path := range []string{statusPath, "/v8/management/observability/quota-shadow"} {
		if response := do(path, ""); response.Code != http.StatusUnauthorized {
			t.Fatalf("unprotected management route: %s %d", path, response.Code)
		}
		if response := do(path, "pool-client-key"); response.Code == http.StatusOK {
			t.Fatalf("inference key accessed management route: %s", path)
		}
	}
	check := func(state string, restart bool) {
		t.Helper()
		response := do(statusPath, "test-pool-management")
		value := ojson.ParseBytes(response.Body.Bytes())
		if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || value.Get("state").String() != state || value.Get("restart_required").Bool() != restart {
			t.Fatalf("runtime state: %d %s", response.Code, response.Body.String())
		}
	}
	check("disabled", false)
	next := cfg.CloneForRuntime()
	next.AccountPools.Enabled = true
	server.UpdateClients(next)
	check("restart_required", true)
	for _, path := range []string{"/pools/basic/v1/models", "/v1/models"} {
		response := do(path, "pool-client-key")
		if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "pool_restart_required") || !strings.Contains(response.Body.String(), "Restart") {
			t.Fatalf("mode transition: %d %s", response.Code, response.Body.String())
		}
	}
	server = newTestServerWithConfig(t, next)
	check("active", false)
	if response := do("/pools/basic/v1/models", "pool-client-key"); response.Code != http.StatusOK {
		t.Fatalf("restart did not activate pools: %d %s", response.Code, response.Body.String())
	}
	next = next.CloneForRuntime()
	next.AccountPools.Enabled = false
	server.UpdateClients(next)
	check("restart_required", true)
	if response := do("/v1/models", "pool-client-key"); response.Code != http.StatusServiceUnavailable {
		t.Fatal("disabling isolation silently restored global routing")
	}
}
