package auth

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/kvstore"
	internallogging "github.com/router-for-me/CLIProxyAPI/v8/internal/logging"
)

const (
	authErrorHistoryMaxPerAuth = 50
	authErrorMessageMaxRunes   = 4096
	authErrorSummaryMaxRunes   = 300
	authErrorHistoryKVPrefix   = "auth:error-history:v1:"
	authErrorHistoryTTL        = 30 * 24 * time.Hour
)

var errErrorHistoryStoreInactive = errors.New("auth error history store inactive")

// AuthErrorRecord represents a single persisted failure attributed to a specific credential.
type AuthErrorRecord struct {
	ID              string     `json:"id"`
	Timestamp       time.Time  `json:"timestamp"`
	RequestID       string     `json:"request_id,omitempty"`
	Phase           string     `json:"phase"`
	Provider        string     `json:"provider,omitempty"`
	Model           string     `json:"model,omitempty"`
	RouteModel      string     `json:"route_model,omitempty"`
	HTTPStatus      int        `json:"http_status,omitempty"`
	Code            string     `json:"code,omitempty"`
	Message         string     `json:"message"`
	Retryable       bool       `json:"retryable,omitempty"`
	CredentialScope bool       `json:"credential_scope,omitempty"`
	CooldownUntil   *time.Time `json:"cooldown_until,omitempty"`
}

// AuthErrorSummary is a lightweight summary included in credential list responses.
type AuthErrorSummary struct {
	Latest *AuthErrorRecord `json:"latest,omitempty"`
	Total  int              `json:"total"`
}

// AuthErrorHistoryStore persists per-credential error history across restarts.
type AuthErrorHistoryStore interface {
	Load(ctx context.Context, authID string) ([]AuthErrorRecord, error)
	Save(ctx context.Context, authID string, records []AuthErrorRecord) error
	Delete(ctx context.Context, authID string) error
}

type kvAuthErrorHistoryStore struct{}

func authErrorHistoryKVKey(authID string) string {
	return authErrorHistoryKVPrefix + kvstore.HashKeyPart(strings.TrimSpace(authID))
}

