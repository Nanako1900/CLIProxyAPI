package handlers

import (
	"context"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/thinking"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/pool"
)

// FilterPoolModels projects the catalog onto credentials in the authenticated pool.
// Unassigned credentials never contribute models to a scoped catalog.
func (h *BaseAPIHandler) FilterPoolModels(c *gin.Context, models []map[string]any) []map[string]any {
	if c == nil || c.Request == nil {
		return models
	}
	return h.filterPoolModelsContext(c.Request.Context(), models)
}

func (h *BaseAPIHandler) filterPoolModelsContext(ctx context.Context, models []map[string]any) []map[string]any {
	scope, scoped := pool.FromContext(ctx)
	if !scoped {
		return models
	}
	out := make([]map[string]any, 0)
	if h == nil || h.AuthManager == nil {
		return out
	}
	allowed := make(map[string]bool)
	for _, a := range h.AuthManager.List() {
		if a.Disabled || a.AccountPoolID() != scope.ID {
			continue
		}
		for _, model := range registry.GetGlobalRegistry().GetModelsForClient(a.ID) {
			if model != nil {
				allowed[model.ID] = true
			}
		}
	}
	for _, model := range models {
		id, _ := model["id"].(string)
		if id == "" {
			id, _ = model["name"].(string)
			id = strings.TrimPrefix(id, "models/")
		}
		if allowed[id] {
			out = append(out, model)
		}
	}
	return out
}

// Resolve auto from the pool catalog before provider routing can consult the global catalog.
func (h *BaseAPIHandler) resolvePoolAutoModel(ctx context.Context, model string) string {
	if _, scoped := pool.FromContext(ctx); !scoped {
		return model
	}
	parsed := thinking.ParseSuffix(model)
	if parsed.ModelName != "auto" {
		return model
	}
	for _, entry := range h.filterPoolModelsContext(ctx, registry.GetGlobalRegistry().GetAvailableModels("openai")) {
		id, _ := entry["id"].(string)
		if id == "" || h.validateImageOnlyModel(id, false) != nil || h.validateSpeechOnlyModel(id, false) != nil {
			continue
		}
		if parsed.HasSuffix {
			return id + "(" + parsed.RawSuffix + ")"
		}
		return id
	}
	return "pool-model-unavailable"
}
