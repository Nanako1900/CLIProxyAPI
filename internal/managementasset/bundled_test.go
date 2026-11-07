package managementasset

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
)

func TestBundledPanelIsPinnedAndExplicitOverrideWins(t *testing.T) {
	t.Setenv("MANAGEMENT_STATIC_PATH", "")
	path := filepath.Join(t.TempDir(), "management.html")
	t.Setenv("MANAGEMENT_BUNDLED_PATH", path)
	if err := os.WriteFile(path, []byte("bundled-panel"), 0600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	if FilePath("config.yaml") != path {
		t.Fatal("bundle path not used")
	}
	if _, skip := autoUpdateSkipReason(&config.Config{}); !skip {
		t.Fatal("bundled panel updater enabled")
	}
	if !EnsureLatestManagementHTML(t.Context(), t.TempDir(), "", server.URL) || calls.Load() != 0 {
		t.Fatal("bundled panel was fetched from network")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if EnsureLatestManagementHTML(t.Context(), t.TempDir(), "", server.URL) || calls.Load() != 0 {
		t.Fatal("missing bundle silently replaced")
	}
	override := filepath.Join(t.TempDir(), "management.html")
	t.Setenv("MANAGEMENT_STATIC_PATH", override)
	if BundledFilePath() != "" || FilePath("config.yaml") != override {
		t.Fatal("explicit static override lost precedence")
	}
	if _, skip := autoUpdateSkipReason(&config.Config{}); skip {
		t.Fatal("explicit override unexpectedly pinned")
	}
}
