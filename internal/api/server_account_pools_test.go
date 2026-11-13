package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/pluginhost"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/pool"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func testServerPoolConfig() *config.Config {
	enabled := true
	return &config.Config{SDKConfig: config.SDKConfig{
		AccountPools: config.AccountPoolsConfig{Enabled: true, Pools: []config.AccountPool{{ID: "basic", Name: "Basic", Enabled: &enabled}, {ID: "premium", Name: "Premium", Enabled: &enabled}}},
		PoolClients:  []config.PoolClient{{ID: "basic-client", APIKey: "pool-client-key", AllowedPools: []string{"basic"}}},
	}}
}

func TestAccountPoolRoutesFailClosed(t *testing.T) {
	server := newTestServerWithConfig(t, testServerPoolConfig())
	for _, tc := range []struct {
		method, path, key string
		status            int
	}{
		{"GET", "/pools/basic/v1/models", "pool-client-key", 200},
		{"GET", "/pools/premium/v1/models", "pool-client-key", 403},
		{"GET", "/pools/unknown/v1/models", "pool-client-key", 403},
		{"GET", "/pools/basic/v1/models", "", 401},
		{"GET", "/v1/models", "pool-client-key", 400},
		{"GET", "/pools/basic/v1/responses", "pool-client-key", 501},
		{"POST", "/pools/basic/v1/realtime", "pool-client-key", 501},
		{"GET", "/pools/basic/openai/v1/videos/video-id", "pool-client-key", 501},
		{"GET", "/pools/basic/v1/tunnel", "pool-client-key", 501},
		{"POST", "/api/provider/openai/v1/chat/completions", "pool-client-key", 400},
		{"GET", "/pools/%62asic/v1/models", "pool-client-key", 400},
		{"GET", "/healthz", "", 200},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			if tc.key != "" {
				req.Header.Set("Authorization", "Bearer "+tc.key)
			}
			req.Header.Set("X-Pool-ID", "premium")
			rec := httptest.NewRecorder()
			server.engine.ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status=%d want=%d body=%s", rec.Code, tc.status, rec.Body.String())
			}
		})
	}
}

type poolResourcePlugin struct{}

func (p poolResourcePlugin) RegisterManagement(context.Context, pluginapi.ManagementRegistrationRequest) (pluginapi.ManagementRegistrationResponse, error) {
	return pluginapi.ManagementRegistrationResponse{
		Routes: []pluginapi.ManagementRoute{
			{Method: http.MethodGet, Path: "/legacy.html", Menu: "Legacy page", Handler: p},
		},
		Resources: []pluginapi.ResourceRoute{
			{Path: "/index.html", Menu: "Plugin page", Handler: p},
			{Path: "/assets/app.js", Handler: p},
			{Path: "/assets/style.css", Handler: p},
		},
	}, nil
}

func (poolResourcePlugin) HandleManagement(_ context.Context, req pluginapi.ManagementRequest) (pluginapi.ManagementResponse, error) {
	return pluginapi.ManagementResponse{Body: []byte(req.Path)}, nil
}

