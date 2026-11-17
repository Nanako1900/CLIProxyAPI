package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MoeForge/moego/ojson"
)

func TestMergeCatalogFilesPreservesFallbackBeforeOverwriting(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "remote.json")
	output := filepath.Join(dir, "shipped.json")
	for path, contents := range map[string]string{
		input:  `{"models":[{"slug":"gpt-6.2-sol","description":"remote"}]}`,
		output: `{"models":[{"slug":"gpt-6.2-sol","description":"old"},{"slug":"gpt-6.2-astra","description":"fallback"}]}`,
	} {
		if errWrite := os.WriteFile(path, []byte(contents), 0600); errWrite != nil {
			t.Fatal(errWrite)
		}
	}
	if errMerge := mergeCatalogFiles(input, output, output); errMerge != nil {
		t.Fatal(errMerge)
	}
	data, errRead := os.ReadFile(output)
	if errRead != nil {
		t.Fatal(errRead)
	}
	if ojson.GetBytes(data, "models.#").Int() != 2 ||
		ojson.GetBytes(data, "models.0.description").String() != "remote" ||
		ojson.GetBytes(data, "models.1.slug").String() != "gpt-6.2-astra" {
		t.Fatalf("unexpected merged catalog: %s", data)
	}
}

func TestMergeCatalogFilesRejectsInvalidInputWithoutReplacingOutput(t *testing.T) {
	for _, invalidFallback := range []bool{false, true} {
		dir := t.TempDir()
		input := filepath.Join(dir, "remote.json")
		fallback := filepath.Join(dir, "shipped.json")
		output := filepath.Join(dir, "output.json")
		inputData, fallbackData := `{"models":[]}`, `{"models":[]}`
		if invalidFallback {
			fallbackData = "invalid"
		} else {
			inputData = "invalid"
		}
		for path, contents := range map[string]string{input: inputData, fallback: fallbackData, output: "previous catalog"} {
			if errWrite := os.WriteFile(path, []byte(contents), 0600); errWrite != nil {
				t.Fatal(errWrite)
			}
		}
		if errMerge := mergeCatalogFiles(input, fallback, output); errMerge == nil {
			t.Fatal("accepted invalid catalog")
		}
		data, errRead := os.ReadFile(output)
		if errRead != nil || string(data) != "previous catalog" {
			t.Fatalf("failed refresh changed output: %s, %v", data, errRead)
		}
	}
}
