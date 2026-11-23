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

func TestGeminiCLISurfaceUserAgents(t *testing.T) {
	const terminal = "/0.70.0/gemini-3.5-flash (darwin; arm64; terminal)"
	const vscode = "CloudCodeVSCode/0.70.0 (aidev_client; os_type=Linux; os_version=6.8.0; arch=x64; host_path=VSCode/1.112.0 > CloudShell/2; proxy_client=geminicli)"
	const authSDK = " google-api-nodejs-client/10.9.0"
	const nestedSurface = "GeminiCLI-tui/0.70.0/gemini-2.5-flash (linux; x64; Corporate (EU))"
	for _, ua := range []string{
		"GeminiCLI-tui" + terminal, "GeminiCLI-tui" + terminal + authSDK,
		"GeminiCLI-cli-command" + terminal, "GeminiCLI-acp" + terminal,
		"GeminiCLI-acp-zed" + terminal, "GeminiCLI-acp-jetbrains" + terminal,
		"GeminiCLI-a2a-server" + terminal, "GeminiCLI" + terminal + authSDK,
		vscode, vscode + authSDK, nestedSurface, nestedSurface + authSDK,
		"GeminiCLI-acp-future-ide" + terminal,
		"GeminiCLI/0.70.0/models/my model (high) (linux; arm64; Corporate (EU (West)))",
	} {
		profile, err := FromHeaders(http.Header{"User-Agent": {ua}, "Authorization": {"Bearer secret"}, "X-Goog-Api-Client": {"gl-node/22"}})
		if err != nil || profile.Provider != "gemini" || profile.Version != "0.70.0" || len(profile.Headers) != 1 || profile.Headers["User-Agent"] != ua {
			t.Errorf("UA %q: profile = %#v, error = %v", ua, profile, err)
		}
	}
}

func TestGeminiCLIRejectsUnrelatedIdentities(t *testing.T) {
	for _, ua := range []string{
		"google-api-nodejs-client/10.9.0", "google-genai-sdk/1.0.0 gl-node/v22.0.0",
		"GeminiCLI-tui-extra/0.70.0/model (linux; x64; terminal)",
		"GeminiCLI-acpx-zed/0.70.0/model (linux; x64; terminal)",
		"GeminiCLI-acp-/0.70.0/model (linux; x64; terminal)",
		"GeminiCLI-tui/0.70.0/model (linux; x64; Corporate (EU)))",
		"GeminiCLI-tui/latest/model", "GeminiCLI-tui/0.70.0/ (linux; x64; terminal)",
		"GeminiCLI-tui/0.70.0/model (linux; x64; terminal) unrelated/1.0.0",
		"CloudCodeVSCode/latest (aidev_client; proxy_client=geminicli)",
		"CloudCodeVSCode/0.70.0 (aidev_client; host_path=VSCode/1.112.0)",
		"CloudCodeVSCode/0.70.0 (aidev_client; proxy_client=geminicli-extra)",
		"CloudCodeVSCode/0.70.0 (aidev_client; other_proxy_client=geminicli)",
		"CloudCodeVSCode/0.70.0 (aidev_client) (proxy_client=geminicli)",
		"CloudCodeVSCode/0.70.0 (aidev_client) other/1.0.0 (proxy_client=geminicli)",
		"Other/0.70.0 (aidev_client; proxy_client=geminicli)",
	} {
		if _, err := FromHeaders(http.Header{"User-Agent": {ua}}); err == nil {
			t.Errorf("accepted %q", ua)
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