func TestAccountPoolPluginResourcesRemainPublic(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "pool-management-key")
	host := pluginhost.New()
	t.Cleanup(host.ShutdownAll)
	for _, id := range []string{"first-ui", "second-ui"} {
		host.RegisterPluginForTest(id, pluginapi.Plugin{
			Capabilities: pluginapi.Capabilities{ManagementAPI: poolResourcePlugin{}},
		})
	}
	server := newTestServerWithConfig(t, testServerPoolConfig(), WithPluginHost(host))
	for _, id := range []string{"first-ui", "second-ui"} {
		for _, resource := range []string{"index.html", "legacy.html", "assets/app.js", "assets/style.css"} {
			path := "/v0/resource/plugins/" + id + "/" + resource
			for _, key := range []string{"", "invalid-api-key"} {
				t.Run(path+"/"+key, func(t *testing.T) {
					req := httptest.NewRequest(http.MethodGet, path, nil)
					if key != "" {
						req.Header.Set("Authorization", "Bearer "+key)
					}
					rec := httptest.NewRecorder()
					server.engine.ServeHTTP(rec, req)
					if rec.Code != http.StatusOK || rec.Body.String() != path {
						t.Fatalf("status=%d body=%s, want 200 and plugin resource", rec.Code, rec.Body.String())
					}
				})
			}
		}
	}
	for _, tc := range []struct {
		method, path, key string
		status            int
	}{
		{http.MethodGet, "/v0/resource/plugins/first-ui/missing.html", "", http.StatusNotFound},
		{http.MethodGet, "/v0/resource/plugins/missing/index.html", "", http.StatusNotFound},
		{http.MethodPost, "/v0/resource/plugins/first-ui/index.html", "", http.StatusNotFound},
		{http.MethodGet, "/v0/resource/plugins-other/first-ui/index.html", "", http.StatusUnauthorized},
		{http.MethodGet, "/pools/basic/v0/resource/plugins/first-ui/index.html", "pool-client-key", http.StatusNotImplemented},
		{http.MethodGet, "/pools/basic/v1/models", "", http.StatusUnauthorized},
		{http.MethodGet, "/v8/management/plugins", "", http.StatusUnauthorized},
		{http.MethodGet, "/v8/management/plugins", "pool-client-key", http.StatusUnauthorized},
		{http.MethodGet, "/v8/management/plugins", "pool-management-key", http.StatusOK},
	} {
		t.Run(tc.method+tc.path+"/"+tc.key, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.RemoteAddr = "127.0.0.1:12345"
			if tc.key != "" {
				req.Header.Set("Authorization", "Bearer "+tc.key)
			}
			rec := httptest.NewRecorder()
			server.engine.ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status=%d want=%d body=%s", rec.Code, tc.status, rec.Body.String())
			}
		})
	}
}

func TestAccountPoolModelsDoNotExposeForeignCredentials(t *testing.T) {
	server := newTestServerWithConfig(t, testServerPoolConfig())
	for _, item := range []struct{ id, pool, model string }{{"catalog-basic", "basic", "pool-basic-model"}, {"catalog-premium", "premium", "pool-premium-model"}, {"catalog-unassigned", "", "pool-unassigned-model"}} {
		if _, err := server.handlers.AuthManager.Register(context.Background(), &coreauth.Auth{ID: item.id, Provider: "openai", PoolID: item.pool, Status: coreauth.StatusActive}); err != nil {
			t.Fatal(err)
		}
		registry.GetGlobalRegistry().RegisterClient(item.id, "openai", []*registry.ModelInfo{{ID: item.model, Object: "model", OwnedBy: "openai"}})
		t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(item.id) })
	}
	for _, path := range []string{"/pools/basic/v1/models", "/pools/basic/v1/models?client_version=0.100.0"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer pool-client-key")
		rec := httptest.NewRecorder()
		server.engine.ServeHTTP(rec, req)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), "pool-basic-model") || strings.Contains(rec.Body.String(), "pool-premium-model") || strings.Contains(rec.Body.String(), "pool-unassigned-model") {
			t.Fatalf("catalog leak or missing pool: %d %s", rec.Code, rec.Body.String())
		}
	}
}

func TestAccountPoolContextReachesRegisteredHandler(t *testing.T) {
	cfg := testServerPoolConfig()
	cfg.AccountPools.Unscoped = config.PoolUnscopedConfig{Mode: "default", PoolID: "basic"}
	server := newTestServerWithConfig(t, cfg)
	for _, path := range []string{"/pools/basic/v1/chat/completions", "/v1/chat/completions"} {
		req := httptest.NewRequest("POST", path, strings.NewReader(`{"model":"missing-test-model","messages":[]}`))
		req.Header.Set("Authorization", "Bearer pool-client-key")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		server.engine.ServeHTTP(rec, req)
		if rec.Code == 401 || rec.Code == 403 || rec.Code == 404 || strings.Contains(rec.Body.String(), "pool_required") {
			t.Fatalf("authorized route lost scope: %d %s", rec.Code, rec.Body.String())
		}
	}
}

func TestAccountPoolDisabledLeavesLegacyRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{SDKConfig: config.SDKConfig{APIKeys: []string{"ordinary-key"}}}
	server := newTestServerWithConfig(t, cfg)
	for _, path := range []string{"/v1/models", "/pools/basic/v1/models"} {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Authorization", "Bearer ordinary-key")
		rec := httptest.NewRecorder()
		server.engine.ServeHTTP(rec, req)
		want := 200
		if strings.HasPrefix(path, "/pools/") {
			want = 404
		}
		if rec.Code != want {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
		}
	}
}

type poolHTTPExecutor struct {
	calls  []string
	scopes []string
}

