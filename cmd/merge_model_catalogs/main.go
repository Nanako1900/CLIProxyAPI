// Command merge_model_catalogs preserves shipped GPT-6.2 entries when refreshing
// build-time catalogs from an upstream repository that does not include them yet.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
)

func main() {
	input := flag.String("file", "", "Downloaded model catalog")
	fallback := flag.String("fallback", "", "Shipped model catalog")
	output := flag.String("output", "", "Merged model catalog destination")
	flag.Parse()
	if *input == "" || *fallback == "" || *output == "" {
		fmt.Fprintln(os.Stderr, "error: --file, --fallback, and --output are required")
		os.Exit(2)
	}
	if errMerge := mergeCatalogFiles(*input, *fallback, *output); errMerge != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", errMerge)
		os.Exit(1)
	}
}

func mergeCatalogFiles(inputPath, fallbackPath, outputPath string) error {
	data, errRead := os.ReadFile(inputPath)
	if errRead != nil {
		return fmt.Errorf("read downloaded catalog: %w", errRead)
	}
	fallback, errFallback := os.ReadFile(fallbackPath)
	if errFallback != nil {
		return fmt.Errorf("read shipped catalog: %w", errFallback)
	}
	merged, errMerge := registry.MergeCodex62CatalogFallbacks(data, fallback)
	if errMerge != nil {
		return errMerge
	}
	if errWrite := os.WriteFile(outputPath, merged, 0644); errWrite != nil {
		return fmt.Errorf("write merged catalog: %w", errWrite)
	}
	return nil
}
