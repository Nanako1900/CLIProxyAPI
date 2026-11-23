package auth

import (
	"context"
	"testing"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

func TestIssue7741WrapStreamResultClosesOnCancellationDuringUpstreamStall(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	remaining := make(chan cliproxyexecutor.StreamChunk)
	t.Cleanup(func() { close(remaining) })

	manager := NewManager(nil, nil, nil)
	result := manager.wrapStreamResult(
		ctx,
		&Auth{ID: "issue7741-auth", Provider: "issue7741"},
		"issue7741",
		"issue7741-model",
		"issue7741-model",
		nil,
		nil,
		remaining,
		OAuthModelAliasResult{},
		false,
		cliproxyexecutor.Options{},
		1,
	)

	go func() {
		remaining <- cliproxyexecutor.StreamChunk{Payload: []byte(`{"type":"response.created"}`)}
	}()

	select {
	case chunk, ok := <-result.Chunks:
		if !ok {
			t.Fatal("output closed before forwarding the first upstream chunk")
		}
		if len(chunk.Payload) == 0 {
			t.Fatal("forwarded chunk has an empty payload")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the first upstream chunk")
	}

	cancel()

	select {
	case _, ok := <-result.Chunks:
		if ok {
			t.Fatal("output produced another chunk after cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("output did not close when context was canceled during upstream stall")
	}
}

func TestWrapStreamAccountsForDeliveredError(t *testing.T) {
	failure := &Error{HTTPStatus: 502, Code: ErrorCodeTransientTransport, Message: "malformed function call"}
	for _, tc := range []struct {
		name      string
		confirmed error
		terminal  bool
		cleanEOF  bool
	}{
		{name: "cancel while upstream stalls", confirmed: failure},
		{name: "cancel with terminal error ready", confirmed: failure, terminal: true},
		{name: "EOF after error event", confirmed: failure, cleanEOF: true},
		{name: "ordinary cancellation"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			hook := &captureResultHook{}
			manager := NewManager(nil, nil, hook)
			auth, err := manager.Register(ctx, &Auth{ID: t.Name(), Provider: "aistudio"})
			if err != nil {
				t.Fatal(err)
			}
			remaining := make(chan cliproxyexecutor.StreamChunk, 1)
			if tc.terminal {
				remaining <- cliproxyexecutor.StreamChunk{Err: failure}
			}
			if tc.cleanEOF {
				close(remaining)
			} else {
				defer close(remaining)
			}
			payload := []byte("event: error\n")
			if tc.confirmed == nil {
				payload = []byte("event: message_start\n")
			}
			buffered := []cliproxyexecutor.StreamChunk{{Payload: payload, ConfirmedError: tc.confirmed}}
			result := manager.wrapStreamResult(ctx, auth, auth.Provider, "gemini", "gemini", nil, buffered, remaining, OAuthModelAliasResult{}, false, cliproxyexecutor.Options{}, 1)
			if chunk := <-result.Chunks; string(chunk.Payload) != string(payload) {
				t.Fatalf("first payload = %q", chunk.Payload)
			}
			if !tc.cleanEOF {
				cancel()
			}
			deadline := time.After(time.Second)
			for open := true; open; {
				select {
				case chunk, ok := <-result.Chunks:
					open = ok
					if ok && (!tc.terminal || chunk.Err != failure) {
						t.Fatalf("unexpected chunk: %+v", chunk)
					}
				case <-deadline:
					t.Fatal("client stream did not close")
				}
			}
			if tc.confirmed == nil {
				if len(hook.results) != 0 {
					t.Fatalf("ordinary cancellation recorded %+v", hook.results)
				}
				return
			}
			if len(hook.results) != 1 || hook.results[0].Success || hook.results[0].Error == nil || hook.results[0].Error.Code != ErrorCodeTransientTransport || hook.results[0].Error.HTTPStatus != 502 {
				t.Fatalf("results = %+v, want one transient 502", hook.results)
			}
			updated, _ := manager.GetByID(auth.ID)
			if updated.Failed != 1 || updated.Success != 0 || updated.Unavailable || !updated.NextRetryAfter.IsZero() {
				t.Fatalf("unexpected credential counters or cooldown: %+v", updated)
			}
		})
	}
}