func (*poolHTTPExecutor) Identifier() string { return "pool-http" }
func (e *poolHTTPExecutor) Execute(ctx context.Context, a *coreauth.Auth, _ coreexecutor.Request, _ coreexecutor.Options) (coreexecutor.Response, error) {
	scope, _ := pool.FromContext(ctx)
	e.calls = append(e.calls, a.ID)
	e.scopes = append(e.scopes, scope.ID)
	return coreexecutor.Response{Payload: []byte(`{"id":"test","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)}, nil
}
func (e *poolHTTPExecutor) ExecuteStream(ctx context.Context, a *coreauth.Auth, r coreexecutor.Request, o coreexecutor.Options) (*coreexecutor.StreamResult, error) {
	if _, err := e.Execute(ctx, a, r, o); err != nil {
		return nil, err
	}
	ch := make(chan coreexecutor.StreamChunk, 1)
	ch <- coreexecutor.StreamChunk{Payload: []byte("data: [DONE]\n\n")}
	close(ch)
	return &coreexecutor.StreamResult{Chunks: ch}, nil
}
func (e *poolHTTPExecutor) CountTokens(ctx context.Context, a *coreauth.Auth, r coreexecutor.Request, o coreexecutor.Options) (coreexecutor.Response, error) {
	return e.Execute(ctx, a, r, o)
}
func (*poolHTTPExecutor) Refresh(_ context.Context, a *coreauth.Auth) (*coreauth.Auth, error) {
	return a, nil
}
func (*poolHTTPExecutor) HttpRequest(context.Context, *coreauth.Auth, *http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("unexpected direct path")
}

func TestAccountPoolHTTPDispatchStaysInsidePool(t *testing.T) {
	cfg := testServerPoolConfig()
	cfg.AccountPools.Unscoped = config.PoolUnscopedConfig{Mode: "default", PoolID: "basic"}
	server := newTestServerWithConfig(t, cfg)
	manager := server.handlers.AuthManager
	// The production builder always attaches a host, even when no plugin
	// scheduler is installed. A non-nil host must not reject pooled execution.
	manager.SetPluginScheduler(pluginhost.New())
	e := &poolHTTPExecutor{}
	manager.RegisterExecutor(e)
	for _, item := range []struct{ id, pool string }{{"http-basic", "basic"}, {"http-premium", "premium"}} {
		if _, err := manager.Register(context.Background(), &coreauth.Auth{ID: item.id, PoolID: item.pool, Provider: e.Identifier(), Status: coreauth.StatusActive}); err != nil {
			t.Fatal(err)
		}
		registry.GetGlobalRegistry().RegisterClient(item.id, e.Identifier(), []*registry.ModelInfo{{ID: "pool-http-model", Object: "model"}})
		t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(item.id) })
	}
	for _, path := range []string{"/pools/basic/v1/chat/completions", "/v1/chat/completions", "/pools/basic/v1/messages?beta=true", "/v1/messages?beta=true"} {
		for _, stream := range []bool{false, true} {
			req := httptest.NewRequest("POST", path, strings.NewReader(fmt.Sprintf(`{"model":"pool-http-model","pool_id":"premium","messages":[{"role":"user","content":"test"}],"stream":%t}`, stream)))
			req.Header.Set("Authorization", "Bearer pool-client-key")
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Pool-ID", "premium")
			rec := httptest.NewRecorder()
			server.engine.ServeHTTP(rec, req)
			if rec.Code != 200 {
				t.Fatalf("%s stream=%v: %d %s", path, stream, rec.Code, rec.Body.String())
			}
		}
	}
	reqAuto := httptest.NewRequest("POST", "/pools/basic/v1/chat/completions", strings.NewReader(`{"model":"auto","messages":[{"role":"user","content":"test"}]}`))
	reqAuto.Header.Set("Authorization", "Bearer pool-client-key")
	reqAuto.Header.Set("Content-Type", "application/json")
	autoRec := httptest.NewRecorder()
	server.engine.ServeHTTP(autoRec, reqAuto)
	if autoRec.Code != 200 {
		t.Fatalf("scoped auto failed: %d %s", autoRec.Code, autoRec.Body.String())
	}
	if len(e.calls) != 9 {
		t.Fatalf("calls=%v", e.calls)
	}
	for i, id := range e.calls {
		if id != "http-basic" || e.scopes[i] != "basic" {
			t.Fatalf("pool escape: calls=%v scopes=%v", e.calls, e.scopes)
		}
	}
}
