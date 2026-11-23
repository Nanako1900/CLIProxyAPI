package clientua

import (
	"net/http"
	"strings"
	"testing"
)

func TestParseWhitelistsIdentityAndStopsBeforeBody(t *testing.T) {
	raw := "POST /v1/messages HTTP/1.1\r\nAuthorization: Bearer secret\r\nCookie: session=secret\r\nX-Api-Key: secret\r\nuser-agent: claude-cli/2.1.319 (external, cli)\r\nx-stainless-package-version: 0.74.0\r\nX-Stainless-Runtime-Version: v24.3.0\r\nX-Stainless-Os: Linux\r\nX-Stainless-Arch: arm64\r\nX-Stainless-Timeout: 600\r\nX-Forwarded-For: 192.0.2.1\r\n\r\nUser-Agent: codex/99.0.0\r\nprivate prompt"
	profile, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Provider != "claude" || profile.Version != "2.1.319" || len(profile.Headers) != 6 {
		t.Fatalf("unexpected profile: %#v", profile)
	}
	for key, value := range profile.Headers {
		if strings.Contains(value, "secret") || strings.Contains(value, "private") || key == "Authorization" {
			t.Fatalf("retained unrelated header %s", key)
		}
	}
	if _, errBody := Parse("POST / HTTP/1.0\n\nUser-Agent: codex/1.2.3"); errBody == nil {
		t.Fatal("accepted UA from body")
	}
}

func TestGeminiCLIProfileContainsOnlyUserAgent(t *testing.T) {
	const ua = "GeminiCLI/0.41.2-preview.1/gemini-2.5-pro(high) (linux; arm64)"
	profile, err := Parse("User-Agent: " + ua + "\nX-Goog-Api-Client: sdk-private\nX-Stainless-Timeout: 123\nAuthorization: secret")
	if err != nil || profile.Provider != "gemini" || profile.Version != "0.41.2-preview.1" || len(profile.Headers) != 1 || profile.Headers["User-Agent"] != ua {
		t.Fatalf("unexpected Gemini profile: %#v, error: %v", profile, err)
	}
	if _, err := Parse("User-Agent: GeminiCLI/0.41.2/models/model with spaces (linux; arm64; terminal)"); err != nil {
		t.Errorf("model containing spaces: %v", err)
	}
	for _, invalid := range []string{"GeminiCLI/0.41.2/ (linux; arm64)", "GeminiCLI/latest/model", "GeminiCLI/0.41.2", "GeminiCLI/0.41.2/model extra"} {
		if _, err := Parse("User-Agent: " + invalid); err == nil {
			t.Errorf("accepted invalid identity %q", invalid)
		}
	}
}

func TestRecognizedClientFamilies(t *testing.T) {
	for _, family := range []string{"claude-cli", "codex-tui", "codex_cli_rs", "codex_exec", "codex_vscode", "codex", "Codex Desktop"} {
		t.Run(family, func(t *testing.T) {
			p, err := Parse("User-Agent: " + family + "/1.2.3-beta.1 (Linux; x86_64)\nX-Codex-Beta-Features: multi_agent")
			if err != nil {
				t.Fatal(err)
			}
			expected := "codex"
			if family == "claude-cli" {
				expected = "claude"
			}
			if p.Provider != expected || p.Version != "1.2.3-beta.1" {
				t.Fatalf("unexpected identity: %#v", p)
			}
			if expected == "claude" && p.Headers["X-Codex-Beta-Features"] != "" {
				t.Fatal("cross-provider field retained")
			}
		})
	}
}

func TestRejectInvalidHeaders(t *testing.T) {
	for _, raw := range []string{
		"User-Agent: Mozilla/5.0", "User-Agent: auto", "User-Agent: claude-cli/newest",
		"User-Agent: codex/1.2.3\nuser-agent: codex/2.0.0",
		"User-Agent: \nUser-Agent: codex/1.2.3",
		"User-Agent: codex/1.2.3\x00",
		"User-Agent: claude-cli/1.2.3\nX-Stainless-Runtime-Version: invalid",
		"User-Agent: claude-cli/1.2.3\nX-Stainless-Package-Version: 1.2.3\nX-Stainless-Package-Version: 2.0.0",
		"User-Agent: claude-cli/1.2.3\nX-Stainless-Timeout: 0",
		"User-Agent: claude-cli/1.2.3\nX-Stainless-Timeout: 1.5",
		"User-Agent: codex/1.2.3 " + strings.Repeat("x", 1024),
		strings.Repeat("x", MaxImportBytes+1),
	} {
		if _, err := Parse(raw); err == nil {
			t.Errorf("accepted invalid headers: %.100q", raw)
		}
	}
	headers := http.Header{"user-agent": {"codex/1.2.3"}, "User-Agent": {"codex/2.0.0"}}
	if _, err := FromHeaders(headers); err == nil {
		t.Fatal("accepted conflicting case variants")
	}
	if _, err := Parse("User-Agent: codex/1.2.3\nUSER-AGENT: codex/1.2.3"); err != nil {
		t.Fatal(err)
	}
}
