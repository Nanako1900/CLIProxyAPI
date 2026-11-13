package auth

import (
	"context"
	"maps"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/MoeForge/moego/ojson"
	internalconfig "github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	executor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/pool"
)

type poolError struct {
	code   string
	status int
}

func (e *poolError) Error() string         { return e.code }
func (e *poolError) StatusCode() int       { return e.status }
func (e *poolError) IsRequestScoped() bool { return true }

// AccountPoolID resolves persisted membership without treating omission as a default.
func (a *Auth) AccountPoolID() string {
	if a == nil {
		return ""
	}
	if value, exists := a.Metadata["pool_id"]; exists {
		if id, ok := value.(string); ok {
			return id
		}
		return "!invalid"
	}
	return a.PoolID
}

func (m *Manager) checkPoolScope(ctx context.Context) error {
	cfg, _ := m.runtimeConfig.Load().(*internalconfig.Config)
	scope, scoped := pool.FromContext(ctx)
	if cfg == nil || !cfg.AccountPools.Enabled {
		if scoped {
			return &poolError{"pool_state_unavailable", http.StatusServiceUnavailable}
		}
		return nil
	}
	if cfg.Home.Enabled {
		return &poolError{"pool_protocol_unsupported", http.StatusServiceUnavailable}
	}
	if !scoped {
		return &poolError{"pool_required", http.StatusBadRequest}
	}
	if !cfg.PoolAccess(scope.Provider, scope.Principal, scope.ID) {
		return &poolError{"pool_access_denied", http.StatusForbidden}
	}
	p, exists := cfg.AccountPool(scope.ID)
	if !exists {
		return &poolError{"pool_not_found", http.StatusNotFound}
	}
	if !p.IsEnabled() {
		return &poolError{"pool_disabled", http.StatusServiceUnavailable}
	}
	return nil
}

// admitPool revalidates a selected snapshot and fences membership changes for the
// lifetime of the upstream operation. It never holds m.mu over network I/O.
func (m *Manager) admitPool(ctx context.Context, selected *Auth) (func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkPoolScope(ctx); err != nil {
		return nil, err
	}
	scope, scoped := pool.FromContext(ctx)
	if !scoped {
		return func() {}, nil
	}
	if selected == nil {
		return nil, &poolError{"auth_not_found", http.StatusServiceUnavailable}
	}
	current := m.auths[selected.ID]
	if m.poolMoving[selected.ID] || current == nil || current.AccountPoolID() != scope.ID || current.Disabled ||
		current.RegistrationEpoch != selected.RegistrationEpoch {
		return nil, &poolError{"pinned_auth_scope_conflict", http.StatusConflict}
	}
	if m.poolActive == nil {
		m.poolActive = make(map[string]int)
	}
	m.poolActive[selected.ID]++
	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			m.poolActive[selected.ID]--
			if m.poolActive[selected.ID] == 0 {
				delete(m.poolActive, selected.ID)
			}
		})
	}, nil
}

func (m *Manager) executeInPool(ctx context.Context, target ProviderExecutor, a *Auth, req executor.Request, opts executor.Options) (executor.Response, error) {
	if err := checkPoolRequest(ctx, req); err != nil {
		return executor.Response{}, err
	}
	release, err := m.admitPool(ctx, a)
	if err != nil {
		return executor.Response{}, err
	}
	defer release()
	opts = poolExecutionOptions(ctx, opts)
	return target.Execute(ctx, a, req, opts)
}

func (m *Manager) countInPool(ctx context.Context, target ProviderExecutor, a *Auth, req executor.Request, opts executor.Options) (executor.Response, error) {
	if err := checkPoolRequest(ctx, req); err != nil {
		return executor.Response{}, err
	}
	release, err := m.admitPool(ctx, a)
	if err != nil {
		return executor.Response{}, err
	}
	defer release()
	opts = poolExecutionOptions(ctx, opts)
	return target.CountTokens(ctx, a, req, opts)
}

func (m *Manager) streamInPool(ctx context.Context, target ProviderExecutor, a *Auth, req executor.Request, opts executor.Options) (*executor.StreamResult, error) {
	if err := checkPoolRequest(ctx, req); err != nil {
		return nil, err
	}
	release, err := m.admitPool(ctx, a)
	if err != nil {
		return nil, err
	}
	opts = poolExecutionOptions(ctx, opts)
	result, errStream := target.ExecuteStream(ctx, a, req, opts)
	if errStream != nil || result == nil || result.Chunks == nil {
		release()
		return result, errStream
	}
	if _, scoped := pool.FromContext(ctx); !scoped {
		release()
		return result, nil
	}
	out := make(chan executor.StreamChunk)
	go func() {
		defer close(out)
		defer release()
		// Keep the admission until the executor has actually terminated. Cancellation
		// stops delivery but does not make a still-running operation safe to move.
		for chunk := range result.Chunks {
			select {
			case out <- chunk:
			case <-ctx.Done():
			}
		}
	}()
	return &executor.StreamResult{Headers: result.Headers, Chunks: out}, nil
}

