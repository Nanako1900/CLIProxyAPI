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
	"github.com/router-for-me/CLIProxyAPI/v8/internal/pluginhost"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/watcher/synthesizer"
	fileauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/auth"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// A token-focused plugin need not know about host-side membership settings.
type poolPluginParser struct{ host *pluginhost.Host }

func (p poolPluginParser) ParseAuth(_ context.Context, req pluginapi.AuthParseRequest) (*coreauth.Auth, bool, error) {
	return p.host.AuthDataToCoreAuth(pluginapi.AuthData{
		ID: req.FileName, Provider: "kiro4cpa", FileName: req.FileName,
		StorageJSON: req.RawJSON,
		Metadata:    map[string]any{"access_token": ojson.GetBytes(req.RawJSON, "access_token").String()},
	}, req.Path, req.FileName), true, nil
}

func TestPluginPoolMembershipSurvivesWatcherRefreshAndRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kiro-account.json")
	if err := os.WriteFile(path, []byte(`{"type":"kiro4cpa","access_token":"test-token"}`), 0600); err != nil {
		t.Fatal(err)
	}
	parser := poolPluginParser{pluginhost.New()}
	fileauth.RegisterPluginAuthParser(parser)
	t.Cleanup(func() { fileauth.RegisterPluginAuthParser(nil) })
	store := fileauth.NewFileTokenStore()
	store.SetBaseDir(dir)
	manager := coreauth.NewManager(store, nil, nil)
	t.Cleanup(manager.StopAutoRefresh)
	if err := manager.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	enabled := true
	cfg := &config.Config{AuthDir: dir, SDKConfig: config.SDKConfig{AccountPools: config.AccountPoolsConfig{
		Enabled: true, Pools: []config.AccountPool{{ID: "kiro", Name: "Kiro", Enabled: &enabled}},
	}}}
	manager.SetConfig(cfg)
	h := NewHandlerWithoutConfigFilePath(cfg, manager)
	router := gin.New()
	router.GET("/pool", h.GetCredentialPool)
	router.PATCH("/pool", h.PatchCredentialPool)
	read := func() *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		router.ServeHTTP(r, httptest.NewRequest("GET", "/pool?id=kiro-account.json", nil))
		return r
	}
	for _, poolID := range []string{"kiro", "", "kiro"} {
		etag := read().Header().Get("ETag")
		request := httptest.NewRequest("PATCH", "/pool", strings.NewReader(`{"id":"kiro-account.json","pool_id":"`+poolID+`"}`))
		request.Header.Set("If-Match", etag)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, request)
		if rec.Code != 200 {
			t.Fatalf("assignment: %d %s", rec.Code, rec.Body.String())
		}
		data, errRead := os.ReadFile(path)
		if errRead != nil || ojson.GetBytes(data, "pool_id").String() != poolID {
			t.Fatalf("membership was not saved: %v", errRead)
		}
		snapshots, errSynthesize := synthesizer.SynthesizeAuthFile(&synthesizer.SynthesisContext{
			Config: cfg, AuthDir: dir, PluginAuthParser: parser, StrictFileParsing: true,
		}, path, data)
		if errSynthesize != nil || len(snapshots) != 1 {
			t.Fatalf("watcher reload: %v", errSynthesize)
		}
		if _, errUpdate := manager.Update(coreauth.WithSkipPersist(context.Background()), snapshots[0]); errUpdate != nil {
			t.Fatal(errUpdate)
		}
		if got := ojson.GetBytes(read().Body.Bytes(), "pool_id").String(); got != poolID {
			t.Fatalf("pool lost after successful assignment and watcher reload: got %q want %q", got, poolID)
		}
		base, _ := manager.GetByID("kiro-account.json")
		refreshed := parser.host.AuthDataToCoreAuth(pluginapi.AuthData{ID: base.ID, Provider: base.Provider,
			StorageJSON: []byte(`{"type":"kiro4cpa","access_token":"renewed-token"}`),
			Metadata:    map[string]any{"access_token": "renewed-token"}}, path, base.FileName)
		if _, errRefresh := manager.UpdateRefreshedAuth(context.Background(), base, refreshed); errRefresh != nil {
			t.Fatal(errRefresh)
		}
		restarted := coreauth.NewManager(store, nil, nil)
		if err := restarted.Load(context.Background()); err != nil {
			t.Fatal(err)
		}
		loaded, ok := restarted.GetByID(base.ID)
		restarted.StopAutoRefresh()
		if !ok || loaded.AccountPoolID() != poolID || loaded.Metadata["access_token"] != "renewed-token" {
			t.Fatalf("membership/token lost after refresh and restart: pool=%q", loaded.AccountPoolID())
		}
	}
}
