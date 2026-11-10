package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/MoeForge/moego/ojson"
	internalconfig "github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	internallogging "github.com/router-for-me/CLIProxyAPI/v8/internal/logging"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

// Quota shadow mode evaluates a quota-aware scheduling policy next to every real
// selection and records both decisions plus the quota windows they were based on.
// It never changes routing: the real pick is always returned unchanged.

const (
	quotaShadowFileName          = "quota-shadow.jsonl"
	quotaShadowDefaultSoftLimit  = 0.9
	quotaShadowMaxCandidates     = 128
	quotaShadowFloatEpsilon      = 0.01
	quotaShadowShortWindowCutoff = 24 * time.Hour
)

// quotaShadowSettingsFromConfig normalizes the shadow configuration. ok is false when disabled.
func quotaShadowSettingsFromConfig(cfg *internalconfig.Config) (quotaShadowSettings, bool) {
	if cfg == nil || !cfg.Routing.QuotaShadow.Enabled {
		return quotaShadowSettings{}, false
	}
	shadow := cfg.Routing.QuotaShadow
	settings := quotaShadowSettings{
		path:      strings.TrimSpace(shadow.File),
		maxBytes:  int64(shadow.MaxFileMB) << 20,
		softLimit: shadow.SoftLimitPercent / 100,
	}
	if settings.path == "" {
		settings.path = filepath.Join(internallogging.ResolveLogDirectory(cfg), quotaShadowFileName)
	}
	if settings.maxBytes <= 0 {
		settings.maxBytes = quotaShadowDefaultMaxBytes
	}
	if settings.softLimit <= 0 || settings.softLimit > 1 {
		settings.softLimit = quotaShadowDefaultSoftLimit
	}
	return settings, true
}

// applyQuotaShadowConfig starts, updates, replaces, or stops the shadow recorder.
// Settings other than the path are updated in place. A recorder replaced for a
// new path drains in the background, so the config update (which runs under the
// config mutex) never waits on file I/O, and the two recorders write different files.
func (m *Manager) applyQuotaShadowConfig(cfg *internalconfig.Config) {
	if m == nil {
		return
	}
	settings, enabled := quotaShadowSettingsFromConfig(cfg)
	m.quotaShadowMu.Lock()
	defer m.quotaShadowMu.Unlock()
	if m.quotaShadowStopped {
		return
	}
	current := m.quotaShadow.Load()
	if enabled && current != nil && current.path == settings.path {
		current.update(settings)
		return
	}
	if !enabled && current == nil {
		return
	}
	var next *quotaShadowRecorder
	if enabled {
		next = newQuotaShadowRecorder(settings)
	}
	if previous := m.quotaShadow.Swap(next); previous != nil {
		m.quotaShadowClosing.Add(1)
		go func() {
			defer m.quotaShadowClosing.Done()
			previous.close()
		}()
	}
}

// StopQuotaShadow flushes and stops the shadow recorder, including recorders
// still draining after a replacement. Later config updates do not restart it.
func (m *Manager) StopQuotaShadow() {
	if m == nil {
		return
	}
	m.quotaShadowMu.Lock()
	m.quotaShadowStopped = true
	previous := m.quotaShadow.Swap(nil)
	m.quotaShadowMu.Unlock()
	if previous != nil {
		previous.close()
	}
	m.quotaShadowClosing.Wait()
}

// quotaShadowCandidate is the lock-free snapshot of one selectable credential.
type quotaShadowCandidate struct {
	id         string
	provider   string
	priority   int
	observedAt time.Time
	signals    map[string]string
}

// quotaShadowEvaluation is the derived scheduling view of one candidate.
type quotaShadowEvaluation struct {
	candidate     quotaShadowCandidate
	windows       []QuotaWindow
	softLimited   bool
	perishLong    float64
	perishShort   float64
	shortActive   bool
	longRemaining float64
}

