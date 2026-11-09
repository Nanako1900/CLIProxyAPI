package helps

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

func accountingIdentity(auth *cliproxyauth.Auth) (string, string) {
	if auth == nil {
		return "", ""
	}
	label := strings.TrimSpace(auth.Label)
	if label == "" && auth.FileName != "" {
		label = filepath.Base(auth.FileName)
	}
	value := func(key string) string {
		if v, ok := auth.Metadata[key].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
		return strings.TrimSpace(auth.Attributes[key])
	}
	identity := ""
	if explicit := value("usage_account_id"); explicit != "" {
		identity = "explicit:" + explicit
	}
	if identity == "" {
		for _, key := range []string{"account_id", "user_id", "sub"} {
			if id := value(key); id != "" {
				identity = key + ":" + id + "\x00" + value("organization_id") + "\x00" + value("project_id")
				break
			}
		}
	}
	if identity == "" {
		if key := value("api_key"); key != "" {
			identity = "key:" + value("base_url") + "\x00" + key
		} else {
			identity = "credential:" + auth.ID + "\x00" + value("email")
		}
	}
	hash := sha256.Sum256([]byte(strings.ToLower(auth.Provider) + "\x00" + identity))
	return fmt.Sprintf("%x", hash), label
}
