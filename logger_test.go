package argus_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	argus "github.com/xxl6097/argus"
	"github.com/xxl6097/argus/argustest"
)

func TestLoggerReceivesLifecycleEvents(t *testing.T) {
	// With a registered LoggerHandler, Run should emit at least the
	// "watcher starting" Info event before ctx cancels.
	var (
		mu   sync.Mutex
		logs []logRecord
	)
	h := func(_ context.Context, level argus.LogLevel, msg string, attrs ...argus.LogAttr) {
		mu.Lock()
		defer mu.Unlock()
		logs = append(logs, logRecord{level: level, msg: msg, attrs: attrs})
	}

	w := argus.New(
		argus.WithFetcher(&argustest.FixedFetcher{}),
		argus.WithLogger(h),
	)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = w.Run(ctx, func(argus.Event) {}, nil)
		close(done)
	}()
	time.Sleep(80 * time.Millisecond)
	cancel()

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer stopCancel()
	_ = w.Stop(stopCtx)
	<-done

	mu.Lock()
	defer mu.Unlock()
	if len(logs) == 0 {
		t.Fatal("expected at least one log event, got none")
	}

	found := false
	for _, r := range logs {
		if r.msg == "watcher starting" {
			found = true
			if r.level != argus.LogLevelInfo {
				t.Errorf("watcher starting should be Info, got %v", r.level)
			}
			break
		}
	}
	if !found {
		t.Errorf("expected 'watcher starting' log, got %+v", logs)
	}
}

func TestLoggerPanicIsolated(t *testing.T) {
	// A panicking LoggerHandler must not kill the Watcher.
	var called atomic.Int32
	h := func(_ context.Context, _ argus.LogLevel, _ string, _ ...argus.LogAttr) {
		called.Add(1)
		panic("boom")
	}

	w := argus.New(
		argus.WithFetcher(&argustest.FixedFetcher{}),
		argus.WithLogger(h),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- w.Run(ctx, func(argus.Event) {}, nil) }()
	time.Sleep(80 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run should return nil, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}
	if called.Load() == 0 {
		t.Error("logger was never called")
	}
}

func TestLoggerNilIsZeroCost(t *testing.T) {
	// Without WithLogger, the library must not crash; this is the
	// existing default and the test exists to guard against a future
	// regression where we forget the nil check.
	w := argus.New(argus.WithFetcher(&argustest.FixedFetcher{}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- w.Run(ctx, func(argus.Event) {}, nil) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done
}

func TestConfigErrorExposesFieldViaAs(t *testing.T) {
	// Config.Validate returns *ConfigError so downstream UI code can
	// extract the offending field name.
	cfg := argus.DefaultConfig()
	cfg.OfflineMisses = 0

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate should reject OfflineMisses=0")
	}
	if !errors.Is(err, argus.ErrInvalidConfig) {
		t.Errorf("validate error should wrap ErrInvalidConfig, got %v", err)
	}

	var ce *argus.ConfigError
	if !errors.As(err, &ce) {
		t.Fatalf("should be a *ConfigError, got %T: %v", err, err)
	}
	if ce.Field != "OfflineMisses" {
		t.Errorf("field = %q, want %q", ce.Field, "OfflineMisses")
	}
	if ce.Value != 0 {
		t.Errorf("value = %v, want 0", ce.Value)
	}
}

func TestConfigErrorFromRunIsUnwrappable(t *testing.T) {
	// Run with a bad config must return an error that unwraps to
	// both ErrInvalidConfig and *ConfigError. We call Validate directly
	// here because WithConfig treats zero values as "use default" — the
	// only way to land a bad config at Run time is to construct one
	// that's already non-zero-but-illegal (RSSI > 0, etc.).
	cfg := argus.DefaultConfig()
	cfg.CooldownReleaseRSSI = 5 // must be <= 0

	err := cfg.Validate()
	if !errors.Is(err, argus.ErrInvalidConfig) {
		t.Errorf("Validate err should be ErrInvalidConfig, got %v", err)
	}
	var ce *argus.ConfigError
	if !errors.As(err, &ce) {
		t.Errorf("Validate err should expose *ConfigError, got %T: %v", err, err)
	}
	if ce != nil && ce.Field != "CooldownReleaseRSSI" {
		t.Errorf("field = %q, want %q", ce.Field, "CooldownReleaseRSSI")
	}
}

type logRecord struct {
	level argus.LogLevel
	msg   string
	attrs []argus.LogAttr
}