// observeQuotaShadowSelection snapshots the candidates eligible for this pick and
// hands scoring and serialization to the recorder goroutine.
func (m *Manager) observeQuotaShadowSelection(ctx context.Context, providers []string, model string, opts cliproxyexecutor.Options, tried map[string]struct{}, picked *Auth, pickedProvider string, pickErr error) {
	recorder := m.quotaShadow.Load()
	if recorder == nil || m.HomeEnabled() {
		return
	}
	now := time.Now()
	sessionHash := quotaShadowSessionHash(opts)
	attempt := len(tried)
	providers = append([]string(nil), providers...)
	if pickErr != nil || picked == nil {
		errText := errorString(pickErr)
		status := statusCodeFromError(pickErr)
		var retryAfter *time.Duration
		if pickErr != nil {
			retryAfter = retryAfterFromError(pickErr)
		}
		recorder.emit(func(dropped uint64) []byte {
			return renderQuotaShadowSelectionError(now, providers, model, sessionHash, attempt, status, retryAfter, errText, dropped)
		})
		return
	}
	candidates := m.quotaShadowCandidates(ctx, providers, model, opts, tried, now)
	actualID := picked.ID
	actualProvider := pickedProvider
	softLimit := recorder.softLimit()
	recorder.emit(func(dropped uint64) []byte {
		evaluations := evaluateQuotaShadowCandidates(candidates, now, softLimit)
		shadowID, reason := pickQuotaShadow(evaluations)
		return renderQuotaShadowSelection(now, providers, model, sessionHash, attempt, actualID, actualProvider, shadowID, reason, evaluations, dropped)
	})
}

// quotaShadowCandidates mirrors the real availability filter: eligible, untried,
// supporting the route model, not cooling down, and in the highest priority tier.
func (m *Manager) quotaShadowCandidates(ctx context.Context, providers []string, model string, opts cliproxyexecutor.Options, tried map[string]struct{}, now time.Time) []quotaShadowCandidate {
	providerSet := make(map[string]struct{}, len(providers))
	for _, provider := range providers {
		if key := canonicalSchedulingProvider(provider); key != "" {
			providerSet[key] = struct{}{}
		}
	}
	if len(providerSet) == 0 {
		return nil
	}
	eligibility := authSelectionEligibilityForRequest(ctx, opts)
	modelKey := canonicalModelKey(model)
	registryRef := registry.GetGlobalRegistry()

	m.mu.RLock()
	defer m.mu.RUnlock()
	pool := make([]*Auth, 0, len(m.auths))
	for _, candidate := range m.auths {
		if candidate == nil || candidate.Disabled {
			continue
		}
		if _, used := tried[candidate.ID]; used {
			continue
		}
		if !eligibility.allows(candidate) {
			continue
		}
		providerKey := canonicalSchedulingProvider(executorKeyFromAuth(candidate))
		if _, ok := providerSet[providerKey]; !ok {
			continue
		}
		if _, ok := m.executorLocked(providerKey); !ok {
			continue
		}
		if modelKey != "" && !m.authSupportsRouteModel(registryRef, candidate, model) {
			continue
		}
		pool = append(pool, candidate)
	}
	if len(pool) == 0 {
		return nil
	}
	available, errAvailable := m.availableAuthsForRouteModelForRequest(ctx, pool, "mixed", model, now, false, opts.Headers)
	if errAvailable != nil {
		return nil
	}
	out := make([]quotaShadowCandidate, 0, len(available))
	for _, auth := range available {
		snapshot := quotaShadowCandidate{
			id:         auth.ID,
			provider:   canonicalSchedulingProvider(executorKeyFromAuth(auth)),
			priority:   authPriority(auth),
			observedAt: auth.Quota.ObservedAt,
		}
		if len(auth.Quota.Signals) > 0 {
			snapshot.signals = make(map[string]string, len(auth.Quota.Signals))
			for key, value := range auth.Quota.Signals {
				snapshot.signals[key] = value
			}
		}
		out = append(out, snapshot)
	}
	return out
}

func evaluateQuotaShadowCandidates(candidates []quotaShadowCandidate, now time.Time, softLimit float64) []quotaShadowEvaluation {
	evaluations := make([]quotaShadowEvaluation, 0, len(candidates))
	for _, candidate := range candidates {
		evaluations = append(evaluations, evaluateQuotaShadowCandidate(candidate, now, softLimit))
	}
	return evaluations
}

