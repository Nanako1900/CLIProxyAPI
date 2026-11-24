package management

import (
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/logging"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

const (
	defaultAuthErrorsLimit = 50
	maxAuthErrorsLimit     = 50
)

// GetAuthFileErrors returns the current error state, per-model errors/cooldowns,
// and persisted recent failure history for a specific credential.
func (h *Handler) GetAuthFileErrors(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if h == nil || h.authManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "core auth manager unavailable"})
		return
	}
	name := strings.TrimSpace(c.Query("name"))
	authIndex := strings.TrimSpace(c.Query("auth_index"))
	if name == "" && authIndex == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name or auth_index is required"})
		return
	}
	limit := defaultAuthErrorsLimit
	if rawLimit := strings.TrimSpace(c.Query("limit")); rawLimit != "" {
		parsed, errParse := strconv.Atoi(rawLimit)
		if errParse != nil || parsed <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "limit must be a positive integer"})
			return
		}
		if parsed > maxAuthErrorsLimit {
			parsed = maxAuthErrorsLimit
		}
		limit = parsed
	}

	auth, ok := h.lookupAuthFile(name, authIndex)
	if !ok || auth == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": errAuthFileNotFound.Error()})
		return
	}

	ctx := c.Request.Context()
	now := time.Now().UTC()
	unavailable, status, statusMessage, nextRetryAfter := reconcileAuthFileCooldownState(auth, now)
	if statusMessage != "" {
		statusMessage = coreauth.SanitizeRenewalMessage(statusMessage)
	}
	fileName := strings.TrimSpace(auth.FileName)
	if fileName == "" {
		fileName = auth.ID
	}

	resp := gin.H{
		"observed_at":      now,
		"auth_id":          auth.ID,
		"auth_index":       lockedAuthIndex(auth),
		"name":             fileName,
		"provider":         strings.TrimSpace(auth.Provider),
		"status":           status,
		"status_message":   statusMessage,
		"disabled":         auth.Disabled,
		"unavailable":      unavailable,
		"requires_sign_in": auth.RequiresSignIn,
		"success":          auth.Success,
		"failed":           auth.Failed,
	}
	if email := authEmail(auth); email != "" {
		resp["email"] = email
	}
	if !nextRetryAfter.IsZero() {
		resp["next_retry_after"] = nextRetryAfter.UTC()
	}
	if auth.RequiresSignIn {
		resp["next_action"] = "sign_in"
		resp["recovery_hint"] = "Re-authenticate this account on this proxy instance via OAuth login; signing in on another machine or a client's separate direct login does not update this instance."
	}
	if auth.Metadata != nil {
		if appeal, okAppeal := auth.Metadata["tos_appeal_url"].(string); okAppeal && strings.TrimSpace(appeal) != "" {
			resp["tos_appeal_url"] = strings.TrimSpace(appeal)
		}
		if appealText, okAppealText := auth.Metadata["tos_appeal_url_text"].(string); okAppealText && strings.TrimSpace(appealText) != "" {
			resp["tos_appeal_url_text"] = strings.TrimSpace(appealText)
		}
	}
	if auth.LastError != nil {
		resp["last_error"] = gin.H{
			"code":        strings.TrimSpace(auth.LastError.Code),
			"http_status": auth.LastError.HTTPStatus,
			"message":     logging.SafeDetailForErrorDisplay(auth.LastError.Message, 0),
			"retryable":   auth.LastError.Retryable,
		}
	}
	if auth.RefreshError != nil {
		resp["refresh_error"] = gin.H{
			"code":        strings.TrimSpace(auth.RefreshError.Code),
			"http_status": auth.RefreshError.HTTPStatus,
			"message":     logging.SafeDetailForErrorDisplay(auth.RefreshError.Message, 0),
		}
	}
	if !h.authManager.HomeEnabled() {
		resp["cooldowns"] = coreauth.CooldownSnapshotForAuth(auth, now)
	} else {
		resp["cooldowns"] = nil
	}

	resp["model_errors"] = buildModelErrorsPayload(auth, now)

	records := h.authManager.AuthErrorHistory(ctx, auth.ID, limit)
	logFileNames := h.availableRequestLogFiles()
	recentPayload := make([]gin.H, 0, len(records))
	for _, rec := range records {
		item := gin.H{
			"id":        rec.ID,
			"timestamp": rec.Timestamp.UTC(),
			"phase":     rec.Phase,
			"message":   rec.Message,
		}
		if rec.RequestID != "" {
			item["request_id"] = rec.RequestID
			if hasMatchingRequestLog(logFileNames, rec.RequestID) {
				item["has_request_log"] = true
			}
		}
		if rec.Provider != "" {
			item["provider"] = rec.Provider
		}
		if rec.Model != "" {
			item["model"] = rec.Model
		}
		if rec.RouteModel != "" {
			item["route_model"] = rec.RouteModel
		}
		if rec.HTTPStatus > 0 {
			item["http_status"] = rec.HTTPStatus
		}
		if rec.Code != "" {
			item["code"] = rec.Code
		}
		if rec.Retryable {
			item["retryable"] = true
		}
		if rec.CredentialScope {
			item["credential_scope"] = true
		}
		if rec.CooldownUntil != nil && !rec.CooldownUntil.IsZero() {
			item["cooldown_until"] = rec.CooldownUntil.UTC()
		}
		recentPayload = append(recentPayload, item)
	}
	resp["recent_errors"] = recentPayload
	resp["recent_error_count"] = len(recentPayload)

	c.JSON(http.StatusOK, resp)
}

