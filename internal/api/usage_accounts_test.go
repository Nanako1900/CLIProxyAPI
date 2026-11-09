package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/MoeForge/moego/ojson"
	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/api/handlers"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
)

func TestAccountUsageEndToEndAndAccessControl(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{AuthDir: t.TempDir(), UsageAccounting: config.UsageAccountingConfig{Enabled: true}}
	cfg.RemoteManagement.SecretKey = "configured-for-local-password"
	s := NewServer(cfg, nil, nil, filepath.Join(t.TempDir(), "config.yaml"), WithLocalManagementPassword("test-password"), WithRouterConfigurator(func(engine *gin.Engine, base *handlers.BaseAPIHandler, _ *config.Config) {
		engine.POST("/accounting-test", func(c *gin.Context) {
			ctx, cancel := base.GetContextWithCancel(nil, c, context.Background())
			defer cancel()
			usage.PublishRecord(ctx, usage.Record{RequestID: "http-execution", AuthID: "auth", AuthIndex: "credential", AccountKey: "stable-principal", AccountLabel: "Test account", Provider: "openai", Model: "gpt-4o", Detail: usage.Detail{Reported: true, InputTokens: 1000, OutputTokens: 200}})
			c.Status(http.StatusOK)
		})
	}))
	t.Cleanup(func() {
		if errStop := s.Stop(context.Background()); errStop != nil {
			t.Error(errStop)
		}
	})
	request := func(method, path string, authorized bool, want int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, nil)
		req.RemoteAddr = "127.0.0.1:1234"
		if authorized {
			req.Header.Set("Authorization", "Bearer test-password")
		}
		response := httptest.NewRecorder()
		s.engine.ServeHTTP(response, req)
		if response.Code != want {
			t.Fatalf("%s: status %d, body %s", path, response.Code, response.Body.String())
		}
		return response
	}
	request(http.MethodPost, "/accounting-test", false, 200)
	if errProcess := s.usageAccounting.Store().ProcessPending(); errProcess != nil {
		t.Fatal(errProcess)
	}
	const prefix = "/v8/management/observability/usage"
	request(http.MethodGet, prefix+"/accounts", false, 401)
	response := request(http.MethodGet, prefix+"/accounts", true, 200)
	root := ojson.NewDoc(response.Body.Bytes())
	id := root.Get("items.0.account_uid").String()
	if id == "" || root.Get("items.0.totals.output_tokens").Int() != 200 || root.Get("items.0.totals.executions").Int() != 1 {
		t.Fatal(response.Body.String())
	}
	request(http.MethodGet, prefix+"/accounts/"+id, true, 200)
	request(http.MethodGet, prefix+"/accounts/"+id+"/requests", true, 200)
	request(http.MethodGet, prefix+"/accounts/unknown", true, 404)
	request(http.MethodGet, prefix+"/accounts?limit=201", true, 400)
	request(http.MethodGet, prefix+"/accounts?from=bad", true, 400)
	request(http.MethodGet, prefix+"/accounts?from=2026-11-09&to=2026-11-01", true, 400)
	request(http.MethodGet, prefix+"/status", true, 200)
	dashboard := request(http.MethodGet, "/usage.html", false, 200)
	if dashboard.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("dashboard missing CSP")
	}
}