// evaluateQuotaShadowCandidate derives the scheduling inputs from credential-wide
// windows only. Scoped windows (model-specific or additional limits) are recorded
// but not scored because their model coverage is not known.
func evaluateQuotaShadowCandidate(candidate quotaShadowCandidate, now time.Time, softLimit float64) quotaShadowEvaluation {
	evaluation := quotaShadowEvaluation{
		candidate:     candidate,
		windows:       ParseQuotaWindows(candidate.provider, QuotaState{ObservedAt: candidate.observedAt, Signals: candidate.signals}),
		longRemaining: 1,
	}
	for _, window := range evaluation.windows {
		if window.Scope != "" {
			continue
		}
		remaining := window.Remaining(now)
		if remaining <= 1-softLimit+1e-9 || (!window.Expired(now) && isQuotaWindowRejected(window.Status)) {
			evaluation.softLimited = true
		}
		if window.Duration <= 0 {
			continue
		}
		perish := window.PerishAtFullPace(now)
		if window.Duration <= quotaShadowShortWindowCutoff {
			evaluation.perishShort = math.Max(evaluation.perishShort, perish)
			if window.Active(now) {
				evaluation.shortActive = true
			}
			continue
		}
		evaluation.perishLong = math.Max(evaluation.perishLong, perish)
		evaluation.longRemaining = math.Min(evaluation.longRemaining, remaining)
	}
	return evaluation
}

func isQuotaWindowRejected(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "rejected", "limit_reached":
		return true
	default:
		return false
	}
}

// pickQuotaShadow applies the shadow policy and returns the chosen credential
// with the first ranking key that separated it from the runner-up:
//  1. credentials below the soft limit first
//  2. more weekly quota that would expire unused even at full pace
//  3. more short-window quota that would expire unused even at full pace
//  4. credentials without a running short window (keeps windows cycling and spreads load)
//  5. more remaining weekly quota
//  6. credential ID for determinism
func pickQuotaShadow(evaluations []quotaShadowEvaluation) (string, string) {
	if len(evaluations) == 0 {
		return "", "no_candidates"
	}
	ranked := make([]quotaShadowEvaluation, len(evaluations))
	copy(ranked, evaluations)
	sort.SliceStable(ranked, func(i, j int) bool {
		less, _ := quotaShadowLess(ranked[i], ranked[j])
		return less
	})
	if len(ranked) == 1 {
		return ranked[0].candidate.id, "only_candidate"
	}
	_, reason := quotaShadowLess(ranked[0], ranked[1])
	return ranked[0].candidate.id, reason
}

// quotaShadowLess reports whether a ranks before b and which key decided it.
func quotaShadowLess(a, b quotaShadowEvaluation) (bool, string) {
	if a.softLimited != b.softLimited {
		return !a.softLimited, "soft_limit"
	}
	if decided, less := compareDescending(a.perishLong, b.perishLong); decided {
		return less, "perish_long"
	}
	if decided, less := compareDescending(a.perishShort, b.perishShort); decided {
		return less, "perish_short"
	}
	if a.shortActive != b.shortActive {
		return !a.shortActive, "short_idle"
	}
	if decided, less := compareDescending(a.longRemaining, b.longRemaining); decided {
		return less, "long_remaining"
	}
	return a.candidate.id < b.candidate.id, "id"
}

func compareDescending(a, b float64) (decided bool, less bool) {
	if math.Abs(a-b) < quotaShadowFloatEpsilon {
		return false, false
	}
	return true, a > b
}

