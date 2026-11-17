package models

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
)

func TestCodex62ClientCatalog(t *testing.T) {
	for _, model := range []struct {
		id      string
		context int
	}{
		{id: "gpt-6.2-sol", context: 500000},
		{id: "gpt-6.2-astra", context: 272000},
	} {
		t.Run(model.id, func(t *testing.T) {
			info := registry.LookupStaticModelInfoByChannel(model.id, "codex")
			if info == nil || !info.SupportConfigurationUpdate || info.ContextLength != model.context {
				t.Fatalf("missing or inconsistent model capabilities: %+v", info)
			}
			if !registry.CodexClientModelUsesResponsesLite(model.id) {
				t.Fatal("model lost Responses-Lite support")
			}
			response := BuildResponseForClient([]map[string]any{{"id": model.id}}, func(string) []string {
				return []string{"codex"}
			}, false, "0.167.0")
			entries, ok := response["models"].([]map[string]any)
			if !ok || len(entries) != 1 {
				t.Fatalf("expected one client model, got %T", response["models"])
			}
			entry := entries[0]
			if got := intModelValue(entry, "context_window"); got != model.context {
				t.Errorf("context_window = %d, want %d", got, model.context)
			}
			if got := intModelValue(entry, "max_context_window"); got != 872000 {
				t.Errorf("max_context_window = %d, want 872000", got)
			}
			if entry["default_reasoning_level"] != "low" || entry["supports_reasoning_effort_updates"] != true {
				t.Error("client reasoning metadata does not match the official catalog")
			}
			if entry["minimal_client_version"] != "0.166.0" || entry["visibility"] != "list" {
				t.Error("client visibility metadata does not match the official catalog")
			}
		})
	}
}
