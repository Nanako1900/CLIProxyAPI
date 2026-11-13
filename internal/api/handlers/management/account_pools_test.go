package management

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MoeForge/moego/ojson"
	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	fileauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/auth"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

func TestCredentialPoolPersistsAndChecksETag(t *testing.T) {
	dir := t.TempDir()
	store := fileauth.NewFileTokenStore()
	store.SetBaseDir(dir)
	manager := coreauth.NewManager(store, nil, nil)
	enabled := true
	cfg := &config.Config{AuthDir: dir, SDKConfig: config.SDKConfig{AccountPools: config.AccountPoolsConfig{Enabled: true, Pools: []config.AccountPool{{ID: "premium", Name: "Premium", Enabled: &enabled}}}}}
	record := &coreauth.Auth{ID: "pool-credential.json", FileName: "pool-credential.json", Provider: "codex", Attributes: map[string]string{"path": filepath.Join(dir, "pool-credential.json")}, Metadata: map[string]any{"type": "codex", "access_token": "must-not-appear"}}
	if _, err := manager.Register(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	h := NewHandlerWithoutConfigFilePath(cfg, manager)
	router := gin.New()
	router.GET("/pool", h.GetCredentialPool)
	router.PATCH("/pool", h.PatchCredentialPool)
	read := httptest.NewRecorder()
	router.ServeHTTP(read, httptest.NewRequest("GET", "/pool?id=pool-credential.json", nil))
	etag := read.Header().Get("ETag")
	if read.Code != 200 || etag == "" {
		t.Fatalf("read: %d %s", read.Code, read.Body.String())
	}
	patch := func(match string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("PATCH", "/pool", strings.NewReader(`{"id":"pool-credential.json","pool_id":"premium"}`))
		req.Header.Set("Content-Type", "application/json")
		if match != "" {
			req.Header.Set("If-Match", match)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	if got := patch(""); got.Code != 428 {
		t.Fatalf("missing precondition: %d", got.Code)
	}
	if got := patch("\"stale\""); got.Code != 412 {
		t.Fatalf("stale precondition: %d", got.Code)
	}
	changed := patch(etag)
	if changed.Code != 200 {
		t.Fatalf("move: %d %s", changed.Code, changed.Body.String())
	}
	if strings.Contains(changed.Body.String(), "must-not-appear") {
		t.Fatal("secret leaked")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "pool-credential.json"))
	if err != nil {
		t.Fatal(err)
	}
	if ojson.GetBytes(raw, "pool_id").String() != "premium" {
		t.Fatalf("membership not persisted: %s", raw)
	}
	if got := patch(etag); got.Code != 412 {
		t.Fatalf("old ETag accepted after change: %d", got.Code)
	}
	unassign := httptest.NewRequest("PATCH", "/pool", strings.NewReader(`{"id":"pool-credential.json","pool_id":""}`))
	unassign.Header.Set("Content-Type", "application/json")
	unassign.Header.Set("If-Match", changed.Header().Get("ETag"))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, unassign)
	if rec.Code != 200 {
		t.Fatalf("unassign: %d %s", rec.Code, rec.Body.String())
	}
	raw, err = os.ReadFile(filepath.Join(dir, "pool-credential.json"))
	if err != nil {
		t.Fatal(err)
	}
	if ojson.GetBytes(raw, "pool_id").String() != "" {
		t.Fatal("unassignment not persisted")
	}
}