// observeQuotaShadowResult records the outcome and the latest quota windows of
// one execution so window semantics and consumption can be analyzed offline.
func (m *Manager) observeQuotaShadowResult(result Result, auth *Auth, now time.Time) {
	recorder := m.quotaShadow.Load()
	if recorder == nil || auth == nil {
		return
	}
	provider := canonicalSchedulingProvider(executorKeyFromAuth(auth))
	if provider == "" {
		provider = strings.ToLower(strings.TrimSpace(result.Provider))
	}
	var signals map[string]string
	observedAt := auth.Quota.ObservedAt
	if !result.SkipQuotaObservation && observedAt.Equal(now) && len(auth.Quota.Signals) > 0 {
		signals = make(map[string]string, len(auth.Quota.Signals))
		for key, value := range auth.Quota.Signals {
			signals[key] = value
		}
	}
	authID := auth.ID
	model := result.Model
	routeModel := result.RouteModel
	success := result.Success
	credentialScope := result.CredentialScope
	status := statusCodeFromResult(result.Error)
	var errorCode string
	if result.Error != nil {
		errorCode = result.Error.Code
	}
	var retryAfter *time.Duration
	if result.RetryAfter != nil {
		value := *result.RetryAfter
		retryAfter = &value
	}
	recorder.emit(func(dropped uint64) []byte {
		doc := ojson.NewDoc([]byte("{}"))
		_ = doc.Set("type", "observation")
		_ = doc.Set("ts_ms", now.UnixMilli())
		_ = doc.Set("auth", authID)
		_ = doc.Set("provider", provider)
		_ = doc.Set("model", model)
		if routeModel != "" && routeModel != model {
			_ = doc.Set("route_model", routeModel)
		}
		_ = doc.Set("success", success)
		if status != 0 {
			_ = doc.Set("status", status)
		}
		if errorCode != "" {
			_ = doc.Set("error_code", errorCode)
		}
		if credentialScope {
			_ = doc.Set("credential_scope", true)
		}
		if retryAfter != nil {
			_ = doc.Set("retry_after_ms", retryAfter.Milliseconds())
		}
		if len(signals) > 0 {
			_ = doc.Set("observed_at_ms", observedAt.UnixMilli())
			windows := ParseQuotaWindows(provider, QuotaState{ObservedAt: observedAt, Signals: signals})
			_ = doc.SetRawBytes("windows", renderQuotaWindows(windows))
			_ = doc.SetRawBytes("signals", renderQuotaSignals(signals))
		}
		_ = doc.Set("dropped", dropped)
		return doc.Bytes()
	})
}

func renderQuotaShadowSelection(now time.Time, providers []string, model, sessionHash string, attempt int, actualID, actualProvider, shadowID, reason string, evaluations []quotaShadowEvaluation, dropped uint64) []byte {
	doc := ojson.NewDoc([]byte("{}"))
	_ = doc.Set("type", "selection")
	_ = doc.Set("ts_ms", now.UnixMilli())
	_ = doc.Set("model", model)
	_ = doc.Set("providers", providers)
	if sessionHash != "" {
		_ = doc.Set("session", sessionHash)
	}
	_ = doc.Set("attempt", attempt)
	_ = doc.Set("actual", actualID)
	_ = doc.Set("actual_provider", actualProvider)
	_ = doc.Set("shadow", shadowID)
	_ = doc.Set("shadow_reason", reason)
	_ = doc.Set("agree", shadowID != "" && shadowID == actualID)
	// The real pick can come from outside the scored pool (session affinity spans
	// priority tiers; plugin schedulers apply their own filters), in which case a
	// disagreement is structural rather than a policy difference.
	actualInPool := false
	for _, evaluation := range evaluations {
		if evaluation.candidate.id == actualID {
			actualInPool = true
			break
		}
	}
	_ = doc.Set("actual_in_pool", actualInPool)
	sorted := make([]quotaShadowEvaluation, len(evaluations))
	copy(sorted, evaluations)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].candidate.id < sorted[j].candidate.id })
	if len(sorted) > quotaShadowMaxCandidates {
		sorted = sorted[:quotaShadowMaxCandidates]
		_ = doc.Set("candidates_truncated", true)
	}
	_ = doc.Set("candidate_count", len(evaluations))
	candidates := ojson.NewDoc([]byte("[]"))
	for _, evaluation := range sorted {
		_ = candidates.SetRawBytes("-1", renderQuotaShadowEvaluation(evaluation))
	}
	_ = doc.SetRawBytes("candidates", candidates.Bytes())
	_ = doc.Set("dropped", dropped)
	return doc.Bytes()
}

