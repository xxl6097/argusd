package argus

import "context"

// SpanRecorder is an optional tracing hook the library uses to emit
// distributed-tracing spans for the multi-stage decision pipeline
// (syslog hint → ping check → emit Online/Offline).
//
// It is the tracing analogue of LoggerHandler: a tiny interface the
// library calls into, with zero third-party dependency. Adapters to
// OpenTelemetry / OpenTracing / Datadog APM are typically ~15 lines.
//
// Start opens a span scoped to the returned context. The returned
// finish function MUST be called exactly once with the operation's
// final error (or nil on success). Span lifecycle is owned by the
// caller; the library never panics if Start is called from a hot
// path with a non-nil recorder, but in practice it is only called
// from the lifecycle paths that already log (see WithLogger).
//
// A nil SpanRecorder is the default: every Start call site short-
// circuits via a single nil check, no allocation.
//
// Example adapter for OpenTelemetry:
//
//	import "go.opentelemetry.io/otel"
//	tracer := otel.Tracer("argus")
//	argus.WithSpanRecorder(argus.SpanRecorderFunc(
//	    func(ctx context.Context, name string) (context.Context, func(error)) {
//	        ctx, span := tracer.Start(ctx, name)
//	        return ctx, func(err error) {
//	            if err != nil {
//	                span.RecordError(err)
//	            }
//	            span.End()
//	        }
//	    },
//	))
type SpanRecorder interface {
	// Start opens a new span. Returns a context.Context that should be
	// used by the spanned operation, and a finish function that the
	// caller must invoke exactly once. The finish function takes the
	// final error (or nil on success).
	Start(ctx context.Context, name string) (context.Context, func(err error))
}

// SpanRecorderFunc adapts a plain function to the SpanRecorder
// interface, mirroring net/http.HandlerFunc style. Useful for inline
// declarations in WithSpanRecorder.
type SpanRecorderFunc func(ctx context.Context, name string) (context.Context, func(err error))

// Start implements SpanRecorder.
func (f SpanRecorderFunc) Start(ctx context.Context, name string) (context.Context, func(err error)) {
	return f(ctx, name)
}

// WithSpanRecorder registers a tracing hook. May be called multiple
// times; only the last registration takes effect.
//
// When unregistered (the default), all library [Watcher.startSpan] call
// sites bail on a single nil check; no context.Context wrapping, no
// closure allocation, no observable cost on the hot path.
func WithSpanRecorder(r SpanRecorder) Option {
	return func(w *Watcher) {
		w.spans = r
	}
}

// startSpan is the internal entry point. It returns the (possibly
// unchanged) context and a finish function that is always safe to
// defer-call. When no recorder is registered, the returned finish is
// a shared no-op closure.
//
// Recorder panics are recovered (both Start and finish) to ensure
// tracing failures never kill the caller; this matches the
// LoggerHandler contract. When Start panics, the original ctx is
// returned unchanged alongside noopFinish — named returns let the
// deferred recover substitute safe defaults.
func (w *Watcher) startSpan(ctx context.Context, name string) (outCtx context.Context, finish func(err error)) {
	outCtx, finish = ctx, noopFinish
	r := w.spans
	if r == nil {
		return
	}
	defer func() {
		if rec := recover(); rec != nil {
			// Recorder.Start panicked: return original ctx + noop finish.
			outCtx, finish = ctx, noopFinish
		}
	}()
	newCtx, userFinish := r.Start(ctx, name)
	outCtx = newCtx
	if userFinish == nil {
		finish = noopFinish
		return
	}
	// Wrap finish so a panic inside it doesn't kill the caller either.
	finish = func(err error) {
		defer func() { _ = recover() }()
		userFinish(err)
	}
	return
}

// noopFinish is the singleton no-op span finisher used when no
// SpanRecorder is registered. Sharing one instance avoids allocating
// a closure per startSpan call.
var noopFinish = func(error) {}
