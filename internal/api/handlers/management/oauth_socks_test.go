package management

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoeForge/moego/ojson"
	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
)

// socksHTTPListener terminates SOCKS negotiation before the fixture HTTP server
// receives a request. The handler decides how to forward or fail that request.
type socksHTTPListener struct {
	net.Listener
	credentials string
}

func (l *socksHTTPListener) Accept() (net.Conn, error) {
	for {
		conn, errAccept := l.Listener.Accept()
		if errAccept != nil {
			return nil, errAccept
		}
		if errHandshake := l.handshake(conn); errHandshake == nil {
			return conn, nil
		}
		_ = conn.Close()
	}
}

func (l *socksHTTPListener) handshake(conn net.Conn) error {
	header := make([]byte, 2)
	if _, errRead := io.ReadFull(conn, header); errRead != nil || header[0] != 5 {
		return fmt.Errorf("invalid SOCKS greeting")
	}
	methods := make([]byte, int(header[1]))
	if _, errRead := io.ReadFull(conn, methods); errRead != nil {
		return errRead
	}
	method := byte(0)
	if l.credentials != "" {
		method = 2
	}
	if _, errWrite := conn.Write([]byte{5, method}); errWrite != nil {
		return errWrite
	}
	if method == 2 {
		if _, errRead := io.ReadFull(conn, header); errRead != nil || header[0] != 1 {
			return fmt.Errorf("invalid SOCKS authentication")
		}
		user := make([]byte, int(header[1]))
		if _, errRead := io.ReadFull(conn, user); errRead != nil {
			return errRead
		}
		if _, errRead := io.ReadFull(conn, header[:1]); errRead != nil {
			return errRead
		}
		password := make([]byte, int(header[0]))
		if _, errRead := io.ReadFull(conn, password); errRead != nil {
			return errRead
		}
		if string(user)+":"+string(password) != l.credentials {
			_, _ = conn.Write([]byte{1, 1})
			return fmt.Errorf("SOCKS authentication rejected")
		}
		if _, errWrite := conn.Write([]byte{1, 0}); errWrite != nil {
			return errWrite
		}
	}
	request := make([]byte, 4)
	if _, errRead := io.ReadFull(conn, request); errRead != nil || request[0] != 5 || request[1] != 1 {
		return fmt.Errorf("invalid SOCKS connect request")
	}
	var size int
	switch request[3] {
	case 1:
		size = 4
	case 4:
		size = 16
	case 3:
		if _, errRead := io.ReadFull(conn, header[:1]); errRead != nil {
			return errRead
		}
		size = int(header[0])
	default:
		return fmt.Errorf("invalid SOCKS address")
	}
	if _, errRead := io.ReadFull(conn, make([]byte, size+2)); errRead != nil {
		return errRead
	}
	_, errWrite := conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
	return errWrite
}

func newOAuthSOCKSTestServer(t *testing.T, handler http.Handler, credentials string) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.URL.Scheme, r.URL.Host = "http", r.Host
		handler.ServeHTTP(w, r)
	}))
	server.Listener = &socksHTTPListener{Listener: server.Listener, credentials: credentials}
	server.Start()
	server.URL = strings.Replace(server.URL, "http://", "socks5://", 1)
	t.Cleanup(server.Close)
	return server
}

func TestStartOAuthV8RequiresSOCKSBeforeProviderInitialization(t *testing.T) {
	h := NewHandlerWithoutConfigFilePath(nil, nil)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/start", h.StartOAuthV8)
	for _, provider := range []string{"codex", "claude", "antigravity", "kimi", "kimi-ai", "xai", "devin", "meta", "custom-plugin"} {
		for _, value := range []string{"", "http://user:secret@localhost:8080", "https://localhost:8080", "direct", "socks5://localhost", "socks5://localhost:0"} {
			body := ojson.NewDocString(`{}`)
			if errSet := body.Set("provider", provider); errSet != nil {
				t.Fatal(errSet)
			}
			if errSet := body.Set("proxy_url", value); errSet != nil {
				t.Fatal(errSet)
			}
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/start", strings.NewReader(body.String())))
			if rec.Code != http.StatusBadRequest || strings.Contains(rec.Body.String(), "secret") {
				t.Fatalf("provider %s: status %d, body %s", provider, rec.Code, rec.Body.String())
			}
		}
	}
	for _, value := range []string{"socks5://localhost:1080", "socks5h://user:pass@[::1]:1080"} {
		if errValidate := validateNewOAuthSOCKSProxy(value); errValidate != nil {
			t.Fatalf("valid SOCKS proxy rejected: %v", errValidate)
		}
	}
}

func TestOAuthRecoveryAndSIWCUseAccountProxy(t *testing.T) {
	for _, tc := range []struct {
		name, proxyURL string
		metadataOnly   bool
	}{
		{"account_socks", "socks5h://user:fixture-password@account.example.test:1080", false},
		{"metadata_socks", "socks5h://user:fixture-password@account.example.test:1080", true},
		{"existing_http", "http://account.example.test:8080", false},
		{"existing_direct", "direct", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := setupSIWCFlow(t, false)
			fixture.h.cfg.ProxyURL = "http://global.example.test:8080"
			proxyURL := tc.proxyURL
			auth := fixture.auth.Clone()
			if tc.metadataOnly {
				auth.Metadata["proxy_url"] = proxyURL
			} else {
				auth.ProxyURL = proxyURL
			}
			if _, errUpdate := fixture.manager.Update(context.Background(), auth); errUpdate != nil {
				t.Fatal(errUpdate)
			}
			var recoveryProxy, bindingProxy string
			originalFactory := newCodexOAuthService
			newCodexOAuthService = func(cfg *config.Config) codexOAuthService {
				recoveryProxy = cfg.ProxyURL
				return &proxyTestCodexOAuthService{}
			}
			t.Cleanup(func() { newCodexOAuthService = originalFactory })
			fixture.router.POST("/start", fixture.h.StartOAuthV8)
			rec := httptest.NewRecorder()
			fixture.router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/start", strings.NewReader(`{"name":"codex.json"}`)))
			if rec.Code != http.StatusOK {
				t.Fatalf("recovery status %d: %s", rec.Code, rec.Body.String())
			}
			state := ojson.Get(rec.Body.String(), "state").String()
			if recoveryProxy != proxyURL || GetOAuthSessionProxyURL(state) != proxyURL {
				t.Fatal("recovery did not retain the account proxy")
			}
			// Finish the callback worker before this fixture restores the global store.
			if _, errCallback := WriteOAuthCallbackFileForPendingSession(fixture.h.cfg.AuthDir, "codex", state, "", "access_denied"); errCallback != nil {
				t.Fatal(errCallback)
			}
			if status := waitForOAuthStatus(t, fixture.router, state); status.Status != "error" {
				t.Fatalf("denied callback status = %#v", status)
			}
			fixture.h.siwcOAuthFactory = func(cfg *config.Config) siwcOAuthService {
				bindingProxy = cfg.ProxyURL
				return fixture.service
			}
			fixture.start(t)
			if bindingProxy != proxyURL || fixture.h.cfg.ProxyURL != "http://global.example.test:8080" {
				t.Fatal("SIWC did not isolate the account proxy from the global configuration")
			}
		})
	}
}
