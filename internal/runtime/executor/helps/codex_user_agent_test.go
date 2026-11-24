package helps

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

func TestApplyCodexUALabHeadersAndWireOrder(t *testing.T) {
	const codexUA = "codex-tui/0.172.0 (Mac OS 27.3.0; arm64) Apple_Terminal/489.11 (codex-tui; 0.172.0)"
	wantOrder := []string{
		"x-codex-beta-features",
		"x-codex-window-id",
		"x-codex-turn-metadata",
		"x-client-request-id",
		"session-id",
		"thread-id",
		"accept",
		"content-type",
		"authorization",
		"originator",
		"user-agent",
		"accept-encoding",
		"host",
		"content-length",
	}

	cfg := &config.Config{
		CodexHeaderDefaults: config.CodexHeaderDefaults{
			UserAgent:      codexUA,
			Originator:     "codex-tui",
			BetaFeatures:   "remote_compaction_v2",
			AcceptEncoding: "zstd,gzip",
			ForceHTTP1:     true,
			HeaderOrder:    wantOrder,
		},
	}

	// Verify ApplyConfiguredUserAgent ignores legacy credential overrides and applies UALab headers.
	req, err := http.NewRequest(http.MethodPost, "http://api.example.invalid/v1/responses", strings.NewReader(`{"model":"gpt-6.2-sol"}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("X-Codex-Window-Id", "win-1")
	req.Header.Set("X-Codex-Turn-Metadata", `{"turn_id":"t1"}`)
	req.Header.Set("X-Client-Request-Id", "req-1")
	req.Header.Set("Session-Id", "sess-1")
	req.Header.Set("Thread-Id", "thread-1")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Connection", "Keep-Alive")

	authWithLegacyUA := &cliproxyauth.Auth{
		Provider: "codex",
		Attributes: map[string]string{
			"header:User-Agent": "legacy-override/1.0",
		},
	}
	ApplyConfiguredUserAgent(context.Background(), req.Header, cfg, authWithLegacyUA, "codex", nil)

	if got := req.Header.Get("User-Agent"); got != codexUA {
		t.Fatalf("User-Agent = %q, want %q", got, codexUA)
	}
	if got := req.Header.Get("Originator"); got != "codex-tui" {
		t.Fatalf("Originator = %q, want codex-tui", got)
	}
	if got := req.Header.Get("X-Codex-Beta-Features"); got != "remote_compaction_v2" {
		t.Fatalf("X-Codex-Beta-Features = %q, want remote_compaction_v2", got)
	}
	if got := req.Header.Get("Accept-Encoding"); got != "zstd,gzip" {
		t.Fatalf("Accept-Encoding = %q, want zstd,gzip", got)
	}
	if got := req.Header.Get("Connection"); got != "" {
		t.Fatalf("Connection = %q, want empty when omitted from HeaderOrder", got)
	}

	// Exercise wire-level HTTP/1.1 header order and exact lowercase header casing via net.Pipe.
	rawCh := make(chan string, 1)
	dialer := &codexPipeDialer{
		onServerConn: func(srv net.Conn) {
			defer func() { _ = srv.Close() }()
			br := bufio.NewReader(srv)
			var raw bytes.Buffer
			for {
				line, errLine := br.ReadString('\n')
				if errLine != nil {
					break
				}
				raw.WriteString(line)
				if line == "\r\n" {
					break
				}
			}
			_, _ = io.CopyN(io.Discard, br, int64(len(`{"model":"gpt-6.2-sol"}`)))
			rawCh <- raw.String()
			_, _ = srv.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\n{}"))
		},
	}

	tr := newCodexOrderedTransportForDialer(dialer, cfg.CodexHeaderDefaults.HeaderOrder, nil)
	client := &http.Client{Transport: tr}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do error = %v", err)
	}
	_ = resp.Body.Close()

	rawHeaders := <-rawCh
	lines := strings.Split(strings.TrimSuffix(rawHeaders, "\r\n\r\n"), "\r\n")
	if len(lines) < 2 || !strings.HasPrefix(lines[0], "POST /v1/responses HTTP/1.1") {
		t.Fatalf("unexpected request line: %q", rawHeaders)
	}
	gotOrder := make([]string, 0, len(lines)-1)
	for _, line := range lines[1:] {
		name, _, ok := strings.Cut(line, ":")
		if !ok {
			t.Fatalf("malformed wire header line %q", line)
		}
		gotOrder = append(gotOrder, name)
	}
	if strings.Join(gotOrder, " -> ") != strings.Join(wantOrder, " -> ") {
		t.Fatalf("wire header order mismatch:\ngot:  %s\nwant: %s\nraw:\n%s",
			strings.Join(gotOrder, " -> "), strings.Join(wantOrder, " -> "), rawHeaders)
	}
}

func TestCodexUnconfiguredDoesNotInjectDefaultOrAuto(t *testing.T) {
	headers := http.Header{}
	ApplyCodexUALabHeaders(headers, &config.Config{})
	if got := headers.Get("User-Agent"); got != "" {
		t.Fatalf("expected empty User-Agent when unconfigured, got %q", got)
	}
	ApplyCodexUALabHeaders(headers, &config.Config{
		CodexHeaderDefaults: config.CodexHeaderDefaults{UserAgent: "auto"},
	})
	if got := headers.Get("User-Agent"); got != "" {
		t.Fatalf("expected auto User-Agent to be ignored for Codex, got %q", got)
	}
	if ResolveCodexUserAgent(&config.Config{CodexHeaderDefaults: config.CodexHeaderDefaults{UserAgent: "auto"}}) != "" {
		t.Fatal("ResolveCodexUserAgent should return empty for auto")
	}
}

func TestDialCodexChatGPTHTTP1TLSNegotiatesHTTP11(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "chatgpt.com"},
		DNSNames:     []string{"chatgpt.com"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv}

	c1, c2 := net.Pipe()
	defer func() { _ = c1.Close(); _ = c2.Close() }()

	alpnCh := make(chan []string, 1)
	go func() {
		srv := tls.Server(c2, &tls.Config{
			Certificates: []tls.Certificate{cert},
			NextProtos:   []string{"h2", "http/1.1"},
			GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
				alpnCh <- append([]string(nil), hello.SupportedProtos...)
				return nil, io.EOF
			},
		})
		_ = srv.Handshake()
		_ = srv.Close()
	}()

	// dialCodexChatGPTHTTP1TLS validates cert against system roots, so handshake
	// will fail cert verification after sending ClientHello — which is enough to
	// inspect the exact ClientHello ALPN list advertised by our uTLS preset.
	_, _ = dialCodexChatGPTHTTP1TLS(context.Background(), c1, "chatgpt.com")
	select {
	case protos := <-alpnCh:
		if len(protos) != 1 || protos[0] != "http/1.1" {
			t.Fatalf("ClientHello ALPN = %v, want [http/1.1]", protos)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for ClientHello ALPN")
	}
}

type codexPipeDialer struct {
	onServerConn func(net.Conn)
}

func (d *codexPipeDialer) Dial(_, _ string) (net.Conn, error) {
	return d.DialContext(context.Background(), "", "")
}

func (d *codexPipeDialer) DialContext(_ context.Context, _, _ string) (net.Conn, error) {
	client, server := net.Pipe()
	go d.onServerConn(server)
	return client, nil
}

func TestAPICallTransportsFailClosedOnInvalidProxyAndIgnoreEnvProxy(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	accepted := make(chan struct{}, 4)
	go func() {
		for {
			conn, errAccept := ln.Accept()
			if errAccept != nil {
				return
			}
			accepted <- struct{}{}
			_ = conn.Close()
		}
	}()

	// Verify strictAPICallDialerForProxy ignores process HTTP_PROXY/HTTPS_PROXY
	// when no proxy is configured, and fails closed when an invalid proxy is given.
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")

	badProxy := "invalid-scheme://127.0.0.1:9999"
	cfg := &config.Config{
		CodexHeaderDefaults: config.CodexHeaderDefaults{
			UserAgent:   "codex-tui/0.172.0 (Mac OS 27.3.0; arm64) Apple_Terminal/489.11 (codex-tui; 0.172.0)",
			HeaderOrder: []string{"user-agent", "host"},
		},
		GeminiHeaderDefaults: config.GeminiHeaderDefaults{
			UserAgent:   "GeminiCLI/0.37.2",
			ForceHTTP1:  true,
			HeaderOrder: []string{"User-Agent", "Host"},
		},
	}

	transports := []struct {
		name string
		rt   http.RoundTripper
	}{
		{name: "codex", rt: NewCodexAPICallTransport(cfg, badProxy)},
		{name: "claude", rt: NewClaudeAPICallTransport(badProxy)},
		{name: "gemini", rt: NewGeminiAPICallTransport(cfg, badProxy)},
	}
	for _, tc := range transports {
		req, errReq := http.NewRequest(http.MethodGet, "https://"+ln.Addr().String()+"/probe", nil)
		if errReq != nil {
			t.Fatalf("%s: new request: %v", tc.name, errReq)
		}
		resp, errDo := tc.rt.RoundTrip(req)
		if errDo == nil {
			_ = resp.Body.Close()
			t.Fatalf("%s: expected invalid proxy to fail closed, got nil error", tc.name)
		}
	}

	select {
	case <-accepted:
		t.Fatal("listener received direct TCP connection after invalid proxy configuration (IP leak)")
	default:
	}

	// Verify strictAPICallDialerForProxy("") connects directly to local listener
	// despite HTTP_PROXY/HTTPS_PROXY pointing to dead port 1.
	directDialer := strictAPICallDialerForProxy("")
	conn, errDirect := directDialer.Dial("tcp", ln.Addr().String())
	if errDirect != nil {
		t.Fatalf("expected direct dialer to ignore env proxy, got error: %v", errDirect)
	}
	_ = conn.Close()
}

func TestResolveClaudeEffectiveUserAgent(t *testing.T) {
	t.Parallel()

	// 1. Fixed config UA wins over default baseline.
	fixedCfg := &config.Config{
		ClaudeHeaderDefaults: config.ClaudeHeaderDefaults{
			UserAgent: "claude-cli/2.1.331 (external, sdk-cli)",
		},
	}
	if got := ResolveClaudeEffectiveUserAgent(fixedCfg, nil); got != "claude-cli/2.1.331 (external, sdk-cli)" {
		t.Fatalf("fixed UA = %q, want claude-cli/2.1.331 (external, sdk-cli)", got)
	}

	// 2. Credential static UA override wins over global fixed config UA.
	authWithOverride := &cliproxyauth.Auth{
		Provider: "claude",
		Attributes: map[string]string{
			"header:User-Agent": "claude-cli/2.1.999 (external, cli)",
		},
	}
	if got := ResolveClaudeEffectiveUserAgent(fixedCfg, authWithOverride); got != "claude-cli/2.1.999 (external, cli)" {
		t.Fatalf("credential override UA = %q, want claude-cli/2.1.999 (external, cli)", got)
	}

	// 3. "auto" never returns literal "auto"; resolves to an actual Claude CLI UA.
	autoCfg := &config.Config{
		ClaudeHeaderDefaults: config.ClaudeHeaderDefaults{
			UserAgent: "auto",
		},
	}
	gotAuto := ResolveClaudeEffectiveUserAgent(autoCfg, nil)
	if gotAuto == "" || strings.EqualFold(gotAuto, "auto") {
		t.Fatalf("auto UA = %q, want resolved Claude CLI UA", gotAuto)
	}
}
