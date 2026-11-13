package synthesizer

import (
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
)

func TestAccountPoolMembershipSynthesizedForEveryConfigProvider(t *testing.T) {
	empty := ""
	cfg := &config.Config{
		GeminiKey:           []config.GeminiKey{{APIKey: "gemini", PoolID: "basic"}},
		InteractionsKey:     []config.GeminiKey{{APIKey: "interactions", PoolID: "basic"}},
		ClaudeKey:           []config.ClaudeKey{{APIKey: "claude", PoolID: "basic"}},
		CodexKey:            []config.CodexKey{{APIKey: "codex", PoolID: "basic"}},
		XAIKey:              []config.XAIKey{{APIKey: "xai", PoolID: "basic"}},
		MetaKey:             []config.MetaKey{{APIKey: "meta", PoolID: "basic"}},
		VertexCompatAPIKey:  []config.VertexCompatKey{{APIKey: "vertex", PoolID: "basic", BaseURL: "https://example.invalid"}},
		OpenAICompatibility: []config.OpenAICompatibility{{Name: "custom", BaseURL: "https://example.invalid", PoolID: "basic", APIKeyEntries: []config.OpenAICompatibilityAPIKey{{APIKey: "inherited"}, {APIKey: "unassigned", PoolID: &empty}}}},
	}
	ctx := &SynthesisContext{Config: cfg, Now: time.Now(), IDGenerator: NewStableIDGenerator()}
	records, err := NewConfigSynthesizer().Synthesize(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 9 {
		t.Fatalf("records=%d want=9", len(records))
	}
	for _, a := range records {
		want := "basic"
		if a.Attributes["api_key"] == "unassigned" {
			want = ""
		}
		if a.AccountPoolID() != want {
			t.Fatalf("%s pool=%q want=%q", a.Provider, a.AccountPoolID(), want)
		}
	}
}
