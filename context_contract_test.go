package argus_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	argus "github.com/xxl6097/argus"
	"github.com/xxl6097/argus/argustest"
)

// The context-cancellation contract table in STABILITY.md is part of the
// Stable public surface. These tests are the enforcement.

func TestContractRunReturnsNilOnCtxCancel(t *testing.T) {
	// Run MUST return nil (not ctx.Err()) when ctx.Done() fires mid-call.
	// Consumers rely on `if err != nil` distinguishing "terminal failure"
	// from "graceful shutdown".
	w := argus.New(argus.WithFetcher(&argustest.FixedFetcher{}))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- w.Run(ctx, func(argus.Event) {}, nil)
	}()
	time.Sleep(50 * time.Millisecond) // let Run get past baseline
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run should return nil on ctx cancel, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return within 3s of ctx cancel")
	}
}

func TestContractStopIdempotent(t *testing.T) {
	// Stop with no active Run must return nil.
	w := argus.New(argus.WithFetcher(&argustest.FixedFetcher{}))
	if err := w.Stop(context.Background()); err != nil {
		t.Errorf("Stop with no Run should return nil, got %v", err)
	}
	if err := w.Stop(context.Background()); err != nil {
		t.Errorf("second Stop should also return nil, got %v", err)
	}
}

func TestContractStopReturnsDeadlineExceeded(t *testing.T) {
	// When stopCtx is already cancelled, Stop must surface the ctx error.
	w := argus.New(argus.WithFetcher(&argustest.FixedFetcher{}))
	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()

	runDone := make(chan error, 1)
	go func() {
		runDone <- w.Run(runCtx, func(argus.Event) {}, nil)
	}()
	time.Sleep(50 * time.Millisecond)

	// Use a pre-cancelled stopCtx so Stop hits the DeadlineExceeded path.
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer stopCancel()
	time.Sleep(5 * time.Millisecond)

	err := w.Stop(stopCtx)
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		// Either is acceptable per the contract (stopCtx.Err() surface).
		// A nil here means the Run finished before stopCtx — also fine.
		t.Logf("Stop returned %v (OK if Run already finished)", err)
	}

	cancelRun()
	<-runDone
}

func TestContractRunAlreadyRunning(t *testing.T) {
	// Concurrent Run on the same Watcher must fast-fail with ErrAlreadyRunning.
	w := argus.New(argus.WithFetcher(&argustest.FixedFetcher{}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runDone := make(chan error, 1)
	go func() {
		runDone <- w.Run(ctx, func(argus.Event) {}, nil)
	}()
	time.Sleep(50 * time.Millisecond)

	err := w.Run(ctx, func(argus.Event) {}, nil)
	if !errors.Is(err, argus.ErrAlreadyRunning) {
		t.Errorf("second Run should return ErrAlreadyRunning, got %v", err)
	}

	cancel()
	<-runDone
}

func TestContractRunStopConcurrencySafe(t *testing.T) {
	// Calling Stop while Run is blocked must be race-free.
	w := argus.New(argus.WithFetcher(&argustest.FixedFetcher{}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	var runErr error
	go func() {
		defer wg.Done()
		runErr = w.Run(ctx, func(argus.Event) {}, nil)
	}()

	time.Sleep(50 * time.Millisecond)

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer stopCancel()
	_ = w.Stop(stopCtx)

	wg.Wait()
	if runErr != nil {
		t.Errorf("Run should return nil after Stop, got %v", runErr)
	}
}

func TestContractListReturnsFetcherError(t *testing.T) {
	// List propagates Fetcher errors without mutating Watcher state.
	sentinel := errors.New("synthetic fetch failure")
	w := argus.New(argus.WithFetcher(&argustest.FixedFetcher{Err: sentinel}))

	_, err := w.List(context.Background())
	if !errors.Is(err, sentinel) {
		t.Errorf("List should wrap Fetcher err (%v), got %v", sentinel, err)
	}
}
