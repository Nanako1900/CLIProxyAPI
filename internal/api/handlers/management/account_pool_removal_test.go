package management

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

func TestAccountPoolRemovalRequiresDisabledEmptyPoolAndVersion(t *testing.T) {
	for _, tc := range []struct {
		name            string
		enabled, member bool
		match           string
		status          int
		code            string
	}{
		{"enabled", true, false, "current", 409, "pool_must_be_disabled"},
		{"occupied", false, true, "current", 409, "pool_not_empty"},
		{"missing version", false, false, "", 428, "precondition_required"},
		{"wildcard version", false, false, "*", 428, "precondition_required"},
		{"stale version", false, false, "stale", 412, "precondition_failed"},
		{"empty disabled", false, false, "current", 200, "ok"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			enabled := "false"
			if tc.enabled {
				enabled = "true"
			}
			raw := "config-version: 8\nserver: {port: 8317}\nrouting:\n  account-pools:\n    pools: [{id: retired, name: Retired, enabled: " + enabled + "}]\n"
			if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := config.LoadConfig(path)
			if err != nil {
				t.Fatal(err)
			}
			m := coreauth.NewManager(nil, nil, nil)
			if tc.member {
				if _, err := m.Register(context.Background(), &coreauth.Auth{ID: "disabled-account", Disabled: true, PoolID: "retired"}); err != nil {
					t.Fatal(err)
				}
			}
			h := &Handler{cfg: cfg, configFilePath: path, authManager: m}
			router := gin.New()
			router.PATCH("/config", h.ConfigV8)
			req := httptest.NewRequest("PATCH", "/config", strings.NewReader(`{"routing":{"account-pools":{"pools":[]}}}`))
			if tc.match == "current" {
				req.Header.Set("If-Match", configV8ETag(config.ComputeSourceHash([]byte(raw))))
			} else if tc.match == "*" {
				req.Header.Set("If-Match", "*")
			} else if tc.match != "" {
				req.Header.Set("If-Match", `"stale"`)
			}
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.code) {
				t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
			}
			after, errRead := os.ReadFile(path)
			if errRead != nil {
				t.Fatal(errRead)
			}
			if tc.status != 200 && string(after) != raw {
				t.Fatal("rejected deletion modified config")
			}
			if tc.status == 200 {
				loaded, errLoad := config.LoadConfig(path)
				if errLoad != nil || len(loaded.AccountPools.Pools) != 0 || loaded.Port != 8317 {
					t.Fatal("deletion did not preserve unrelated configuration")
				}
			}
		})
	}
}

func TestPoolRemovalSerializesWithMembershipPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	// The handler can briefly lag an external config write. The removal must
	// still wait for an already admitted assignment to finish persistence.
	raw := "config-version: 8\nrouting:\n  account-pools:\n    pools: [{id: retired, name: Retired, enabled: false}]\n"
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	cfg.AccountPools.Pools[0].Enabled = &enabled
	store := &blockingPatchAuthStore{entered: make(chan struct{}), release: make(chan struct{})}
	manager := coreauth.NewManager(store, nil, nil)
	registered, err := manager.Register(coreauth.WithSkipPersist(context.Background()), &coreauth.Auth{
		ID: "account", Provider: "kiro4cpa", Metadata: map[string]any{"type": "kiro4cpa"},
	})
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{cfg: cfg, configFilePath: path, authManager: manager}
	router := gin.New()
	router.PATCH("/config", h.ConfigV8)
	router.PATCH("/pool", h.PatchCredentialPool)
	assignment := httptest.NewRequest("PATCH", "/pool", strings.NewReader(`{"id":"account","pool_id":"retired"}`))
	assignment.Header.Set("If-Match", credentialPoolETag(registered))
	assigned := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, assignment)
		assigned <- rec
	}()
	<-store.entered
	locked := !h.poolMutationMu.TryLock()
	if !locked {
		h.poolMutationMu.Unlock()
	}
	removal := httptest.NewRequest("PATCH", "/config", strings.NewReader(`{"routing":{"account-pools":{"pools":[]}}}`))
	removal.Header.Set("If-Match", configV8ETag(config.ComputeSourceHash([]byte(raw))))
	removed := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, removal)
		removed <- rec
	}()
	close(store.release)
	if rec := <-assigned; rec.Code != 200 {
		t.Fatalf("assignment: %d %s", rec.Code, rec.Body.String())
	}
	if rec := <-removed; rec.Code != 409 || !strings.Contains(rec.Body.String(), "pool_not_empty") {
		t.Fatalf("removal raced persistence: %d %s", rec.Code, rec.Body.String())
	}
	if !locked {
		t.Fatal("membership persistence was not fenced against config removal")
	}
}
