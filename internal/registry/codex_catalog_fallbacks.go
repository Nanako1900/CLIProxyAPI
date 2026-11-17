package registry

import (
	"fmt"

	"github.com/MoeForge/moego/ojson"
)

// MergeCodex62CatalogFallbacks bridges the default remote catalogs until they
// include GPT-6.2. Published remote entries always win, and callers must not
// apply this fallback to explicit custom catalogs.
func MergeCodex62CatalogFallbacks(data, embedded []byte) ([]byte, error) {
	if !ojson.ValidBytes(data) || !ojson.ValidBytes(embedded) {
		return nil, fmt.Errorf("model catalogs must contain valid JSON")
	}
	doc := ojson.NewDoc(data)
	changed := false
	for _, section := range []struct {
		path string
		key  string
	}{
		{path: "codex-team", key: "id"},
		{path: "codex-plus", key: "id"},
		{path: "codex-pro", key: "id"},
		{path: "models", key: "slug"},
	} {
		current := doc.Get(section.path)
		if !current.IsArray() {
			continue
		}
		seen := make(map[string]bool)
		for _, model := range current.Array() {
			seen[model.Get(section.key).String()] = true
		}
		for _, model := range ojson.GetBytes(embedded, section.path).Array() {
			id := model.Get(section.key).String()
			if (id != "gpt-6.2-sol" && id != "gpt-6.2-astra") || seen[id] {
				continue
			}
			if errAppend := doc.SetRaw(section.path+".-1", model.Raw); errAppend != nil {
				return nil, fmt.Errorf("append %s catalog fallback: %w", id, errAppend)
			}
			seen[id] = true
			changed = true
		}
	}
	if !changed {
		return data, nil
	}
	return doc.Bytes(), nil
}
