package argus_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os/signal"
	"syscall"

	argus "github.com/xxl6097/argus"
)

// ExampleNew demonstrates the minimal setup: build a Watcher with defaults,
// then run it until SIGINT/SIGTERM is received.
//
// On OpenWrt, the library auto-detects ahsapd (vendor) or hostapd.* (stock);
// no options are required for the common case.
func ExampleNew() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	w := argus.New()

	err := w.Run(ctx, func(e argus.Event) {
		switch e.Kind {
		case argus.EventOnline:
			log.Printf("[+] %s joined %s", e.Device.MAC, e.Device.IP)
		case argus.EventOffline:
			log.Printf("[-] %s left", e.Device.MAC)
		case argus.EventChange:
			log.Printf("[~] %s changed: %+v", e.Device.MAC, e.Changes)
		}
	}, nil)
	if err != nil {
		log.Fatal(err)
	}
}

// ExampleWatcher_List shows a one-shot snapshot of the currently-associated
// devices without starting background monitoring.
func ExampleWatcher_List() {
	w := argus.New()
	ctx := context.Background()

	devices, err := w.List(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(argus.RenderTable(devices))
}

// ExampleWithDecisionHandler enables the decision trace to inspect why
// a particular online/offline was (or wasn't) emitted.
//
// DecisionHandler is zero-cost when not registered.
func ExampleWithDecisionHandler() {
	w := argus.New(
		argus.WithDecisionHandler(func(d argus.Decision) {
			log.Printf("[decision] %-24s %s %s", d.Kind, d.MAC, d.Detail)
		}),
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT)
	defer stop()

	_ = w.Run(ctx, func(e argus.Event) {
		log.Printf("[event] %s %s", e.Kind, e.Device.MAC)
	}, nil)
}

// ExampleWithBaseline demonstrates process hot-reload: carry the known-set
// from the old Watcher into the new one so no spurious "new online" events
// fire on restart.
func ExampleWithBaseline() {
	ctx := context.Background()

	// Old Watcher somewhere in your code
	old := argus.New()
	_, _ = old.List(ctx)

	// Capture the state snapshot (thread-safe deep copy)
	snapshot := old.Known()

	// Build a new Watcher seeded with that state; startup will not re-emit
	// these MACs as EventOnline.
	fresh := argus.New(argus.WithBaseline(snapshot))
	fmt.Printf("Seeded with %d known devices\n", len(fresh.Known()))
}

// ExampleConfig shows how to tune cooldown / flap-suppression knobs.
//
// Zero values preserve defaults; to fully disable a feature, use
// DisableCooldown / DisableFlapSuppression rather than a magic value.
func ExampleConfig() {
	_ = argus.New(argus.WithConfig(argus.Config{
		// Crowded WiFi environment: raise the "weak" threshold so more
		// borderline devices are treated as weak-signal.
		WeakRSSI:          -75,
		WeakMissThreshold: 10,

		// Turn off flap suppression entirely (e.g. aggressive IoT gateway
		// that wants every edge event, even duplicates).
		DisableFlapSuppression: true,
	}))
}

// ExampleErrNoFetcher demonstrates handling typed errors from Run.
//
// When running outside an OpenWrt context (no ubus / no ahsapd / no
// hostapd), EnsureFetcher returns ErrNoFetcher; all sentinel errors
// in the package are matchable via errors.Is.
func ExampleErrNoFetcher() {
	w := argus.New()
	err := w.Run(context.Background(), func(argus.Event) {}, nil)
	switch {
	case errors.Is(err, argus.ErrNoFetcher):
		fmt.Println("no ubus data source; run on OpenWrt or inject WithFetcher")
	case errors.Is(err, argus.ErrHandlerRequired):
		fmt.Println("onEvent must not be nil")
	case errors.Is(err, argus.ErrInvalidConfig):
		fmt.Println("Config.Validate rejected the config")
	case errors.Is(err, argus.ErrFetchFailed):
		fmt.Println("initial baseline fetch failed")
	}
}
