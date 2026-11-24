package management

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/logging"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

func TestAuthFileErrorsEndpointsAndListSummary(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tempDir := t.TempDir()
	authDir := filepath.Join(tempDir, "auths")
	logsDir := filepath.Join(tempDir, "logs")
	if errMk := os.MkdirAll(authDir, 0o755); errMk != nil {
		t.Fatalf("mkdir auths: %v", errMk)
	}
	if errMk := os.MkdirAll(logsDir, 0o755); errMk != nil {
		t.Fatalf("mkdir logs: %v", errMk)
	}
	authPath := filepath.Join(authDir, "oauth-claude.json")
	if errWrite := os.WriteFile(authPath, []byte(`{"type":"claude","email":"user@example.com"}`), 0o600); errWrite != nil {
		t.Fatalf("write auth file: %v", errWrite)
	}

	reqID := "req-12345678-abcd"
	logFile := filepath.Join(logsDir, "error-v1-messages-2026-11-24-"+logging.ShortRequestID(reqID)+".log")
	if errLog := os.WriteFile(logFile, []byte("sample error log"), 0o600); errLog != nil {
		t.Fatalf("write log file: %v", errLog)
	}

	mgr := coreauth.NewManager(nil, nil, nil)
	authRecord := &coreauth.Auth{
		ID:       "oauth-claude.json",
		FileName: "oauth-claude.json",
		Provider: "claude",
		Status:   coreauth.StatusActive,
		Attributes: map[string]string{
			"path": authPath,
		},
		Metadata: map[string]any{
			"email": "user@example.com",
		},
	}
	if _, errReg := mgr.Register(context.Background(), authRecord); errReg != nil {
		t.Fatalf("Register: %v", errReg)
	}

	reqCtx := logging.WithRequestID(context.Background(), reqID)
	mgr.MarkResult(reqCtx, coreauth.Result{
		AuthID:     "oauth-claude.json",
		Provider:   "claude",
		Model:      "claude-sonnet-4",
		RouteModel: "claude-sonnet-4",
		Phase:      "stream",
		Success:    false,
		Error: &coreauth.Error{
			Code:       "overloaded_error",
			Message:    "upstream overloaded: authorization=Bearer secret-xyz",
			HTTPStatus: http.StatusServiceUnavailable,
			Retryable:  true,
		},
	})

	cfg := &config.Config{
		AuthDir: authDir,
	}
	cfg.SDKConfig.ProxyURL = ""
	cfgPath := filepath.Join(tempDir, "config.yaml")
	h := NewHandler(cfg, cfgPath, mgr)
	h.logDir = logsDir

	engine := gin.New()
	engine.GET("/v8/management/credentials", func(c *gin.Context) {
		c.Set(ConfigV8ContextKey, true)
		h.ListAuthFiles(c)
	})
	engine.GET("/v8/management/credentials/errors", func(c *gin.Context) {
		c.Set(ConfigV8ContextKey, true)
		h.GetAuthFileErrors(c)
	})
	engine.DELETE("/v8/management/credentials/errors", func(c *gin.Context) {
		c.Set(ConfigV8ContextKey, true)
		h.ClearAuthFileErrors(c)
	})
	engine.GET("/v0/management/auth-files", h.ListAuthFiles)

	// 1. v8 list includes last_error and recent_error_count.
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v8/management/credentials", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("v8 list status=%d body=%s", rec.Code, rec.Body.String())
	}
	var listResp struct {
		Files []map[string]any `json:"files"`
	}
	if errDec := json.Unmarshal(rec.Body.Bytes(), &listResp); errDec != nil {
		t.Fatalf("decode v8 list: %v", errDec)
	}
	if len(listResp.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(listResp.Files))
	}
	if gotCount, _ := listResp.Files[0]["recent_error_count"].(float64); gotCount != 1 {
		t.Fatalf("expected recent_error_count=1, got %v", listResp.Files[0]["recent_error_count"])
	}
	lastErr, _ := listResp.Files[0]["last_error"].(map[string]any)
	if lastErr == nil || lastErr["code"] != "overloaded_error" {
		t.Fatalf("expected last_error summary in v8 list, got %v", listResp.Files[0]["last_error"])
	}

	// 2. v0 list stays untouched without new v8 error summary keys.
	recV0 := httptest.NewRecorder()
	engine.ServeHTTP(recV0, httptest.NewRequest(http.MethodGet, "/v0/management/auth-files", nil))
	if recV0.Code != http.StatusOK {
		t.Fatalf("v0 list status=%d", recV0.Code)
	}
	var listV0 struct {
		Files []map[string]any `json:"files"`
	}
	if errDec := json.Unmarshal(recV0.Body.Bytes(), &listV0); errDec != nil {
		t.Fatalf("decode v0 list: %v", errDec)
	}
	if _, exists := listV0.Files[0]["recent_error_count"]; exists {
		t.Fatalf("v0 list must not expose recent_error_count")
	}

	// 3. Detail endpoint returns full error state, model errors, and recent_errors with has_request_log.
	recDetail := httptest.NewRecorder()
	engine.ServeHTTP(recDetail, httptest.NewRequest(http.MethodGet, "/v8/management/credentials/errors?name=oauth-claude.json", nil))
	if recDetail.Code != http.StatusOK {
		t.Fatalf("errors detail status=%d body=%s", recDetail.Code, recDetail.Body.String())
	}
	if cc := recDetail.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("expected Cache-Control=no-store, got %q", cc)
	}
	var detail struct {
		AuthID           string           `json:"auth_id"`
		RecentErrorCount int              `json:"recent_error_count"`
		ModelErrors      []map[string]any `json:"model_errors"`
		RecentErrors     []map[string]any `json:"recent_errors"`
	}
	if errDec := json.Unmarshal(recDetail.Body.Bytes(), &detail); errDec != nil {
		t.Fatalf("decode detail: %v", errDec)
	}
	if detail.AuthID != "oauth-claude.json" || detail.RecentErrorCount != 1 || len(detail.RecentErrors) != 1 {
		t.Fatalf("unexpected detail response: %+v", detail)
	}
	if len(detail.ModelErrors) != 1 || detail.ModelErrors[0]["model"] != "claude-sonnet-4" {
		t.Fatalf("expected 1 model error, got %+v", detail.ModelErrors)
	}
	first := detail.RecentErrors[0]
	if first["phase"] != "stream" || first["request_id"] != reqID {
		t.Fatalf("unexpected recent error item: %+v", first)
	}
	if hasLog, _ := first["has_request_log"].(bool); !hasLog {
		t.Fatalf("expected has_request_log=true for matching log file %s, got %+v", logFile, first)
	}
	msg, _ := first["message"].(string)
	if strings.Contains(msg, "secret-xyz") {
		t.Fatalf("secret leaked in detail message: %q", msg)
	}

	// 4. Delete clears persisted history.
	recDel := httptest.NewRecorder()
	engine.ServeHTTP(recDel, httptest.NewRequest(http.MethodDelete, "/v8/management/credentials/errors?name=oauth-claude.json", nil))
	if recDel.Code != http.StatusOK {
		t.Fatalf("delete errors status=%d body=%s", recDel.Code, recDel.Body.String())
	}
	recAfter := httptest.NewRecorder()
	engine.ServeHTTP(recAfter, httptest.NewRequest(http.MethodGet, "/v8/management/credentials/errors?name=oauth-claude.json", nil))
	if recAfter.Code != http.StatusOK {
		t.Fatalf("after clear status=%d", recAfter.Code)
	}
	var after struct {
		RecentErrorCount int `json:"recent_error_count"`
	}
	_ = json.Unmarshal(recAfter.Body.Bytes(), &after)
	if after.RecentErrorCount != 0 {
		t.Fatalf("expected 0 errors after clear, got %d", after.RecentErrorCount)
	}

	// 5. Missing param returns 400; unknown auth returns 404.
	recBad := httptest.NewRecorder()
	engine.ServeHTTP(recBad, httptest.NewRequest(http.MethodGet, "/v8/management/credentials/errors", nil))
	if recBad.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", recBad.Code)
	}
	recMissing := httptest.NewRecorder()
	engine.ServeHTTP(recMissing, httptest.NewRequest(http.MethodGet, "/v8/management/credentials/errors?name=missing.json", nil))
	if recMissing.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", recMissing.Code)
	}

	_ = time.Second
}
