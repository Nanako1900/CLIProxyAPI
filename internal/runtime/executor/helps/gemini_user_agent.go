package helps

import (
	"context"
	"io"
	"maps"
	"net/http"
	"runtime"
	"strings"

	"github.com/MoeForge/moego/ojson"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/util"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

// GeminiCredentialUserAgent returns the credential override before downstream
// header variables are expanded. The CLI plugin applies file headers last.
func GeminiCredentialUserAgent(auth *cliproxyauth.Auth) string {
	if auth == nil {
		return ""
	}
	attr := geminiHeaderOverride(auth.Attributes, "header:User-Agent", IsUserAgentAttribute)
	if attr != "" && !strings.EqualFold(strings.TrimSpace(auth.Provider), "gemini-cli") {
		return attr
	}
	isUserAgent := func(key string) bool { return strings.EqualFold(strings.TrimSpace(key), "User-Agent") }
	var metadata string
	switch headers := auth.Metadata["headers"].(type) {
	case map[string]string:
		metadata = geminiHeaderOverride(headers, "User-Agent", isUserAgent)
	case map[string]any:
		metadata = geminiHeaderOverride(headers, "User-Agent", isUserAgent)
	}
	if metadata != "" {
		return metadata
	}
	return attr
}

// Existing credentials can contain multiple spellings of one header. Prefer the
// canonical key, followed by the lexicographically first valid alternative.
func geminiHeaderOverride[T any](headers map[string]T, canonical string, matches func(string) bool) string {
	var bestKey, bestValue string
	for key, raw := range headers {
		if !matches(key) {
			continue
		}
		value, ok := any(raw).(string)
		if !ok || !config.ValidUserAgent(value) {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if key == canonical {
			return value
		}
		if bestKey == "" || key < bestKey {
			bestKey, bestValue = key, value
		}
	}
	return bestValue
}

// ResolveGeminiUserAgent returns the configured identity for native Gemini and
// CLI plugin requests. An empty result preserves the channel's own behavior.
func ResolveGeminiUserAgent(ctx context.Context, cfg *config.Config, auth *cliproxyauth.Auth, model string, clientHeaders http.Header) string {
	return resolveGeminiUserAgent(ctx, cfg, auth, model, clientHeaders, nil)
}

func resolveGeminiUserAgent(ctx context.Context, cfg *config.Config, auth *cliproxyauth.Auth, model string, clientHeaders http.Header, req *http.Request) string {
	value := resolveCredentialUserAgent(ctx, GeminiCredentialUserAgent(auth), clientHeaders)
	if value == "" && cfg != nil && config.ValidUserAgent(cfg.GeminiHeaderDefaults.UserAgent) {
		value = strings.TrimSpace(cfg.GeminiHeaderDefaults.UserAgent)
	}
	if strings.EqualFold(value, "auto") {
		if model == "" && req != nil {
			model = geminiRawRequestModel(req)
		}
		return geminiCLIUserAgent(nativeClientVersions.get("gemini"), model)
	}
	return value
}

func geminiCLIUserAgent(version, model string) string {
	platform := runtime.GOOS
	if platform == "windows" {
		platform = "win32"
	}
	arch := runtime.GOARCH
	switch arch {
	case "amd64":
		arch = "x64"
	case "386":
		arch = "x86"
	}
	prefix := "GeminiCLI/" + version + "/"
	suffix := " (" + platform + "; " + arch + "; terminal)"
	model = strings.Trim(model, " ")
	if model == "" || !config.ValidUserAgent(model) || len(prefix)+len(model)+len(suffix) > 1024 {
		model = "unknown"
	}
	return prefix + model + suffix
}

// geminiRawRequestModel inspects a replayable Interactions body without touching
// the request stream. Agent-only calls have no model to report.
func geminiRawRequestModel(req *http.Request) string {
	if req.URL == nil {
		return ""
	}
	if _, tail, ok := strings.Cut(req.URL.Path, "/models/"); ok {
		model, _, _ := strings.Cut(tail, ":")
		return model
	}
	if req.Method != http.MethodPost || !strings.HasSuffix(strings.TrimRight(req.URL.Path, "/"), "/interactions") || req.GetBody == nil {
		return ""
	}
	body, errOpen := req.GetBody()
	if errOpen != nil || body == nil {
		return ""
	}
	defer func() {
		if errClose := body.Close(); errClose != nil {
			log.WithError(errClose).Warn("close Gemini Interactions request body copy")
		}
	}()
	// The model may precede a large prompt. Read at most 64 KiB and accept
	// only a complete JSON string, even when the surrounding body is longer.
	prefix, errRead := io.ReadAll(io.LimitReader(body, 64<<10))
	if errRead != nil {
		return ""
	}
	model := ojson.GetBytesNoCopy(prefix, "model")
	if model.Type != ojson.String || !ojson.Valid(model.Raw) {
		return ""
	}
	return model.String()
}

// ApplyGeminiUserAgent overwrites only the UA, keeping all native headers when
// neither the provider nor the credential configures one.
func ApplyGeminiUserAgent(req *http.Request, cfg *config.Config, auth *cliproxyauth.Auth, model string, clientHeaders http.Header) {
	if ua := resolveGeminiUserAgent(req.Context(), cfg, auth, model, clientHeaders, req); ua != "" {
		setClientUserAgent(req.Header, ua)
	}
}

// ApplyGeminiHeaders applies credential headers while reserving User-Agent for
// the final resolver. Missing and invalid variables preserve the native default.
func ApplyGeminiHeaders(req *http.Request, cfg *config.Config, auth *cliproxyauth.Auth, model string, clientHeaders http.Header) {
	if auth != nil {
		attrs := auth.Attributes
		for key := range attrs {
			if IsUserAgentAttribute(key) {
				attrs = maps.Clone(attrs)
				maps.DeleteFunc(attrs, func(key, _ string) bool { return IsUserAgentAttribute(key) })
				break
			}
		}
		util.ApplyCustomHeadersFromAttrs(req, attrs, clientHeaders)
	}
	ApplyGeminiUserAgent(req, cfg, auth, model, clientHeaders)
}
