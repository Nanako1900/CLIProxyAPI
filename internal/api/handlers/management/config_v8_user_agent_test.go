package management

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