func (kvAuthErrorHistoryStore) Load(ctx context.Context, authID string) ([]AuthErrorRecord, error) {
	authID = strings.TrimSpace(authID)
	if authID == "" {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var records []AuthErrorRecord
	kvMode, found := kvstore.KVGetJSONBestEffort(ctx, authErrorHistoryKVKey(authID), &records)
	if !kvMode {
		return nil, errErrorHistoryStoreInactive
	}
	if !found {
		return nil, nil
	}
	return records, nil
}

func (kvAuthErrorHistoryStore) Save(ctx context.Context, authID string, records []AuthErrorRecord) error {
	authID = strings.TrimSpace(authID)
	if authID == "" {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	kvstore.KVSetJSONBestEffort(ctx, authErrorHistoryKVKey(authID), records, authErrorHistoryTTL)
	return nil
}

func (kvAuthErrorHistoryStore) Delete(ctx context.Context, authID string) error {
	authID = strings.TrimSpace(authID)
	if authID == "" {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	kvstore.KVDelBestEffort(ctx, authErrorHistoryKVKey(authID))
	return nil
}

type authErrorHistory struct {
	mu      sync.Mutex
	store   AuthErrorHistoryStore
	loaded  map[string]bool
	records map[string][]AuthErrorRecord
	nowFunc func() time.Time
}

func newAuthErrorHistory() *authErrorHistory {
	return &authErrorHistory{
		store:   kvAuthErrorHistoryStore{},
		loaded:  make(map[string]bool),
		records: make(map[string][]AuthErrorRecord),
		nowFunc: func() time.Time { return time.Now().UTC() },
	}
}

func (h *authErrorHistory) now() time.Time {
	if h != nil && h.nowFunc != nil {
		return h.nowFunc().UTC()
	}
	return time.Now().UTC()
}

func (h *authErrorHistory) setStore(store AuthErrorHistoryStore) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if store == nil {
		h.store = kvAuthErrorHistoryStore{}
	} else {
		h.store = store
	}
	h.loaded = make(map[string]bool)
}

func (h *authErrorHistory) ensureLoadedLocked(ctx context.Context, authID string) {
	if h == nil || authID == "" || h.loaded[authID] || h.store == nil {
		return
	}
	persisted, errLoad := h.store.Load(ctx, authID)
	if errLoad != nil {
		if errors.Is(errLoad, errErrorHistoryStoreInactive) {
			return
		}
		// Keep loaded=false on transient store errors so a later read can retry.
		return
	}
	h.loaded[authID] = true
	if len(persisted) == 0 {
		return
	}
	existing := h.records[authID]
	if len(existing) == 0 {
		h.records[authID] = clampAuthErrorRecords(persisted)
		return
	}
	// Merge in-memory records written before store hydration ahead of persisted history.
	merged := make([]AuthErrorRecord, 0, len(existing)+len(persisted))
	seen := make(map[string]struct{}, len(existing)+len(persisted))
	for _, rec := range existing {
		if rec.ID != "" {
			seen[rec.ID] = struct{}{}
		}
		merged = append(merged, rec)
	}
	for _, rec := range persisted {
		if rec.ID != "" {
			if _, dup := seen[rec.ID]; dup {
				continue
			}
			seen[rec.ID] = struct{}{}
		}
		merged = append(merged, rec)
	}
	h.records[authID] = clampAuthErrorRecords(merged)
}

func clampAuthErrorRecords(records []AuthErrorRecord) []AuthErrorRecord {
	if len(records) <= authErrorHistoryMaxPerAuth {
		out := make([]AuthErrorRecord, len(records))
		copy(out, records)
		return out
	}
	out := make([]AuthErrorRecord, authErrorHistoryMaxPerAuth)
	copy(out, records[:authErrorHistoryMaxPerAuth])
	return out
}

func cloneAuthErrorRecord(rec AuthErrorRecord) AuthErrorRecord {
	if rec.CooldownUntil != nil {
		t := rec.CooldownUntil.UTC()
		rec.CooldownUntil = &t
	}
	return rec
}

func (h *authErrorHistory) append(ctx context.Context, authID string, record AuthErrorRecord) {
	authID = strings.TrimSpace(authID)
	if h == nil || authID == "" {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if record.ID == "" {
		record.ID = uuid.NewString()
	}
	if record.Timestamp.IsZero() {
		record.Timestamp = h.now()
	} else {
		record.Timestamp = record.Timestamp.UTC()
	}
	if strings.TrimSpace(record.Phase) == "" {
		record.Phase = "execute"
	}
	record.Message = internallogging.SafeDetailForErrorDisplay(record.Message, authErrorMessageMaxRunes)
	if record.Message == "" {
		record.Message = "request failed"
	}

	h.mu.Lock()
	h.ensureLoadedLocked(ctx, authID)
	existing := h.records[authID]
	updated := make([]AuthErrorRecord, 0, min(len(existing)+1, authErrorHistoryMaxPerAuth))
	updated = append(updated, cloneAuthErrorRecord(record))
	for i := 0; i < len(existing) && len(updated) < authErrorHistoryMaxPerAuth; i++ {
		updated = append(updated, existing[i])
	}
	h.records[authID] = updated
	toPersist := clampAuthErrorRecords(updated)
	store := h.store
	h.mu.Unlock()

	if store != nil {
		_ = store.Save(context.WithoutCancel(ctx), authID, toPersist)
	}
}

func (h *authErrorHistory) list(ctx context.Context, authID string, limit int) []AuthErrorRecord {
	authID = strings.TrimSpace(authID)
	if h == nil || authID == "" {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	h.mu.Lock()
	h.ensureLoadedLocked(ctx, authID)
	items := h.records[authID]
	count := len(items)
	if limit > 0 && limit < count {
		count = limit
	}
	out := make([]AuthErrorRecord, count)
	for i := 0; i < count; i++ {
		out[i] = cloneAuthErrorRecord(items[i])
	}
	h.mu.Unlock()
	return out
}

func (h *authErrorHistory) summary(ctx context.Context, authID string) AuthErrorSummary {
	authID = strings.TrimSpace(authID)
	if h == nil || authID == "" {
		return AuthErrorSummary{}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	h.mu.Lock()
	h.ensureLoadedLocked(ctx, authID)
	items := h.records[authID]
	total := len(items)
	var latest *AuthErrorRecord
	if total > 0 {
		rec := cloneAuthErrorRecord(items[0])
		rec.Message = internallogging.SafeDetailForErrorDisplay(rec.Message, authErrorSummaryMaxRunes)
		latest = &rec
	}
	h.mu.Unlock()
	return AuthErrorSummary{
		Latest: latest,
		Total:  total,
	}
}

func (h *authErrorHistory) clear(ctx context.Context, authID string) {
	authID = strings.TrimSpace(authID)
	if h == nil || authID == "" {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	h.mu.Lock()
	delete(h.records, authID)
	h.loaded[authID] = true
	store := h.store
	h.mu.Unlock()

	if store != nil {
		_ = store.Delete(context.WithoutCancel(ctx), authID)
	}
}

func (m *Manager) ensureErrorHistory() *authErrorHistory {
	if m == nil {
		return nil
	}
	m.errorHistoryOnce.Do(func() {
		if m.errorHistory == nil {
			m.errorHistory = newAuthErrorHistory()
		}
	})
	return m.errorHistory
}

// SetErrorHistoryStore configures a custom persistence backend for per-credential error history.
func (m *Manager) SetErrorHistoryStore(store AuthErrorHistoryStore) {
	if m == nil {
		return
	}
	m.ensureErrorHistory().setStore(store)
}

// AuthErrorSummary returns a lightweight persisted error summary for authID.
func (m *Manager) AuthErrorSummary(ctx context.Context, authID string) AuthErrorSummary {
	if m == nil {
		return AuthErrorSummary{}
	}
	return m.ensureErrorHistory().summary(ctx, authID)
}

// AuthErrorHistory returns the newest-first persisted error records for authID.
func (m *Manager) AuthErrorHistory(ctx context.Context, authID string, limit int) []AuthErrorRecord {
	if m == nil {
		return nil
	}
	return m.ensureErrorHistory().list(ctx, authID, limit)
}

// ClearAuthErrorHistory removes persisted error records for authID.
func (m *Manager) ClearAuthErrorHistory(ctx context.Context, authID string) {
	if m == nil {
		return
	}
	m.ensureErrorHistory().clear(ctx, authID)
}

func (m *Manager) recordAuthErrorFromResult(ctx context.Context, result Result, authSnapshot *Auth) {
	if m == nil || result.Success || strings.TrimSpace(result.AuthID) == "" {
		return
	}
	if result.Error == nil && result.RetryAfter == nil {
		return
	}
	phase := strings.TrimSpace(result.Phase)
	if phase == "" {
		if result.Options.Stream {
			phase = "stream"
		} else {
			phase = "execute"
		}
	}
	now := m.ensureErrorHistory().now()
	rec := AuthErrorRecord{
		Timestamp:       now,
		RequestID:       strings.TrimSpace(internallogging.GetRequestID(ctx)),
		Phase:           phase,
		Provider:        strings.TrimSpace(result.Provider),
		Model:           strings.TrimSpace(result.Model),
		RouteModel:      strings.TrimSpace(result.RouteModel),
		CredentialScope: result.CredentialScope,
		Message:         errorEventBody(result.Error),
	}
	if rec.Provider == "" && authSnapshot != nil {
		rec.Provider = strings.TrimSpace(authSnapshot.Provider)
	}
	if result.Error != nil {
		rec.HTTPStatus = result.Error.HTTPStatus
		rec.Code = strings.TrimSpace(result.Error.Code)
		rec.Retryable = result.Error.Retryable
	}
	if cooldownUntil := cooldownUntilFromSnapshot(result.Model, authSnapshot, now); !cooldownUntil.IsZero() {
		t := cooldownUntil.UTC()
		rec.CooldownUntil = &t
	}
	m.ensureErrorHistory().append(ctx, result.AuthID, rec)
}

func (m *Manager) recordAuthRefreshError(ctx context.Context, authSnapshot *Auth, rawErr error, sanitizedErr *Error) {
	if m == nil || authSnapshot == nil || strings.TrimSpace(authSnapshot.ID) == "" {
		return
	}
	if rawErr == nil && sanitizedErr == nil {
		return
	}
	now := m.ensureErrorHistory().now()
	var msg string
	var code string
	var status int
	var retryable bool
	if sanitizedErr != nil {
		msg = strings.TrimSpace(sanitizedErr.Message)
		code = strings.TrimSpace(sanitizedErr.Code)
		status = sanitizedErr.HTTPStatus
		retryable = sanitizedErr.Retryable
	}
	if rawErr != nil {
		if rawStatus := statusCodeFromError(rawErr); rawStatus > 0 && status == 0 {
			status = rawStatus
		}
		if msg == "" {
			if rawMsg := strings.TrimSpace(SanitizeRenewalMessage(rawErr.Error())); rawMsg != "" {
				msg = rawMsg
			}
		}
	}
	if msg == "" {
		msg = "token refresh failed"
	}
	rec := AuthErrorRecord{
		Timestamp:       now,
		RequestID:       strings.TrimSpace(internallogging.GetRequestID(ctx)),
		Phase:           "refresh",
		Provider:        strings.TrimSpace(authSnapshot.Provider),
		HTTPStatus:      status,
		Code:            code,
		Message:         msg,
		Retryable:       retryable,
		CredentialScope: true,
	}
	if !authSnapshot.NextRetryAfter.IsZero() && authSnapshot.NextRetryAfter.After(now) {
		t := authSnapshot.NextRetryAfter.UTC()
		rec.CooldownUntil = &t
	}
	m.ensureErrorHistory().append(ctx, authSnapshot.ID, rec)
}

func cooldownUntilFromSnapshot(model string, authSnapshot *Auth, now time.Time) time.Time {
	if authSnapshot == nil {
		return time.Time{}
	}
	var until time.Time
	if model != "" {
		if state := existingModelState(authSnapshot, model); state != nil {
			if state.NextRetryAfter.After(now) && state.NextRetryAfter.After(until) {
				until = state.NextRetryAfter
			}
			if state.Quota.Exceeded && state.Quota.NextRecoverAt.After(now) && state.Quota.NextRecoverAt.After(until) {
				until = state.Quota.NextRecoverAt
			}
		}
	}
	if authSnapshot.NextRetryAfter.After(now) && authSnapshot.NextRetryAfter.After(until) {
		until = authSnapshot.NextRetryAfter
	}
	if authSnapshot.Quota.Exceeded && authSnapshot.Quota.NextRecoverAt.After(now) && authSnapshot.Quota.NextRecoverAt.After(until) {
		until = authSnapshot.Quota.NextRecoverAt
	}
	return until
}
