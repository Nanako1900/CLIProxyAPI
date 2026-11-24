package helps

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

type geminiPipeDialer struct {
	serverConnCh chan net.Conn
}

func (d *geminiPipeDialer) Dial(network, addr string) (net.Conn, error) {
	c1, c2 := net.Pipe()
	d.serverConnCh <- c2
	return c1, nil
}

func TestApplyGeminiUALabProfileAndWireOrder(t *testing.T) {
	const agyUA = "google-genai-sdk/1.79.1 gl-go/go1.28-20261102-RC01 cl/1012564305 +298f53e483 X:fieldtrack,boringcrypto,nogolistexportnewformat"
	wantOrder := []string{
		"Host",
		"User-Agent",
		"Content-Length",
		"Content-Type",
		"X-Goog-Api-Client",
		"X-Goog-Api-Key",
		"Accept-Encoding",
	}

	cfg := &config.Config{
		GeminiHeaderDefaults: config.GeminiHeaderDefaults{
			UserAgent:      agyUA,
			APIClient:      agyUA,
			AcceptEncoding: "gzip",
			HeaderOrder:    wantOrder,
			ForceHTTP1:     true,
		},
	}

	authWithLegacyUA := &cliproxyauth.Auth{
		Attributes: map[string]string{
			"header:User-Agent":        "legacy-ignored/1.0",
			"header:X-Goog-Api-Client": "legacy-ignored/1.0",
			"header:X-Custom-Route":    "keep-me",
		},
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://api.meowapi.dev/v1beta/models/gemini-4-neon:streamGenerateContent?alt=sse", bytes.NewReader([]byte(`{"contents":[]}`)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", "test-key-123")
	ApplyGeminiHeaders(req, cfg, authWithLegacyUA, "gemini-4-neon", nil)

	if req.Header.Get("User-Agent") != agyUA {
		t.Fatalf("unexpected User-Agent: %q", req.Header.Get("User-Agent"))
	}
	if req.Header.Get("X-Goog-Api-Client") != agyUA {
		t.Fatalf("unexpected X-Goog-Api-Client: %q", req.Header.Get("X-Goog-Api-Client"))
	}
	if req.Header.Get("X-Custom-Route") != "keep-me" {
		t.Fatalf("expected non-identity header preserved, got %q", req.Header.Get("X-Custom-Route"))
	}
	req.Header.Del("X-Custom-Route")

	// Verify exact HTTP/1.1 wire header order & casing via httpwire.NewOrderedRequestConn
	dialer := &geminiPipeDialer{serverConnCh: make(chan net.Conn, 1)}
	tr := newGeminiOrderedTransportForDialer(dialer, wantOrder, nil)
	defer tr.CloseIdleConnections()

	rawHeadCh := make(chan string, 1)
	go func() {
		srvConn := <-dialer.serverConnCh
		defer srvConn.Close()
		br := bufio.NewReader(srvConn)
		var lines []string
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				break
			}
			trimmed := strings.TrimRight(line, "\r\n")
			if trimmed == "" {
				break
			}
			lines = append(lines, trimmed)
		}
		_, _ = io.CopyN(io.Discard, br, int64(len(`{"contents":[]}`)))
		rawHeadCh <- strings.Join(lines, "\n")
		_, _ = io.WriteString(srvConn, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\n{}")
	}()

	client := &http.Client{Transport: tr}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do failed: %v", err)
	}
	_, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	rawHead := <-rawHeadCh
	lines := strings.Split(rawHead, "\n")
	if len(lines) < 2 {
		t.Fatalf("unexpected raw wire request: %q", rawHead)
	}
	var gotOrder []string
	for _, line := range lines[1:] {
		name, _, ok := strings.Cut(line, ":")
		if ok {
			gotOrder = append(gotOrder, name)
		}
	}
	if strings.Join(gotOrder, ",") != strings.Join(wantOrder, ",") {
		t.Fatalf("wire header order mismatch:\ngot : %v\nwant: %v\nraw:\n%s", gotOrder, wantOrder, rawHead)
	}
}
