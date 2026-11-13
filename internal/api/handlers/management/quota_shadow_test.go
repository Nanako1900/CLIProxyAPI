package management

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MoeForge/moego/ojson"
	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	auth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

func TestQuotaShadowManagementStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{}
	router := gin.New()
	router.GET("/status", h.GetQuotaShadowStatus)
	read := func() *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/status", nil))
		if response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("status must not be cached")
		}
		return response
	}
	if response := read(); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing manager: %d", response.Code)
	}
	h.authManager = auth.NewManager(nil, nil, nil)
	defer h.authManager.StopQuotaShadow()
	response := read()
	value := ojson.ParseBytes(response.Body.Bytes())
	if response.Code != http.StatusOK || value.Get("state").String() != "disabled" || value.Get("recent").Raw != "[]" {
		t.Fatalf("unexpected status: %d %s", response.Code, response.Body.String())
	}
}

func TestQuotaShadowConfigPatchValidationAndConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	path := filepath.Join(t.TempDir(), "config.yaml")
	initial := "config-version: 8\nserver: {port: 8317}\naccess: {api-keys: [keep-key]}\nrouting: {strategy: round-robin}\n"
	if err := os.WriteFile(path, []byte(initial), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{cfg: cfg, configFilePath: path}
	router := gin.New()
	router.GET("/config", h.ConfigV8)
	router.PATCH("/config", h.ConfigV8)
	do := func(method, body, etag string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(method, "/config", strings.NewReader(body))
		if etag != "" {
			request.Header.Set("If-Match", etag)
		}
		router.ServeHTTP(response, request)
		return response
	}
	etag := do(http.MethodGet, "", "").Header().Get("ETag")
	valid := `{"routing":{"quota-shadow":{"enabled":true,"file":"shadow.jsonl","max-file-mb":2,"soft-limit-percent":85}}}`
	saved := do(http.MethodPatch, valid, etag)
	if saved.Code != http.StatusOK || saved.Header().Get("ETag") == etag {
		t.Fatalf("save: %d %s", saved.Code, saved.Body.String())
	}
	loaded, err := config.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Routing.QuotaShadow.Enabled || loaded.Routing.QuotaShadow.SoftLimitPercent != 85 || loaded.Routing.Strategy != "round-robin" || len(loaded.APIKeys) != 1 || loaded.APIKeys[0] != "keep-key" {
		t.Fatal("shadow patch changed unrelated configuration or was not persisted")
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	stale := do(http.MethodPatch, `{"routing":{"quota-shadow":{"enabled":false}}}`, etag)
	if stale.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale save: %d %s", stale.Code, stale.Body.String())
	}
	for _, body := range []string{
		`{"routing":{"quota-shadow":{"max-file-mb":-1}}}`,
		`{"routing":{"quota-shadow":{"soft-limit-percent":101}}}`,
		`{"routing":{"quota-shadow":{"file":"bad\npath"}}}`,
	} {
		response := do(http.MethodPatch, body, saved.Header().Get("ETag"))
		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("invalid settings: %d %s", response.Code, response.Body.String())
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatal("rejected write changed the configuration")
	}
}
