package helps

import (
	"context"
	"testing"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
)

func TestAccountingIdentitySurvivesOAuthRefreshAndRename(t *testing.T) {
	a := &cliproxyauth.Auth{ID: "old.json", Provider: "codex", FileName: "old.json", Metadata: map[string]any{"account_id": "principal", "access_token": "old"}}
	first, _ := accountingIdentity(a)
	a.ID = "new.json"
	a.FileName = "new.json"
	a.Metadata["access_token"] = "new"
	second, _ := accountingIdentity(a)
	if first == "" || first != second {
		t.Fatal("OAuth account identity changed after token rotation")
	}
	a.Metadata["account_id"] = "another-principal"
	third, _ := accountingIdentity(a)
	if first == third {
		t.Fatal("different accounts were merged")
	}
}

func TestAccountingFinalPayloadMetadataAndAdditionalModel(t *testing.T) {
	r := NewUsageReporter(context.Background(), "openai", "client-alias", &cliproxyauth.Auth{ID: "auth", Provider: "openai"})
	r.SetTranslatedRequestMetadata([]byte(`{"model":"final-model","enable_thinking":false,"service_tier":"flex"}`), "openai")
	record := r.buildRecord(usage.Detail{Reported: true}, false)
	if record.UpstreamModel != "final-model" || record.PricingParams["enable_thinking"] || record.EffectiveServiceTier != "flex" {
		t.Fatalf("final metadata: %+v", record)
	}
	if _, ok := record.PricingParams["enable_thinking"]; !ok {
		t.Fatal("explicit false parameter lost")
	}
	child, ok := r.buildAdditionalModelRecord("image-model", usage.Detail{OutputTokens: 10})
	if !ok || child.ParentExecutionID != record.RequestID || child.RequestID == record.RequestID || child.UpstreamModel != "image-model" {
		t.Fatalf("additional-model attribution: %+v", child)
	}
}

func TestAccountingExplicitZeroUsage(t *testing.T) {
	for _, detail := range []usage.Detail{ParseOpenAIUsage([]byte(`{"usage":{"prompt_tokens":0,"completion_tokens":0}}`)), ParseClaudeUsage([]byte(`{"usage":{"input_tokens":0,"output_tokens":0}}`)), ParseGeminiUsage([]byte(`{"usageMetadata":{"promptTokenCount":0,"candidatesTokenCount":0}}`))} {
		if !detail.Reported {
			t.Fatal("explicit zero usage became missing")
		}
	}
	if ParseOpenAIUsage([]byte(`{"choices":[]}`)).Reported {
		t.Fatal("missing usage became reported")
	}
	for _, detail := range []usage.Detail{ParseOpenAIUsage([]byte(`{"usage":{}}`)), ParseClaudeUsage([]byte(`{"usage":{}}`)), ParseGeminiUsage([]byte(`{"usageMetadata":{}}`)), ParseInteractionsUsage([]byte(`{"usage":{}}`))} {
		if detail.Reported {
			t.Fatal("empty usage became explicit zero")
		}
	}
}

func TestAccountingExplicitZeroStreamUsage(t *testing.T) {
	for name, parse := range map[string]func([]byte) (usage.Detail, bool){"openai": ParseOpenAIStreamUsage, "claude": ParseClaudeStreamUsage, "gemini": ParseGeminiStreamUsage, "interactions": ParseInteractionsStreamUsage} {
		t.Run(name, func(t *testing.T) {
			payload := map[string]string{"openai": `data: {"usage":{"prompt_tokens":0,"completion_tokens":0}}`, "claude": `data: {"usage":{"input_tokens":0,"output_tokens":0}}`, "gemini": `data: {"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":0,"candidatesTokenCount":0}}`, "interactions": `data: {"usage":{"input_tokens":0,"output_tokens":0}}`}[name]
			detail, ok := parse([]byte(payload))
			if !ok || !detail.Reported {
				t.Fatal("explicit zero stream usage lost")
			}
		})
	}
	var buffer StreamUsageBuffer
	buffer.ObserveGeminiStream([]byte(`data: {"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":0,"candidatesTokenCount":0}}`))
	if detail, ok := buffer.Detail(); !ok || !detail.Reported {
		t.Fatal("stream buffer discarded explicit zero")
	}
}
