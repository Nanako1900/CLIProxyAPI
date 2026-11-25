package executor

import (
	"context"
	"strings"
	"testing"

	"github.com/MoeForge/moego/ojson"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

func TestClaudePerAccountUAOverrideAlignsBillingCCVersion(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		ClaudeHeaderDefaults: config.ClaudeHeaderDefaults{
			UserAgent: "claude-cli/2.1.319 (external, cli)",
		},
	}

	oldAuth := &cliproxyauth.Auth{
		ID:       "claude-old@example.com.json",
		Provider: "claude",
	}
	newAuth := &cliproxyauth.Auth{
		ID:       "claude-new@example.com.json",
		Provider: "claude",
		Attributes: map[string]string{
			"header:User-Agent": "claude-cli/2.1.331 (external, sdk-cli)",
		},
	}

	payload := []byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hello world from test"}]}`)

	oldHeader := claudeCCHFallbackBillingHeader(context.Background(), cfg, oldAuth, payload, "cli")
	if !strings.Contains(oldHeader, "cc_version=2.1.319.") {
		t.Fatalf("old account billing header = %q, want cc_version=2.1.319.*", oldHeader)
	}

	newHeader := claudeCCHFallbackBillingHeader(context.Background(), cfg, newAuth, payload, "cli")
	if !strings.Contains(newHeader, "cc_version=2.1.331.") {
		t.Fatalf("new account billing header = %q, want cc_version=2.1.331.*", newHeader)
	}

	docOld := ojson.NewDoc(payload)
	oldDocHeader := claudeCCHFallbackBillingHeaderDoc(context.Background(), cfg, oldAuth, docOld, "cli")
	if !strings.Contains(oldDocHeader, "cc_version=2.1.319.") {
		t.Fatalf("old account doc billing header = %q, want cc_version=2.1.319.*", oldDocHeader)
	}

	docNew := ojson.NewDoc(payload)
	newDocHeader := claudeCCHFallbackBillingHeaderDoc(context.Background(), cfg, newAuth, docNew, "cli")
	if !strings.Contains(newDocHeader, "cc_version=2.1.331.") {
		t.Fatalf("new account doc billing header = %q, want cc_version=2.1.331.*", newDocHeader)
	}
}
