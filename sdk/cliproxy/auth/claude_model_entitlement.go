package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

// ErrorCodePoolModelNotSupported denotes a permanent model/credential mismatch,
// not an upstream rejection or a temporarily exhausted quota.
const ErrorCodePoolModelNotSupported = "pool_model_not_supported"

var errClaudePlanChanged = errors.New("selected credential is not entitled to the upstream Claude Fable model")

func noClaudeFableAccount() *Error {
	return &Error{
		Code:       ErrorCodePoolModelNotSupported,
		HTTPStatus: http.StatusServiceUnavailable,
		Message:    "No account in the current pool is eligible for Claude Fable. Choose another model or contact the pool administrator.",
	}
}

func claudeSubscriptionAuth(a *Auth) bool {
	return a != nil && strings.EqualFold(a.Provider, "claude") && (a.AuthKind() != AuthKindAPIKey || strings.HasPrefix(authAttribute(a, AttributeAPIKey), "sk-ant-oat"))
}

func isClaudeFableModel(model string) bool {
	base := canonicalModelLookupKey(model)
	return base == "claude-fable" || strings.HasPrefix(base, "claude-fable-")
}

func claudeFableAllowed(a *Auth) bool {
	if !claudeSubscriptionAuth(a) {
		return true
	}
	plan := authMetadataString(a, "plan_type")
	if plan == "" {
		plan = authAttribute(a, "plan_type")
	}
	if plan == "" {
		plan = authMetadataString(a, "subscription_type")
	}
	if plan == "" {
		plan = authMetadataString(a, "subscriptionType")
	}
	plan = strings.TrimPrefix(strings.ToLower(plan), "claude_")
	if plan == "pro" || plan == "free" {
		return false
	}
	if enabled, ok := a.Metadata["claude_fable_enabled"].(bool); ok {
		return enabled
	}
	return plan == "max"
}

// ClaudePlanAllowsModel evaluates only subscription entitlement. Model registration,
// pool membership, cooldown and pinning are independent constraints.
func (m *Manager) ClaudePlanAllowsModel(a *Auth, routeModel string) bool {
	if !claudeSubscriptionAuth(a) || routeModel == "" {
		return true
	}
	upstream := m.selectionModelForAuth(a, routeModel)
	if a.AuthKind() == AuthKindAPIKey {
		upstream = m.applyAPIKeyModelAlias(a, upstream)
	}
	return !isClaudeFableModel(upstream) || claudeFableAllowed(a)
}

// Keep hard capability exclusions separate from transient availability when
// deciding whether an existing session binding can move to another account.
type planExcludedIDs map[string]struct{}
type planExcludedKey struct{}

func (ids *planExcludedIDs) add(id string) {
	if *ids == nil {
		*ids = make(planExcludedIDs)
	}
	(*ids)[id] = struct{}{}
}

func (ids planExcludedIDs) withContext(ctx context.Context) context.Context {
	if len(ids) == 0 {
		return ctx
	}
	return context.WithValue(ctx, planExcludedKey{}, ids)
}

func planExcludesBinding(ctx context.Context, id string) bool {
	if ctx == nil {
		return false
	}
	ids, _ := ctx.Value(planExcludedKey{}).(planExcludedIDs)
	_, blocked := ids[id]
	return blocked
}
