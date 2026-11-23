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
	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
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
		{ID: "ignored-provider", Provider: "ignored", Attributes: map[string]string{"header:User-Agent": "not-listed"}},
		{ID: "cli", Provider: "gemini-cli", FileName: "private-gemini-auth.json", Label: "operator@example.test (private-gcp-project)", Metadata: map[string]any{"headers": map[string]any{"user-agent": "cli-file", "Authorization": "private-token"}}, Attributes: map[string]string{"header:User-Agent": "stale"}},
		{ID: "ai", Provider: "aistudio", Attributes: map[string]string{"header:User-Agent": "not-listed"}},
		{ID: "gem", Provider: "gemini", Label: "gemini-apikey", Attributes: map[string]string{
			"header:User-Agent": "api", coreauth.AttributeAPIKey: "private-key", coreauth.AttributeSource: "config:gemini",
		}},
		{ID: "interact", Provider: "gemini-interactions", Attributes: map[string]string{"header:User-Agent": "interactions"}},
		{ID: "vertex", Provider: "vertex", FileName: "private-vertex-auth.json", Label: "/credentials/private-vertex-service-account.json", Attributes: map[string]string{"header:User-Agent": "vertex"}},
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
	if len(value.Get("providers").Array()) != 3 || value.Get("catalogPersistence").String() != "disk" || value.Get("catalogLimit").Int() != clientua.LimitPerProvider {
		t.Fatal("missing runtime or catalog metadata")
	}
	overrides := value.Get("overrides").Array()
	wantValues := map[string]bool{"cli-file": false, "api": false, "interactions": false, "vertex": false}
	for _, row := range overrides {
		if row.Get("provider").String() == "gemini" {
			ua := row.Get("userAgent").String()
			wantValues[ua] = true
			switch ua {
			case "cli-file", "vertex":
				index := row.Get("authIndex").String()
				if index == "" || row.Get("name").String() != index || row.Get("source").String() != "credential" {
					t.Errorf("file-backed %s override should display its credential index", ua)
				}
			case "api":
				if row.Get("name").String() != "gemini-apikey" || row.Get("source").String() != "config" {
					t.Error("missing config credential label")
				}
			}
		}
	}
	for ua, seen := range wantValues {
		if !seen {
			t.Errorf("missing Gemini override %q", ua)
		}
	}
	if len(overrides) != 5 || overrides[0].Get("userAgent").String() != "$X-Client-UA" || overrides[0].Get("authIndex").String() == "" || !overrides[0].Get("disabled").Bool() {
		t.Fatal("incorrect override inventory")
	}
	for _, secret := range []string{"private-key", "private-token", "private-access-token", "private-account", "private-file-name", "not-listed", "operator@example.test", "private-gcp-project", "/credentials/", "private-vertex-service-account", "private-gemini-auth", "private-vertex-auth"} {
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

func TestClaudeAPIKeyUserAgentStatusMatchesDuplicateHeaders(t *testing.T) {
	for _, tc := range []struct{ name, canonical, want string }{
		{"canonical", "canonical/1", "canonical/1"},
		{"empty canonical", " ", "first/1"},
		{"invalid canonical", "bad\nvalue", "first/1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			auth := &coreauth.Auth{Provider: "claude", Attributes: map[string]string{
				"api_key": "test-key", "header:User-Agent": tc.canonical,
				"header:USER-AGENT": "first/1", "header:user-agent": "second/1",
			}}
			for range 64 {
				req := httptest.NewRequest(http.MethodGet, "/", nil)
				helps.ApplyClaudeAPIKeyHeaders(req, nil, auth, nil)
				provider, reported := credentialUserAgentOverride(auth)
				if got := req.UserAgent(); got != tc.want || provider != "claude" || reported != tc.want {
					t.Fatalf("outbound = %q, status = (%q, %q), want Claude UA %q", got, provider, reported, tc.want)
				}
			}
		})
	}
}
