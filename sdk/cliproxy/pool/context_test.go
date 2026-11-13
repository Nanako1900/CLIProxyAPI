package pool

import (
	"context"
	"strings"
	"testing"
)

func TestPoolScopeIsTypedAndInherited(t *testing.T) {
	forged := context.WithValue(context.Background(), "pool_id", "premium")
	if _, ok := FromContext(forged); ok {
		t.Fatal("untrusted string context granted pool")
	}
	ctx := WithScope(forged, Scope{ID: "basic", Provider: "config-inline", Principal: "client-secret"})
	copied := Inherit(context.Background(), ctx)
	scope, ok := FromContext(copied)
	if !ok || scope.ID != "basic" {
		t.Fatal("scope was lost")
	}
	first := Namespace(ctx, "session")
	if strings.Contains(first, "client-secret") || strings.Contains(first, "session") {
		t.Fatal("namespace leaked identity")
	}
	if first != Namespace(copied, "session") {
		t.Fatal("namespace not stable")
	}
	for _, other := range []Scope{{ID: "premium", Provider: scope.Provider, Principal: scope.Principal}, {ID: scope.ID, Provider: "external", Principal: scope.Principal}, {ID: scope.ID, Provider: scope.Provider, Principal: "another"}} {
		if first == Namespace(WithScope(context.Background(), other), "session") {
			t.Fatal("namespace collision")
		}
	}
	if Namespace(context.Background(), "legacy") != "legacy" {
		t.Fatal("legacy identity changed")
	}
}
