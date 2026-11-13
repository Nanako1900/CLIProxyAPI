package management

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MoeForge/moego/ojson"
	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/clientua"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

func TestClientUserAgentStatusExposesOnlyIdentityOverrides(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager := coreauth.NewManager(nil, nil, nil)
	defer manager.StopQuotaShadow()
	for _, auth := range []*coreauth.Auth{
		{ID: "private-file-name", Provider: "claude", Label: "Test client", Disabled: true,
			Attributes: map[string]string{"header:user-agent": "$X-Client-UA", "api_key": "private-key", "header:Authorization": "Bearer private-token"},
			Metadata:   map[string]any{"access_token": "private-access-token", "account_id": "private-account"}},
		{ID: "ignored-provider", Provider: "gemini", Attributes: map[string]string{"header:User-Agent": "not-listed"}},
		{ID: "no-override", Provider: "codex", Attributes: map[string]string{"api_key": "private-key"}},
	} {
		if _, errRegister := manager.Register(context.Background(), auth); errRegister != nil {
			t.Fatal(errRegister)
		}
	}
	store, errStore := clientua.NewStore(filepath.Join(t.TempDir(), "client-user-agents.yaml"))
	if errStore != nil {
		t.Fatal(errStore)
	}
	h := &Handler{cfg: &config.Config{}, authManager: manager, clientUserAgents: store}
	router := gin.New()
	router.GET("/status", h.ClientUserAgentStatus)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/status", nil))
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unexpected response: %d", response.Code)
	}
	value := ojson.ParseBytes(response.Body.Bytes())
	if len(value.Get("providers").Array()) != 2 || value.Get("catalogPersistence").String() != "disk" || value.Get("catalogLimit").Int() != clientua.LimitPerProvider {
		t.Fatal("missing runtime or catalog metadata")
	}
	overrides := value.Get("overrides").Array()
	if len(overrides) != 1 || overrides[0].Get("userAgent").String() != "$X-Client-UA" || overrides[0].Get("authIndex").String() == "" || !overrides[0].Get("disabled").Bool() {
		t.Fatal("incorrect override inventory")
	}
	for _, secret := range []string{"private-key", "private-token", "private-access-token", "private-account", "private-file-name", "not-listed"} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatalf("response exposed excluded value %q", secret)
		}
	}
}

func TestClientUserAgentStatusHandlesUnavailableAndHomeState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{}
	router := gin.New()
	router.GET("/status", h.ClientUserAgentStatus)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/status", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing config status: %d", response.Code)
	}
	h.cfg = &config.Config{}
	h.cfg.Home.Enabled = true
	h.cfg.ClaudeHeaderDefaults.UserAgent = "auto"
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/status", nil))
	value := ojson.ParseBytes(response.Body.Bytes())
	if response.Code != http.StatusOK || !value.Get("homeMode").Bool() || value.Get("catalogAvailable").Bool() || value.Get("overrideInventoryAvailable").Bool() || value.Get("providers.0.auto.enabled").Bool() || value.Get("overrides").Raw != "[]" {
		t.Fatal("unavailable resources must not be reported as enabled")
	}
}