// reconcilePoolSelectorsLocked gives each pool independent rotation and affinity state.
// Configuration snapshots and the selector map are published under the same lock.
func (m *Manager) reconcilePoolSelectorsLocked(previous, next *internalconfig.Config) {
	old := m.poolSelectors
	current := make(map[string]Selector)
	if next.AccountPools.Enabled {
		for _, p := range next.AccountPools.Pools {
			if !p.IsEnabled() {
				continue
			}
			if previous != nil && reflect.DeepEqual(previous.Routing, next.Routing) {
				prior, ok := previous.AccountPool(p.ID)
				if ok && prior.Strategy == p.Strategy && old[p.ID] != nil {
					current[p.ID] = old[p.ID]
					delete(old, p.ID)
					continue
				}
			}
			strategy := p.Strategy
			if strategy == "" {
				strategy = strings.ToLower(next.Routing.Strategy)
			}
			var selector Selector
			switch strategy {
			case "weighted-round-robin":
				selector = &WeightedRoundRobinSelector{}
			case "fill-first", "fillfirst", "ff":
				selector = &FillFirstSelector{}
			default:
				selector = &RoundRobinSelector{}
			}
			if next.Routing.SessionAffinity {
				ttl := time.Hour
				if parsed, err := time.ParseDuration(next.Routing.SessionAffinityTTL); err == nil && parsed > 0 {
					ttl = parsed
				}
				selector = NewSessionAffinitySelectorWithConfig(SessionAffinityConfig{Fallback: selector, TTL: ttl, SubagentAffinity: next.Routing.SessionAffinitySubagents, RebindOn: next.Routing.SessionAffinityRebindOn})
			}
			current[p.ID] = selector
		}
	}
	m.poolSelectors = current
	for _, selector := range old {
		if stop, ok := selector.(interface{ Stop() }); ok {
			stop.Stop()
		}
	}
}

func (m *Manager) selectorForContextLocked(ctx context.Context) Selector {
	if scope, ok := pool.FromContext(ctx); ok {
		if selector := m.poolSelectors[scope.ID]; selector != nil {
			return selector
		}
		return &FillFirstSelector{}
	}
	return m.selector
}

func (m *Manager) selectorForContext(ctx context.Context) Selector {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.selectorForContextLocked(ctx)
}

func poolExecutionOptions(ctx context.Context, opts executor.Options) executor.Options {
	if _, scoped := pool.FromContext(ctx); !scoped {
		return opts
	}
	opts.Metadata = maps.Clone(opts.Metadata)
	if opts.Metadata == nil {
		opts.Metadata = make(map[string]any)
	}
	opts.Metadata[executor.CallerScopeMetadataKey] = pool.Namespace(ctx, "caller")
	if id, ok := opts.Metadata[executor.ExecutionSessionMetadataKey].(string); ok && id != "" {
		opts.Metadata[executor.ExecutionSessionMetadataKey] = pool.Namespace(ctx, id)
	}
	return opts
}

func (m *Manager) selectorsSnapshot() []Selector {
	m.mu.RLock()
	defer m.mu.RUnlock()
	selectors := []Selector{m.selector}
	for _, selector := range m.poolSelectors {
		selectors = append(selectors, selector)
	}
	return selectors
}

func poolNoCandidateError(ctx context.Context, pinned string) error {
	if _, scoped := pool.FromContext(ctx); !scoped {
		return &Error{Code: "auth_not_found", Message: "no auth available"}
	}
	if pinned != "" {
		return &poolError{"pinned_auth_scope_conflict", http.StatusConflict}
	}
	return &poolError{"pool_no_eligible_accounts", http.StatusServiceUnavailable}
}

// Stateful upstream handles require an ownership ledger before they can cross a
// pooled boundary. Until that adapter exists, callers must replay full history.
func checkPoolRequest(ctx context.Context, req executor.Request) error {
	if _, scoped := pool.FromContext(ctx); !scoped {
		return nil
	}
	for _, path := range []string{"previous_response_id", "conversation", "conversation_id", "thread.previous_message_id", "thread_id", "previous_interaction_id"} {
		value := ojson.GetBytesNoCopy(req.Payload, path)
		if value.Exists() && value.Type != ojson.Null && value.String() != "" {
			return &poolError{"pool_continuation_unsupported", http.StatusNotImplemented}
		}
	}
	return nil
}

func poolCallerOptions(ctx context.Context, opts executor.Options) executor.Options {
	if _, scoped := pool.FromContext(ctx); !scoped {
		return opts
	}
	opts.Metadata = maps.Clone(opts.Metadata)
	if opts.Metadata == nil {
		opts.Metadata = make(map[string]any)
	}
	opts.Metadata[executor.CallerScopeMetadataKey] = pool.Namespace(ctx, "caller")
	return opts
}
