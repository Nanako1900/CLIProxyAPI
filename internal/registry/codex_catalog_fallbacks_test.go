package registry

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/MoeForge/moego/ojson"
)

func TestCodex62CatalogFallbacksRespectSourcesAndRemoteMetadata(t *testing.T) {
	for _, catalog := range []struct {
		name     string
		embedded []byte
		sections []string
		key      string
		validate func([]byte) error
	}{
		{"general", embeddedModelsJSON, []string{"codex-team", "codex-plus", "codex-pro"}, "id", validateCatalogBytes},
		{"client", embeddedCodexClientModelsJSON, []string{"models"}, "slug", ValidateCodexClientModelsJSON},
	} {
		for _, keepRemoteSol := range []bool{false, true} {
			t.Run(catalog.name+"/remote-sol="+strconv.FormatBool(keepRemoteSol), func(t *testing.T) {
				doc := ojson.NewDoc(catalog.embedded)
				for _, section := range catalog.sections {
					models := doc.Get(section).Array()
					for i := len(models) - 1; i >= 0; i-- {
						id := models[i].Get(catalog.key).String()
						path := section + "." + strconv.Itoa(i)
						switch {
						case id == "gpt-6.2-astra", id == "gpt-6.1-sol", id == "gpt-6.2-sol" && !keepRemoteSol:
							if errDelete := doc.Delete(path); errDelete != nil {
								t.Fatal(errDelete)
							}
						case id == "gpt-6.2-sol":
							if errSet := doc.Set(path+".description", "Updated upstream metadata"); errSet != nil {
								t.Fatal(errSet)
							}
						}
					}
				}
				remote := doc.Bytes()
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					_, _ = w.Write(remote)
				}))
				defer server.Close()
				fetch := catalogFetcherWithSource(catalog.embedded, []string{server.URL}, catalog.validate)
				for _, source := range []string{"", server.URL} {
					data, resolved, errFetch := fetch(context.Background(), source)
					if errFetch != nil {
						t.Fatal(errFetch)
					}
					if resolved != server.URL {
						t.Fatalf("resolved source = %s, want %s", resolved, server.URL)
					}
					if errValidate := catalog.validate(data); errValidate != nil {
						t.Fatal(errValidate)
					}
					if source != "" {
						if !bytes.Equal(data, remote) {
							t.Fatal("explicit custom catalog was changed")
						}
						continue
					}
					for _, section := range catalog.sections {
						counts := make(map[string]int)
						for _, model := range ojson.GetBytes(data, section).Array() {
							id := model.Get(catalog.key).String()
							counts[id]++
							if id == "gpt-6.2-sol" && keepRemoteSol && model.Get("description").String() != "Updated upstream metadata" {
								t.Fatal("embedded fallback replaced remote metadata")
							}
							if id == "gpt-6.2-astra" && catalog.key == "id" && !model.Get("support_configuration_update").Bool() {
								t.Fatal("fallback lost configuration_update capability")
							}
							if id == "gpt-6.2-astra" && catalog.key == "slug" && !model.Get("use_responses_lite").Bool() {
								t.Fatal("fallback lost Responses-Lite metadata")
							}
						}
						if counts["gpt-6.2-sol"] != 1 || counts["gpt-6.2-astra"] != 1 {
							t.Fatalf("%s: missing or duplicated GPT-6.2 entries", section)
						}
						if counts["gpt-6.1-sol"] != 0 {
							t.Fatal("fallback resurrected an unrelated removed model")
						}
					}
					if got, want := ojson.GetBytes(data, "codex-free").Raw, ojson.GetBytes(remote, "codex-free").Raw; got != want {
						t.Fatal("fallback changed free-plan eligibility")
					}
					again, errFallback := MergeCodex62CatalogFallbacks(data, catalog.embedded)
					if errFallback != nil || !bytes.Equal(again, data) {
						t.Fatalf("fallback is not idempotent: %v", errFallback)
					}
				}
			})
		}
	}
}
