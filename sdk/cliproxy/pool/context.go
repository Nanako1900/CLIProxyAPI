// Package pool carries host-authenticated account pool scope across execution.
package pool

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

type scopeKey struct{}

// Scope is established by the host, never decoded from request metadata.
type Scope struct {
	ID        string
	Principal string
	Provider  string
}

func WithScope(ctx context.Context, scope Scope) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, scopeKey{}, scope)
}

func FromContext(ctx context.Context) (Scope, bool) {
	if ctx == nil {
		return Scope{}, false
	}
	scope, ok := ctx.Value(scopeKey{}).(Scope)
	return scope, ok && scope.ID != ""
}

func Inherit(ctx, source context.Context) context.Context {
	if scope, ok := FromContext(source); ok {
		return WithScope(ctx, scope)
	}
	return ctx
}

// Namespace partitions existing session identities without exposing credentials.
func Namespace(ctx context.Context, identity string) string {
	scope, ok := FromContext(ctx)
	if !ok {
		return identity
	}
	sum := sha256.Sum256(fmt.Appendf(nil, "%d:%s%d:%s%d:%s%d:%s", len(scope.ID), scope.ID, len(scope.Provider), scope.Provider, len(scope.Principal), scope.Principal, len(identity), identity))
	return "pool-v1:" + hex.EncodeToString(sum[:])
}
