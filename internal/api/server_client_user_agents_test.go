package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoeForge/moego/ojson"
	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

func TestClientUserAgentCaptureAfterAuthentication(t *testing.T) {
	s := newTestServer(t)
	t.Cleanup(func() {
		if err := s.clientUserAgents.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, route := range []struct{ method, path, ua, provider string }{
		{http.MethodGet, "/v1beta/models", "GeminiCLI/0.41.0/gemini-2.5-pro (linux; arm64)", "gemini"},
		{http.MethodGet, "/v1/models", "claude-cli/2.1.319", "claude"},
		{http.MethodPost, "/v1/responses", "codex-tui/0.114.0", "codex"},
		{http.MethodGet, "/backend-api/codex/responses", "codex_cli_rs/0.115.0", "codex"},
	} {
		before := len(s.clientUserAgents.List(route.provider))
		req := httptest.NewRequest(route.method, route.path, strings.NewReader(`{}`))
		req.Header.Set("User-Agent", route.ua)
		rec := httptest.NewRecorder()
		s.engine.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s unauthenticated status=%d", route.path, rec.Code)
		}
		if len(s.clientUserAgents.List(route.provider)) != before {
			t.Fatal("captured unauthenticated client")
		}
		req = httptest.NewRequest(route.method, route.path, strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer test-key")
		req.Header.Set("User-Agent", route.ua)
		rec = httptest.NewRecorder()
		s.engine.ServeHTTP(rec, req)
		found := false
		for _, entry := range s.clientUserAgents.List(route.provider) {
			if entry.Headers["User-Agent"] == route.ua && entry.Observed && entry.Count == 1 {
				found = true
			}
			if entry.Headers["Authorization"] != "" {
				t.Fatal("stored access key")
			}
		}
		if !found {
			t.Fatalf("did not capture authenticated %s %s", route.method, route.path)
		}
	}
	// Simulate executor normalization after the capture middleware.
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("User-Agent", "claude-cli/2.1.318")
	s.captureClientUserAgent(c)
	c.Request.Header.Set("User-Agent", "claude-cli/0.0.1")
	if got := s.clientUserAgents.List("claude")[0].Headers["User-Agent"]; got != "claude-cli/2.1.318" {
		t.Fatalf("captured rewritten UA: %s", got)
	}
}

func TestGeminiCLIStreamObservedInUserAgentCatalog(t *testing.T) {
	const model = "gemini-2.5-flash"
	t.Setenv("MANAGEMENT_PASSWORD", "test-management-key")
	for _, ua := range []string{
		"GeminiCLI-tui/0.70.0/gemini-2.5-flash (darwin; arm64; terminal)",
		"GeminiCLI-tui/0.70.0/gemini-2.5-flash (darwin; arm64; terminal) google-api-nodejs-client/10.9.0",
	} {
		t.Run(ua, func(t *testing.T) {
			s := newTestServer(t)
			t.Cleanup(func() {
				if err := s.clientUserAgents.Close(); err != nil {
					t.Error(err)
				}
			})
			executor := &poolHTTPExecutor{}
			s.handlers.AuthManager.RegisterExecutor(executor)
			credential := &auth.Auth{ID: "gemini-catalog-test", Provider: executor.Identifier(), Status: auth.StatusActive}
			if _, err := s.handlers.AuthManager.Register(t.Context(), credential); err != nil {
				t.Fatal(err)
			}
			models := registry.GetGlobalRegistry()
			models.RegisterClient(credential.ID, credential.Provider, []*registry.ModelInfo{{ID: model}})
			t.Cleanup(func() { models.UnregisterClient(credential.ID) })

			for _, tc := range []struct {
				key    string
				status int
			}{
				{"wrong-key", http.StatusUnauthorized},
				{"test-key", http.StatusOK},
				{"test-key", http.StatusOK},
			} {
				req := httptest.NewRequest(http.MethodPost, "/v1beta/models/"+model+":streamGenerateContent?alt=sse", strings.NewReader(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-Goog-Api-Key", tc.key)
				req.Header.Set("X-Goog-Api-Client", "gl-node/22.0.0")
				req.Header.Set("User-Agent", ua)
				rec := httptest.NewRecorder()
				s.engine.ServeHTTP(rec, req)
				if rec.Code != tc.status {
					t.Fatalf("stream status = %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
				}
			}

			req := httptest.NewRequest(http.MethodGet, "/v8/management/client-user-agents?provider=gemini", nil)
			req.RemoteAddr = "127.0.0.1:1234"
			req.Header.Set("Authorization", "Bearer test-management-key")
			rec := httptest.NewRecorder()
			s.engine.ServeHTTP(rec, req)
			items := ojson.Get(rec.Body.String(), "items").Array()
			if rec.Code != http.StatusOK || len(items) != 1 {
				t.Fatalf("catalog status = %d, want one Gemini sample: %s", rec.Code, rec.Body.String())
			}
			entry := items[0]
			if !entry.Get("observed").Bool() || entry.Get("imported").Bool() || entry.Get("count").Int() != 2 {
				t.Errorf("want two observations and no imports, got %s", entry.Raw)
			}
			if entry.Get("provider").String() != "gemini" || entry.Get("version").String() != "0.70.0" {
				t.Errorf("want Gemini CLI version 0.70.0, got %s", entry.Raw)
			}
			headers := entry.Get("headers").Map()
			if len(headers) != 1 || headers["User-Agent"].String() != ua {
				t.Errorf("want only the exact incoming User-Agent, got %s", entry.Get("headers").Raw)
			}
		})
	}
}

func TestClientUserAgentManagementContract(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "test-management-key")
	s := newTestServer(t)
	t.Cleanup(func() {
		if err := s.clientUserAgents.Close(); err != nil {
			t.Error(err)
		}
	})
	request := func(method, path, body, key string, want int) string {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.RemoteAddr = "127.0.0.1:1234"
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		s.engine.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%s %s status=%d want=%d body=%s", method, path, rec.Code, want, rec.Body.String())
		}
		if want == http.StatusOK && rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("catalog response is cacheable")
		}
		return rec.Body.String()
	}
	const endpoint = "/v8/management/client-user-agents?provider=claude"
	for _, key := range []string{"", "test-key"} {
		request(http.MethodGet, endpoint, "", key, http.StatusUnauthorized)
		request(http.MethodPost, endpoint, `{"headers":"User-Agent: claude-cli/1.2.3"}`, key, http.StatusUnauthorized)
		request(http.MethodDelete, "/v8/management/client-user-agents/test?provider=claude", "", key, http.StatusUnauthorized)
		request(http.MethodGet, endpoint, "", "test-management-key", http.StatusOK)
	}
	key := "test-management-key"
	const gemini = "/v8/management/client-user-agents?provider=gemini"
	request(http.MethodPost, gemini, `{"headers":"User-Agent: codex/1.2.3"}`, key, http.StatusUnprocessableEntity)
	request(http.MethodPost, gemini, `{"headers":"User-Agent: GeminiCLI/1.2.3/gemini-pro (linux; arm64)\nX-Goog-Api-Client: private"}`, key, http.StatusOK)
	result := request(http.MethodGet, gemini, "", key, http.StatusOK)
	if ojson.Get(result, "items.0.version").String() != "1.2.3" || strings.Contains(result, "private") {
		t.Fatalf("unexpected Gemini list: %s", result)
	}
	request(http.MethodGet, "/v8/management/client-user-agents?provider=unknown", "", key, http.StatusBadRequest)
	request(http.MethodPost, endpoint, `{"headers":"User-Agent: codex/1.2.3"}`, key, http.StatusUnprocessableEntity)
	request(http.MethodPost, endpoint, `{"headers":"User-Agent: claude-cli/1.2.3\nUser-Agent: claude-cli/2.0.0"}`, key, http.StatusUnprocessableEntity)
	request(http.MethodPost, endpoint, `{"headers":"`+strings.Repeat("x", 65536)+`"}`, key, http.StatusBadRequest)
	body := request(http.MethodPost, endpoint, `{"headers":"POST /v1/messages HTTP/1.1\nUser-Agent: claude-cli/2.1.319\nAuthorization: Bearer do-not-store\nCookie: hidden\n\nprivate body"}`, key, http.StatusOK)
	if strings.Contains(body, "do-not-store") || strings.Contains(body, "hidden") || strings.Contains(body, "private body") {
		t.Fatal("import leaked unrelated data")
	}
	id := ojson.Get(body, "id").String()
	if id == "" || !ojson.Get(body, "imported").Bool() || ojson.Get(body, "observed").Bool() {
		t.Fatalf("unexpected import: %s", body)
	}
	list := request(http.MethodGet, endpoint, "", key, http.StatusOK)
	if len(ojson.Get(list, "items").Array()) != 1 {
		t.Fatalf("unexpected list: %s", list)
	}
	request(http.MethodDelete, "/v8/management/client-user-agents/"+id+"?provider=claude", "", key, http.StatusNoContent)
	list = request(http.MethodGet, endpoint, "", key, http.StatusOK)
	if len(ojson.Get(list, "items").Array()) != 0 {
		t.Fatal("sample not deleted")
	}
	s.cfgMu.Lock()
	homeConfig := *s.cfg
	homeConfig.Home.Enabled = true
	s.cfg = &homeConfig
	s.cfgMu.Unlock()
	request(http.MethodGet, endpoint, "", key, http.StatusNotFound)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	c.Request.Header.Set("User-Agent", "claude-cli/3.0.0")
	s.captureClientUserAgent(c)
	if len(s.clientUserAgents.List("claude")) != 0 {
		t.Fatal("captured in Home mode")
	}
	home := &Server{}
	home.initClientUserAgents(&config.Config{Home: config.HomeConfig{Enabled: true}})
	if home.clientUserAgents != nil {
		t.Fatal("initialized standalone catalog in Home mode")
	}
}
