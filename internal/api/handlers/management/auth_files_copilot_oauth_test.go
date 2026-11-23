package management

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MoeForge/moego/json"
	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	sdkAuth "github.com/router-for-me/CLIProxyAPI/v8/sdk/auth"
)

func TestCopilotV8DeviceOAuth(t *testing.T) {
	tests := []struct {
		name, grant, wantStatus string
		cancel                  bool
	}{
		{name: "authorized", grant: `{"access_token":"github-fixture"}`, wantStatus: "ok"},
		{name: "denied", grant: `{"error":"access_denied"}`, wantStatus: "error"},
		{name: "cancelled", grant: `{"access_token":"github-fixture"}`, wantStatus: "error", cancel: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			authDir := t.TempDir()
			h := &Handler{cfg: &config.Config{AuthDir: authDir}, tokenStore: sdkAuth.NewFileTokenStore()}
			router := gin.New()
			const base = "/v8/management/oauth"
			router.POST(base+"/auth-url", h.StartOAuthV8)
			router.GET("/status", h.GetAuthStatus)
			router.DELETE(base+"/session", h.CancelAuthSession)
			savedPath := filepath.Join(authDir, "copilot-12345.json")

			tokenRequested := make(chan struct{})
			grantReady, allowGrant := context.WithCancel(t.Context())
			defer allowGrant()
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload string
				switch r.Host + r.URL.Path {
				case "github.com/login/device/code":
					payload = `{"device_code":"device-fixture","user_code":"USER-CODE","verification_uri":"https://github.com/login/device","expires_in":900,"interval":1}`
				case "github.com/login/oauth/access_token":
					if !tt.cancel {
						<-grantReady.Done()
					}
					payload = tt.grant
				case "api.github.com/user":
					payload = `{"id":12345,"login":"alice"}`
				case "api.github.com/copilot_internal/v2/token":
					if tt.cancel {
						close(tokenRequested)
						<-grantReady.Done()
					}
					payload = fmt.Sprintf(`{"token":"copilot-fixture","expires_at":%d}`, time.Now().Add(time.Hour).Unix())
				default:
					t.Errorf("unexpected OAuth request: %s %s", r.Host, r.URL.Path)
					http.NotFound(w, r)
					return
				}
				_, _ = io.WriteString(w, payload)
			}))
			server.Listener = &socksHTTPListener{Listener: server.Listener}
			server.StartTLS()
			t.Cleanup(server.Close)
			proxyURL := "socks5://" + server.Listener.Addr().String()

			// Trust the local fixture for both GitHub hostnames and reject direct connections.
			transport := server.Client().Transport.(*http.Transport).Clone()
			transport.TLSClientConfig.ServerName = server.Certificate().DNSNames[0]
			transport.DialContext = func(_ context.Context, _, addr string) (net.Conn, error) {
				return nil, fmt.Errorf("unexpected direct connection to %s", addr)
			}
			previousTransport := http.DefaultTransport
			http.DefaultTransport = transport
			t.Cleanup(func() { http.DefaultTransport = previousTransport })

			body := fmt.Sprintf(`{"provider":"copilot","proxy_url":%q}`, proxyURL)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, base+"/auth-url", strings.NewReader(body)))
			workerDone := make(chan struct{})
			go func() {
				h.copilotOAuthWorkers.Wait()
				close(workerDone)
			}()
			t.Cleanup(func() { <-workerDone })
			var login struct {
				State     string `json:"state"`
				Flow      string `json:"flow"`
				URL       string `json:"url"`
				UserCode  string `json:"user_code"`
				ExpiresIn int    `json:"expires_in"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &login); err != nil || rec.Code != http.StatusOK ||
				login.State == "" || login.Flow != "device" || login.URL != "https://github.com/login/device" ||
				login.UserCode != "USER-CODE" || login.ExpiresIn != 900 {
				t.Fatalf("unexpected login response (%d): %s", rec.Code, rec.Body)
			}
			t.Cleanup(func() { CancelOAuthSession(login.State) })
			if status := performOAuthStatusRequest(t, router, login.State); status.Status != "wait" {
				t.Fatalf("unexpected pending status: %+v", status)
			}
			if tt.cancel {
				// Cancel with the final upstream response still in flight.
				select {
				case <-tokenRequested:
				case <-workerDone:
					t.Fatal("device login exited before the final token exchange")
				}
				rec = httptest.NewRecorder()
				router.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, base+"/session?state="+login.State, nil))
				if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"cancelled":true`) {
					t.Fatalf("unexpected cancel response (%d): %s", rec.Code, rec.Body)
				}
			}
			allowGrant()
			<-workerDone
			if status := performOAuthStatusRequest(t, router, login.State); status.Status != tt.wantStatus {
				t.Fatalf("unexpected final status: %+v", status)
			}
			data, errRead := os.ReadFile(savedPath)
			if tt.wantStatus == "error" {
				if !os.IsNotExist(errRead) {
					t.Fatalf("failed login should not save credentials: %v", errRead)
				}
				return
			}
			var metadata map[string]any
			if errRead != nil || json.Unmarshal(data, &metadata) != nil || metadata["type"] != "copilot" || metadata["proxy_url"] != proxyURL ||
				metadata["account_id"] != "12345" || metadata["username"] != "alice" ||
				metadata["access_token"] != "copilot-fixture" || metadata["refresh_token"] != "github-fixture" {
				t.Fatalf("Copilot credentials were not persisted correctly: %v", errRead)
			}
		})
	}
}
