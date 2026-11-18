package management

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoeForge/moego/ojson"
	"github.com/gin-gonic/gin"
)

func requestOAuthProxyIP(t *testing.T, endpoint, proxyURL string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := ojson.NewDocString(`{}`)
	if errSet := body.Set("proxy_url", proxyURL); errSet != nil {
		t.Fatal(errSet)
	}
	c.Request = httptest.NewRequest(http.MethodPost, "/oauth/proxy-ip", strings.NewReader(body.String()))
	c.Request.Header.Set("Authorization", "Bearer management-secret")
	c.Request.Header.Set("Cookie", "sensitive=value")
	checkOAuthProxyIP(c, endpoint)
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("IP check must not be cached")
	}
	return rec
}

func TestOAuthProxyIPThroughSOCKS(t *testing.T) {
	for _, tc := range []struct{ scheme, ip string }{{"socks5", "203.0.113.5"}, {"socks5h", "2001:db8::5"}} {
		t.Run(tc.scheme, func(t *testing.T) {
			proxy := newOAuthSOCKSTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.Host != "check.invalid" || r.URL.RequestURI() != "/" || r.Header.Get("Authorization") != "" || r.Header.Get("Proxy-Authorization") != "" || r.Header.Get("Cookie") != "" {
					t.Error("expected an independent GET to the IP service")
				}
				_, _ = w.Write([]byte(tc.ip + "\n"))
			}), "user:proxy-secret")
			proxyURL := strings.Replace(proxy.URL, "socks5://", tc.scheme+"://user:proxy-secret@", 1)
			rec := requestOAuthProxyIP(t, "http://check.invalid", proxyURL)
			if rec.Code != http.StatusOK || ojson.Get(rec.Body.String(), "ip").String() != tc.ip || strings.Contains(rec.Body.String(), "secret") {
				t.Fatalf("unexpected check result: %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestOAuthProxyIPFailsClosed(t *testing.T) {
	// This service would succeed if a failed SOCKS handshake fell back to direct.
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("unexpected direct request")
		_, _ = w.Write([]byte("203.0.113.1"))
	}))
	t.Cleanup(direct.Close)
	proxy := newOAuthSOCKSTestServer(t, http.NotFoundHandler(), "user:correct")
	wrong := strings.Replace(proxy.URL, "://", "://user:wrong-secret@", 1)
	for _, tc := range []struct {
		proxyURL string
		want     int
	}{{"", 400}, {"direct", 400}, {"http://user:wrong-secret@host:8080", 400}, {"socks5://host", 400}, {wrong, 502}} {
		rec := requestOAuthProxyIP(t, direct.URL, tc.proxyURL)
		if rec.Code != tc.want || strings.Contains(rec.Body.String(), "wrong-secret") || ojson.Get(rec.Body.String(), "ip").Exists() {
			t.Fatalf("unexpected failure response: %d %s", rec.Code, rec.Body.String())
		}
	}
}

func TestOAuthProxyIPRejectsInvalidServiceResponses(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"service error", "203.0.113.2", 503}, {"not an IP", "<html>untrusted</html>", 200},
		{"private IP", "10.0.0.1", 200}, {"oversized", "203.0.113.1" + strings.Repeat(" ", 64), 200},
		{"redirect", "", 302},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proxy := newOAuthSOCKSTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/" {
					t.Error("unexpected redirect follow")
				}
				w.Header().Set("Location", "/other")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}), "")
			rec := requestOAuthProxyIP(t, "http://check.invalid", proxy.URL)
			if rec.Code != http.StatusBadGateway || strings.Contains(rec.Body.String(), "untrusted") || ojson.Get(rec.Body.String(), "ip").Exists() {
				t.Fatalf("unexpected service failure: %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}
