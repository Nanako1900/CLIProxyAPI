package executor

import (
	"context"
	"net/http"
	"testing"

	"github.com/MoeForge/moego/ojson"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/pool"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

func TestAccountPoolReplayKeysPartitionIdenticalClientSessions(t *testing.T) {
	req := coreexecutor.Request{Payload: []byte(`{"metadata":{"user_id":"same-user"},"session_id":"same-session"}`)}
	opts := coreexecutor.Options{SourceFormat: translator.FormatClaude, OriginalRequest: req.Payload, Headers: http.Header{"Session_id": {"same-session"}}}
	ctxA := pool.WithScope(context.Background(), pool.Scope{ID: "basic", Provider: "config-inline", Principal: "key"})
	ctxB := pool.WithScope(context.Background(), pool.Scope{ID: "premium", Provider: "config-inline", Principal: "key"})
	for name, key := range map[string]func(context.Context) string{
		"payload": func(ctx context.Context) string {
			return codexReasoningReplaySessionKey(ctx, translator.FormatClaude, req, opts, req.Payload)
		},
		"document": func(ctx context.Context) string {
			return codexReasoningReplaySessionKeyDoc(ctx, translator.FormatClaude, req, opts, ojson.NewDoc(req.Payload))
		},
	} {
		t.Run(name, func(t *testing.T) {
			a, b := key(ctxA), key(ctxB)
			if a == "" || b == "" || a == b {
				t.Fatalf("replay isolation missing: %q %q", a, b)
			}
			if a != key(ctxA) {
				t.Fatal("replay namespace changed between calls")
			}
		})
	}
}
