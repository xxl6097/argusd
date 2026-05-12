package argus_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	argus "github.com/xxl6097/argusd"
	"github.com/xxl6097/argusd/argustest"
)

func TestSpanRecorderReceivesRunSpan(t *testing.T) {
	var (
		mu    sync.Mutex
		names []string
	)
	rec := argus.SpanRecorderFunc(func(ctx context.Context, name string) (context.Context, func(error)) {
		mu.Lock()
		names = append(names, name)
		mu.Unlock()
		return ctx, func(error) {}
	})

	w := argus.New(
		argus.WithFetcher(&argustest.FixedFetcher{}),
		argus.WithSpanRecorder(rec),
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
	found := false
	for _, n := range names {
		if n == "argus.Run" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'argus.Run' span, got %v", names)
	}
}

func TestSpanRecorderFinishCalledOnce(t *testing.T) {
	// Each Start must be balanced by exactly one finish invocation.
	var starts, finishes atomic.Int32

	rec := argus.SpanRecorderFunc(func(ctx context.Context, name string) (context.Context, func(error)) {
		starts.Add(1)
		return ctx, func(error) {
			finishes.Add(1)
		}
	})

	w := argus.New(
		argus.WithFetcher(&argustest.FixedFetcher{}),
		argus.WithSpanRecorder(rec),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		_ = w.Run(ctx, func(argus.Event) {}, nil)
		close(done)
	}()
	time.Sleep(80 * time.Millisecond)
	cancel()
	<-done

	if s, f := starts.Load(), finishes.Load(); s != f {
		t.Errorf("start/finish mismatch: starts=%d finishes=%d", s, f)
	}
}

func TestSpanRecorderStartPanicIsolated(t *testing.T) {
	// A recorder that panics in Start must not kill Run.
	rec := argus.SpanRecorderFunc(func(ctx context.Context, name string) (context.Context, func(error)) {
		panic("recorder boom")
	})

	w := argus.New(
		argus.WithFetcher(&argustest.FixedFetcher{}),
		argus.WithSpanRecorder(rec),
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
			t.Errorf("Run should return nil despite recorder panic, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return — recorder panic killed it")
	}
}

func TestSpanRecorderFinishPanicIsolated(t *testing.T) {
	// A recorder whose finish closure panics must not kill the caller.
	rec := argus.SpanRecorderFunc(func(ctx context.Context, name string) (context.Context, func(error)) {
		return ctx, func(error) { panic("finish boom") }
	})

	w := argus.New(
		argus.WithFetcher(&argustest.FixedFetcher{}),
		argus.WithSpanRecorder(rec),
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
			t.Errorf("Run should return nil despite finish panic, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return — finish panic killed it")
	}
}

func TestSpanRecorderNilFinishIsTreatedAsNoop(t *testing.T) {
	// A recorder that returns a nil finish function must not panic
	// when the library invokes it.
	rec := argus.SpanRecorderFunc(func(ctx context.Context, name string) (context.Context, func(error)) {
		return ctx, nil
	})

	w := argus.New(
		argus.WithFetcher(&argustest.FixedFetcher{}),
		argus.WithSpanRecorder(rec),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- w.Run(ctx, func(argus.Event) {}, nil) }()
	time.Sleep(80 * time.Millisecond)
	cancel()
	<-done
}

func TestSpanRecorderUnregisteredIsNoop(t *testing.T) {
	// Without WithSpanRecorder, the library must behave identically to
	// pre-v0.12 — no panics, no allocations in the hot path.
	w := argus.New(argus.WithFetcher(&argustest.FixedFetcher{}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- w.Run(ctx, func(argus.Event) {}, nil) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done
}
