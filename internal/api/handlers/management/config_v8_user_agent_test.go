package management

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MoeForge/moego/ojson"
	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
)

func TestConfigV8UserAgentSaveReloadAndInvalidValue(t *testing.T) {
	gin.SetMode(gin.TestMode)
	path := filepath.Join(t.TempDir(), "config.yaml")
	raw := []byte("config-version: 8\nserver: {port: 8317}\nplugins: {enabled: false}\n")
	if errWrite := os.WriteFile(path, raw, 0600); errWrite != nil {
		t.Fatal(errWrite)
	}
	cfg, errLoad := config.LoadConfig(path)
	if errLoad != nil {
		t.Fatal(errLoad)
	}
	handler := &Handler{cfg: cfg, configFilePath: path}
	reloads := make(chan *config.Config, 2)
	handler.SetConfigReloadHook(func(_ context.Context, next *config.Config) { reloads <- next })
	router := gin.New()
	router.PATCH("/v8/management/config", handler.ConfigV8)
	recorder := httptest.NewRecorder()
	body := `{"oauth":{"providers":{"codex":{"header-defaults":{"user-agent":"auto"}}}},"upstream":{"claude":{"header-defaults":{"user-agent":"claude-cli/2.1.319 (external, cli)"}}}}`
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPatch, "/v8/management/config", strings.NewReader(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("save failed: %s", recorder.Body)
	}
	select {
	case updated := <-reloads:
		if updated.CodexHeaderDefaults.UserAgent != "auto" || updated.ClaudeHeaderDefaults.UserAgent != "claude-cli/2.1.319 (external, cli)" || updated.Plugins.Enabled {
			t.Fatal("incorrect native reload snapshot")
		}
	case <-t.Context().Done():
		t.Fatal("config reload canceled")
	}
	saved, errRead := os.ReadFile(path)
	if errRead != nil {
		t.Fatal(errRead)
	}
	loaded, errLoad := config.LoadConfig(path)
	if errLoad != nil || loaded.CodexHeaderDefaults.UserAgent != "auto" {
		t.Fatalf("saved value did not persist: %v", errLoad)
	}
	if loaded.ForAPIKey().CodexHeaderDefaults.UserAgent != "" {
		t.Fatal("OAuth setting leaked into API-key scope")
	}
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPatch, "/v8/management/config", strings.NewReader(`{"upstream":{"claude":{"header-defaults":{"user-agent":"bad\r\nInjected: yes"}}}}`)))
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid header accepted: %d", recorder.Code)
	}
	after, errRead := os.ReadFile(path)
	if errRead != nil || string(after) != string(saved) {
		t.Fatal("invalid save changed the configuration")
	}
}

func TestConfigV8GeminiUserAgentRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("config-version: 8\nupstream: {claude: {header-defaults: {user-agent: claude-existing}}}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{cfg: cfg, configFilePath: path}
	router := gin.New()
	const endpoint = "/v8/management/config"
	router.PATCH(endpoint, h.ConfigV8)
	router.GET(endpoint, h.ConfigV8)
	for _, ua := range []string{"team-identity", "auto", "bad\\r\\nHeader: injected", ""} {
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		response := httptest.NewRecorder()
		body := `{"upstream":{"gemini":{"header-defaults":{"user-agent":"` + ua + `"}}}}`
		router.ServeHTTP(response, httptest.NewRequest(http.MethodPatch, endpoint, strings.NewReader(body)))
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(ua, "bad") || ua == "auto" {
			if response.Code != http.StatusUnprocessableEntity || string(before) != string(after) {
				t.Fatalf("invalid/auto UA write: %q -> %d %s", ua, response.Code, response.Body)
			}
			continue
		}
		if response.Code != http.StatusOK {
			t.Fatalf("setting %q: %d %s", ua, response.Code, response.Body)
		}
		get := httptest.NewRecorder()
		router.ServeHTTP(get, httptest.NewRequest(http.MethodGet, endpoint, nil))
		if get.Code != http.StatusOK || ojson.Get(get.Body.String(), "upstream.gemini.header-defaults.user-agent").String() != ua || ojson.Get(get.Body.String(), "upstream.claude.header-defaults.user-agent").String() != "claude-existing" {
			t.Fatalf("read back %q: %d %s", ua, get.Code, get.Body)
		}
		loaded, err := config.LoadConfig(path)
		if err != nil || loaded.GeminiHeaderDefaults.UserAgent != ua || loaded.ClaudeHeaderDefaults.UserAgent != "claude-existing" {
			t.Fatalf("saved configuration did not retain %q: %v", ua, err)
		}
	}
}

func TestConfigV8UnrelatedPatchSucceedsAfterLegacyGeminiAutoStartupMigration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	path := filepath.Join(t.TempDir(), "config.yaml")
	raw := []byte("config-version: 8\nserver: {port: 8317}\nupstream: {gemini: {header-defaults: {user-agent: auto}}}\n")
	if errWrite := os.WriteFile(path, raw, 0600); errWrite != nil {
		t.Fatal(errWrite)
	}
	cfg, errLoad := config.LoadConfig(path)
	if errLoad != nil {
		t.Fatalf("startup LoadConfig failed on legacy Gemini auto: %v", errLoad)
	}
	h := &Handler{cfg: cfg, configFilePath: path}
	router := gin.New()
	router.PATCH("/v8/management/config", h.ConfigV8)

	// Unrelated save must succeed because startup migrated the legacy "auto" value on disk.
	res := httptest.NewRecorder()
	router.ServeHTTP(res, httptest.NewRequest(http.MethodPatch, "/v8/management/config", strings.NewReader(`{"server":{"port":8318}}`)))
	if res.Code != http.StatusOK {
		t.Fatalf("unrelated PATCH after startup migration failed: %d %s", res.Code, res.Body)
	}

	// Explicitly writing Gemini "auto" via Management API must still be rejected.
	resAuto := httptest.NewRecorder()
	router.ServeHTTP(resAuto, httptest.NewRequest(http.MethodPatch, "/v8/management/config", strings.NewReader(`{"upstream":{"gemini":{"header-defaults":{"user-agent":"auto"}}}}`)))
	if resAuto.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 rejecting Gemini auto via Management API, got %d %s", resAuto.Code, resAuto.Body)
	}
}
