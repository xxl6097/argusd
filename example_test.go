package argus_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

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
	case errors.Is(err, argus.ErrAlreadyRunning):
		fmt.Println("another Run is already active")
	}
}

// ExampleWatcher_Stop shows the SIGHUP hot-reload pattern: the Watcher
// survives config changes without re-emitting Online for every known device.
//
// State preservation across Stop → Run:
//   - preserved: known, offlineCooldown, lastEventAt, detected Fetcher
//   - reset:     misses, disconnectInFlight, syslogHints channel, droppedHints
func ExampleWatcher_Stop() {
	w := argus.New()

	sighup := make(chan os.Signal, 1)
	signal.Notify(sighup, syscall.SIGHUP)
	sigterm := make(chan os.Signal, 1)
	signal.Notify(sigterm, syscall.SIGTERM, syscall.SIGINT)

	for {
		ctx, cancel := context.WithCancel(context.Background())
		runErr := make(chan error, 1)
		go func() { runErr <- w.Run(ctx, onEvent, onError) }()

		select {
		case <-sighup:
			// Hot-reload config
			stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := w.Stop(stopCtx); err != nil {
				log.Printf("stop timed out: %v", err)
			}
			stopCancel()
			cancel()
			<-runErr
			log.Println("reloaded config; restarting watcher")
			// loop continues, fresh Run with new config

		case <-sigterm:
			cancel()
			<-runErr
			return
		}
	}
}

// Example callback helpers for ExampleWatcher_Stop.
func onEvent(e argus.Event) {
	log.Printf("[event] %s %s", e.Kind, e.Device.MAC)
}
func onError(err error) {
	log.Printf("[error] %v", err)
}

// ExampleConfig_jsonReload shows loading Config from a JSON config file
// (e.g. /etc/argusd.json). Field names are stable across the v0.x line.
func ExampleConfig_jsonReload() {
	jsonBlob := []byte(`{
        "poll_interval":         2000000000,
        "offline_misses":        7,
        "weak_rssi":             -75,
        "disable_flap_suppression": true
    }`)

	var cfg argus.Config
	if err := json.Unmarshal(jsonBlob, &cfg); err != nil {
		log.Fatal(err)
	}

	w := argus.New(argus.WithConfig(cfg))
	_ = w
}

// ExampleWithLogger shows how to bridge Argus's structured log events
// to a third-party logger (here log/slog stand-in via fmt). The hot
// decision path does not log; this hook only fires for lifecycle events
// (watcher starting/stopping, fetcher detection) and recoverable
// anomalies (syslog buffer overflow, fetch failures).
func ExampleWithLogger() {
	w := argus.New(
		argus.WithLogger(func(_ context.Context, level argus.LogLevel, msg string, attrs ...argus.LogAttr) {
			// Adapt to slog: slog.LogAttrs(ctx, slog.Level(level), msg, ...)
			fmt.Printf("[%s] %s", level, msg)
			for _, a := range attrs {
				fmt.Printf(" %s=%v", a.Key, a.Value)
			}
			fmt.Println()
		}),
	)
	_ = w
}

// ExampleConfigError shows how to extract field-level details from a
// Config validation failure — useful when surfacing errors in a web UI
// where each form field needs its own message.
func ExampleConfigError() {
	cfg := argus.DefaultConfig()
	cfg.WeakMissThreshold = 0 // invalid

	err := cfg.Validate()
	if errors.Is(err, argus.ErrInvalidConfig) {
		var ce *argus.ConfigError
		if errors.As(err, &ce) {
			fmt.Printf("field=%s reason=%s", ce.Field, ce.Reason)
		}
	}
	// Output: field=WeakMissThreshold reason=must be > 0
}
