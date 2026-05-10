package argus

import (
	"context"
)

// LogLevel is the severity the library emits for structured log events.
// Matches log/slog conventions (Debug < Info < Warn < Error) but is a
// plain int so consumers not on slog (zap/zerolog/stdlib log) can pattern
// match without importing log/slog.
type LogLevel int

const (
	// LogLevelDebug is for verbose internal tracing (hot-path detail).
	// Argus itself currently emits nothing at Debug; reserved for future use.
	LogLevelDebug LogLevel = -4
	// LogLevelInfo is for one-off lifecycle messages (detector picks a
	// Fetcher, Run starts, Stop completes).
	LogLevelInfo LogLevel = 0
	// LogLevelWarn is for recoverable anomalies (syslog buffer drops,
	// cache refresh failures, ubus subprocess killed).
	LogLevelWarn LogLevel = 4
	// LogLevelError is for non-recoverable failures that the library
	// still surfaces to onError. Every LogLevelError call has a
	// matching ErrorHandler invocation.
	LogLevelError LogLevel = 8
)

// String returns the slog-compatible level name.
func (l LogLevel) String() string {
	switch {
	case l <= LogLevelDebug:
		return "DEBUG"
	case l <= LogLevelInfo:
		return "INFO"
	case l <= LogLevelWarn:
		return "WARN"
	default:
		return "ERROR"
	}
}

// LogAttr is a single structured log field. Key is always a stable
// identifier (e.g. "mac", "kind", "elapsed_ms"); Value can be any
// type slog / zap / zerolog can format.
type LogAttr struct {
	Key   string
	Value any
}

// LoggerHandler is called by the library to emit structured log lines.
// It is called synchronously from the calling goroutine and MUST NOT
// block.
//
// The library never logs from the hot decision path (emitDecision,
// safeInvokeEvent): every log call site is a one-off lifecycle or
// recoverable-anomaly event, dispatched at most a few times per second.
//
// When LoggerHandler is nil (the default, see WithLogger), library log
// call sites bail out on a nil check without invoking the handler. The
// variadic attrs slice may still be constructed at the call site — this
// is fine for the paths Argus logs from; the hot path has no log calls.
//
// Typical adapters:
//
//	// log/slog
//	argus.WithLogger(func(ctx context.Context, level argus.LogLevel, msg string, attrs ...argus.LogAttr) {
//	    sa := make([]slog.Attr, len(attrs))
//	    for i, a := range attrs {
//	        sa[i] = slog.Any(a.Key, a.Value)
//	    }
//	    slog.LogAttrs(ctx, slog.Level(level), msg, sa...)
//	})
//
//	// zap
//	argus.WithLogger(func(_ context.Context, level argus.LogLevel, msg string, attrs ...argus.LogAttr) {
//	    fields := make([]zap.Field, len(attrs))
//	    for i, a := range attrs {
//	        fields[i] = zap.Any(a.Key, a.Value)
//	    }
//	    switch level {
//	    case argus.LogLevelWarn: zapLogger.Warn(msg, fields...)
//	    case argus.LogLevelError: zapLogger.Error(msg, fields...)
//	    default: zapLogger.Info(msg, fields...)
//	    }
//	})
//
// ErrorHandler remains the primary error-reporting surface for
// programmatic error handling (errors.Is matching). LoggerHandler is
// complementary — it surfaces the same events with structured context
// for observability pipelines (slog / zap / OpenTelemetry), without
// forcing consumers to parse error.Error() strings.
type LoggerHandler func(ctx context.Context, level LogLevel, msg string, attrs ...LogAttr)

// WithLogger registers a structured logger. May be called multiple
// times — only the last registration takes effect.
//
// When unregistered (the default), library log call sites bail on a
// nil check without invoking the handler.
func WithLogger(h LoggerHandler) Option {
	return func(w *Watcher) {
		w.logger = h
	}
}

// log is the internal emission point used throughout the library.
// When w.logger is nil (the default), this function returns immediately.
//
// Call sites use a literal attr list, e.g.:
//
//	w.log(ctx, LogLevelWarn, "syslog buffer overflow",
//	    LogAttr{"dropped", n},
//	    LogAttr{"window_sec", 30},
//	)
//
// Logger panics are recovered here so a misbehaving handler never kills
// the caller.
func (w *Watcher) log(ctx context.Context, level LogLevel, msg string, attrs ...LogAttr) {
	h := w.logger
	if h == nil {
		return
	}
	defer func() { _ = recover() }() // logger panic must never kill caller
	h(ctx, level, msg, attrs...)
}