// ClearAuthFileErrors clears persisted recent failure records for a specific credential.
func (h *Handler) ClearAuthFileErrors(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if h == nil || h.authManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "core auth manager unavailable"})
		return
	}
	name := strings.TrimSpace(c.Query("name"))
	authIndex := strings.TrimSpace(c.Query("auth_index"))
	if name == "" && authIndex == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name or auth_index is required"})
		return
	}
	auth, ok := h.lookupAuthFile(name, authIndex)
	if !ok || auth == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": errAuthFileNotFound.Error()})
		return
	}
	h.authManager.ClearAuthErrorHistory(c.Request.Context(), auth.ID)
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func buildModelErrorsPayload(auth *coreauth.Auth, now time.Time) []gin.H {
	if auth == nil || len(auth.ModelStates) == 0 {
		return []gin.H{}
	}
	models := make([]string, 0, len(auth.ModelStates))
	for model := range auth.ModelStates {
		models = append(models, model)
	}
	sort.Strings(models)

	result := make([]gin.H, 0, len(models))
	for _, model := range models {
		state := auth.ModelStates[model]
		if state == nil {
			continue
		}
		hasErr := state.LastError != nil || strings.TrimSpace(state.StatusMessage) != "" || state.Status == coreauth.StatusError
		blocked := isModelStateBlocked(state, now)
		if !hasErr && !blocked {
			continue
		}
		item := gin.H{
			"model":       model,
			"status":      state.Status,
			"unavailable": blocked,
		}
		if msg := strings.TrimSpace(state.StatusMessage); msg != "" {
			item["status_message"] = logging.SafeDetailForErrorDisplay(msg, 0)
		}
		if state.LastError != nil {
			if code := strings.TrimSpace(state.LastError.Code); code != "" {
				item["code"] = code
			}
			if state.LastError.HTTPStatus > 0 {
				item["http_status"] = state.LastError.HTTPStatus
			}
			if detail := logging.SafeDetailForErrorDisplay(state.LastError.Message, 0); detail != "" {
				item["message"] = detail
			}
		}
		nextRetry := state.NextRetryAfter
		if state.Quota.Exceeded && state.Quota.NextRecoverAt.After(nextRetry) {
			nextRetry = state.Quota.NextRecoverAt
		}
		if !nextRetry.IsZero() && nextRetry.After(now) {
			item["next_retry_after"] = nextRetry.UTC()
		}
		if reason := strings.TrimSpace(state.Quota.Reason); reason != "" {
			item["quota_reason"] = reason
		}
		if !state.UpdatedAt.IsZero() {
			item["updated_at"] = state.UpdatedAt.UTC()
		}
		result = append(result, item)
	}
	return result
}

func (h *Handler) availableRequestLogFiles() []string {
	if h == nil || h.cfg == nil {
		return nil
	}
	dir := strings.TrimSpace(h.logDirectory())
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry == nil || entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".log") {
			names = append(names, name)
		}
	}
	return names
}

func hasMatchingRequestLog(logFileNames []string, requestID string) bool {
	shortID := strings.TrimSpace(logging.ShortRequestID(requestID))
	if shortID == "" || len(logFileNames) == 0 {
		return false
	}
	suffix := "-" + shortID + ".log"
	for _, name := range logFileNames {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}