func renderQuotaShadowEvaluation(evaluation quotaShadowEvaluation) []byte {
	doc := ojson.NewDoc([]byte("{}"))
	_ = doc.Set("id", evaluation.candidate.id)
	_ = doc.Set("provider", evaluation.candidate.provider)
	_ = doc.Set("priority", evaluation.candidate.priority)
	if !evaluation.candidate.observedAt.IsZero() {
		_ = doc.Set("observed_at_ms", evaluation.candidate.observedAt.UnixMilli())
	}
	_ = doc.Set("soft_limited", evaluation.softLimited)
	_ = doc.Set("perish_long", roundQuotaShadow(evaluation.perishLong))
	_ = doc.Set("perish_short", roundQuotaShadow(evaluation.perishShort))
	_ = doc.Set("short_active", evaluation.shortActive)
	_ = doc.Set("long_remaining", roundQuotaShadow(evaluation.longRemaining))
	_ = doc.SetRawBytes("windows", renderQuotaWindows(evaluation.windows))
	return doc.Bytes()
}

func renderQuotaWindows(windows []QuotaWindow) []byte {
	doc := ojson.NewDoc([]byte("[]"))
	for _, window := range windows {
		item := ojson.NewDoc([]byte("{}"))
		_ = item.Set("name", window.Name)
		if window.Scope != "" {
			_ = item.Set("scope", window.Scope)
		}
		if window.Label != "" {
			_ = item.Set("label", window.Label)
		}
		if window.HasUsed {
			_ = item.Set("used", roundQuotaShadow(window.UsedFraction))
		}
		if !window.ResetAt.IsZero() {
			_ = item.Set("reset_at_ms", window.ResetAt.UnixMilli())
		}
		if window.Duration > 0 {
			_ = item.Set("duration_s", int64(window.Duration/time.Second))
		}
		if window.Status != "" {
			_ = item.Set("status", window.Status)
		}
		_ = doc.SetRawBytes("-1", item.Bytes())
	}
	return doc.Bytes()
}

func renderQuotaSignals(signals map[string]string) []byte {
	keys := make([]string, 0, len(signals))
	for key := range signals {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	doc := ojson.NewDoc([]byte("{}"))
	for _, key := range keys {
		_ = doc.Set(ojson.Escape(key), signals[key])
	}
	return doc.Bytes()
}

func renderQuotaShadowSelectionError(now time.Time, providers []string, model, sessionHash string, attempt, status int, retryAfter *time.Duration, errText string, dropped uint64) []byte {
	doc := ojson.NewDoc([]byte("{}"))
	_ = doc.Set("type", "selection_error")
	_ = doc.Set("ts_ms", now.UnixMilli())
	_ = doc.Set("model", model)
	_ = doc.Set("providers", providers)
	if sessionHash != "" {
		_ = doc.Set("session", sessionHash)
	}
	_ = doc.Set("attempt", attempt)
	if status != 0 {
		_ = doc.Set("status", status)
	}
	if retryAfter != nil {
		_ = doc.Set("retry_after_ms", retryAfter.Milliseconds())
	}
	if errText != "" {
		_ = doc.Set("error", truncateString(errText, 256))
	}
	_ = doc.Set("dropped", dropped)
	return doc.Bytes()
}

// quotaShadowSessionHash returns a short stable digest of the request session so
// cold bindings can be identified offline without recording raw identifiers.
func quotaShadowSessionHash(opts cliproxyexecutor.Options) string {
	sessionID := ""
	if opts.Metadata != nil {
		if raw, ok := opts.Metadata[cliproxyexecutor.CanonicalSessionIDMetadataKey].(string); ok {
			sessionID = strings.TrimSpace(raw)
		}
	}
	if sessionID == "" {
		sessionID = CanonicalSessionID(opts.Headers, opts.OriginalRequest, opts.Metadata)
	}
	if sessionID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(sessionID))
	return hex.EncodeToString(sum[:8])
}

func roundQuotaShadow(value float64) float64 {
	return math.Round(value*1e4) / 1e4
}
