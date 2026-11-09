package usage

import "context"

// Recorder commits a usage event before asynchronous plugins are notified.
// Implementations must not retain credentials from Record.
type Recorder interface {
	Record(context.Context, Record) error
}

type recorderContextKey struct{}

// WithRecorder scopes durable accounting to one server or SDK request.
func WithRecorder(ctx context.Context, recorder Recorder) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, recorderContextKey{}, recorder)
}

func recorderFromContext(ctx context.Context) Recorder {
	if ctx == nil {
		return nil
	}
	recorder, _ := ctx.Value(recorderContextKey{}).(Recorder)
	return recorder
}

// InheritRecorder preserves accounting when a handler supplies its own parent context.
func InheritRecorder(ctx, requestCtx context.Context) context.Context {
	if recorder := recorderFromContext(requestCtx); recorder != nil {
		return WithRecorder(ctx, recorder)
	}
	return ctx
}
