package managementasset

import (
	"os"
	"path/filepath"
	"strings"
)

// BundledFilePath pins a source-built panel to the application image. An explicit
// MANAGEMENT_STATIC_PATH keeps its existing behavior and takes precedence.
func BundledFilePath() string {
	if strings.TrimSpace(os.Getenv("MANAGEMENT_STATIC_PATH")) != "" {
		return ""
	}
	if path := strings.TrimSpace(os.Getenv("MANAGEMENT_BUNDLED_PATH")); path != "" {
		return filepath.Clean(path)
	}
	return ""
}
