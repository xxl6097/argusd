package argus

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDecisionKindStringTable(t *testing.T) {
	// Every defined constant must have a non-UNKNOWN string identifier.
	// DecisionKind integer values have reserved gaps — enumerate the
	// actual defined constants rather than iterating a range.
	defined := []DecisionKind{
		DecisionConnectHintReceived, DecisionConnectSkippedKnown, DecisionConnectEmitted,
		DecisionCooldownSuppressOnline, DecisionCooldownCleared, DecisionFlapSuppressOnline,
		DecisionDisconnectHintReceived, DecisionDisconnectIgnoredUnknown, DecisionDisconnectPingOK,
		DecisionOfflineEmitted, DecisionFlapSuppressOffline, DecisionCooldownSuppressOffline,
		DecisionDisconnectSkippedInflight,
		DecisionPollAPSleepProtected, DecisionPollWeakSignalMiss,
		DecisionPollARPFailedOffline, DecisionPollMissesExhausted,
	}
	for _, k := range defined {
		got := k.String()
		if got == "" || got == "DECISION_UNKNOWN" {
			t.Errorf("DecisionKind(%d).String() = %q — should be specific", k, got)
		}
	}
	if got := DecisionKind(999).String(); got != "DECISION_UNKNOWN" {
		t.Errorf("unknown kind string = %q", got)
	}
}

func TestDecisionKindLabelTable(t *testing.T) {
	defined := []DecisionKind{
		DecisionConnectHintReceived, DecisionConnectSkippedKnown, DecisionConnectEmitted,
		DecisionCooldownSuppressOnline, DecisionCooldownCleared, DecisionFlapSuppressOnline,
		DecisionDisconnectHintReceived, DecisionDisconnectIgnoredUnknown, DecisionDisconnectPingOK,
		DecisionOfflineEmitted, DecisionFlapSuppressOffline, DecisionCooldownSuppressOffline,
		DecisionDisconnectSkippedInflight,
		DecisionPollAPSleepProtected, DecisionPollWeakSignalMiss,
		DecisionPollARPFailedOffline, DecisionPollMissesExhausted,
	}
	for _, k := range defined {
		got := k.Label()
		if got == "" || got == "未知决策" {
			t.Errorf("DecisionKind(%d).Label() = %q — should be specific", k, got)
		}
	}
	if got := DecisionKind(999).Label(); got != "未知决策" {
		t.Errorf("unknown kind label = %q", got)
	}
}

func TestDecisionStringFormat(t *testing.T) {
	ts := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	// Without detail
	d := Decision{Time: ts, Kind: DecisionConnectEmitted, MAC: "aa:bb"}
	s := d.String()
	if !strings.Contains(s, "2026-05-10 12:00:00") || !strings.Contains(s, "aa:bb") {
		t.Errorf("String() missing timestamp or MAC: %q", s)
	}
	// With detail
	d.Detail = "via=poll"
	s = d.String()
	if !strings.Contains(s, "via=poll") {
		t.Errorf("String() missing detail: %q", s)
	}
}

func TestDecisionKindMarshalJSON(t *testing.T) {
	// Marshals to the stable English String(), not the int.
	b, err := DecisionConnectEmitted.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `"CONNECT_EMIT"` {
		t.Errorf("MarshalJSON = %s, want \"CONNECT_EMIT\"", b)
	}
	// Round-trip in a wrapping struct
	type wrap struct {
		K DecisionKind `json:"k"`
	}
	out, _ := json.Marshal(wrap{K: DecisionOfflineEmitted})
	if !strings.Contains(string(out), `"OFFLINE_EMIT"`) {
		t.Errorf("wrapped marshal = %s", out)
	}
}

func TestLogLevelString(t *testing.T) {
	cases := []struct {
		l    LogLevel
		want string
	}{
		{LogLevelDebug, "DEBUG"},
		{LogLevelDebug - 1, "DEBUG"},
		{LogLevelInfo, "INFO"},
		{LogLevelWarn, "WARN"},
		{LogLevelError, "ERROR"},
		{LogLevelError + 10, "ERROR"},
	}
	for _, c := range cases {
		if got := c.l.String(); got != c.want {
			t.Errorf("LogLevel(%d).String() = %q, want %q", c.l, got, c.want)
		}
	}
}

func TestConfigErrorFormat(t *testing.T) {
	ce := &ConfigError{Field: "PollInterval", Value: 0, Reason: "must be > 0"}
	msg := ce.Error()
	for _, want := range []string{"invalid config", "must be > 0", "PollInterval"} {
		if !strings.Contains(msg, want) {
			t.Errorf("Error()=%q missing %q", msg, want)
		}
	}
	if !errors.Is(ce, ErrInvalidConfig) {
		t.Error("should unwrap to ErrInvalidConfig")
	}
}

func TestContainsHelper(t *testing.T) {
	// The contains helper in detect.go is called indirectly in DetectFetcher;
	// cover it directly here since DetectFetcher needs a live ubus.
	if !contains([]string{"a", "b", "c"}, "b") {
		t.Error("should find 'b'")
	}
	if contains([]string{"a", "b"}, "z") {
		t.Error("should not find 'z'")
	}
	if contains(nil, "a") {
		t.Error("nil slice cannot contain anything")
	}
}

func TestIsIn172Boundary(t *testing.T) {
	// isIn172 (hostapd.go) — private RFC1918 range check for 172.16/12.
	cases := []struct {
		in   string
		want bool
	}{
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"172.15.0.1", false}, // just below range
		{"172.32.0.1", false}, // just above range
		{"10.0.0.1", false},
		{"not-an-ip", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isIn172(c.in); got != c.want {
			t.Errorf("isIn172(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestWithDecisionHandlerRegisters(t *testing.T) {
	// Smoke: option sets the callback without panicking.
	called := false
	w := New(WithDecisionHandler(func(Decision) { called = true }))
	if w == nil {
		t.Fatal("New returned nil")
	}
	// Emit a decision directly; unregistered path wouldn't call us.
	w.emitDecision(DecisionConnectEmitted, "aa", "")
	if !called {
		t.Error("handler not invoked after WithDecisionHandler")
	}
}

func TestDefaultHintSourceInvalidateCache(t *testing.T) {
	// invalidateCache resets the cache so next Hints() re-reads.
	src := &DefaultHintSource{CacheTTL: 1 * time.Hour}
	// First call populates the cache (even if empty).
	_ = src.Hints(contextTODO(t))
	src.invalidateCache()
	// Second call after invalidate should re-run (we can't observe it
	// directly, but it must not panic).
	_ = src.Hints(contextTODO(t))
}

func TestPackageLevelInvalidateHintsCache(t *testing.T) {
	// Package-level shim; just must not panic.
	invalidateHintsCache()
}

func TestEnsureFetcherShortCircuitsWhenFetcherSet(t *testing.T) {
	// When WithFetcher pre-populates the fetcher, EnsureFetcher is a
	// single sync.Once branch and returns nil immediately — no ubus call.
	type stubFetcher struct{}
	w := New(WithFetcher(stubFetcherFn(func(context.Context) ([]Device, error) {
		return nil, nil
	})))
	if err := w.EnsureFetcher(context.Background()); err != nil {
		t.Errorf("pre-set fetcher should make EnsureFetcher a no-op, got %v", err)
	}
	// Second call must also return nil (sync.Once seals the decision).
	if err := w.EnsureFetcher(context.Background()); err != nil {
		t.Errorf("second EnsureFetcher call: %v", err)
	}
	_ = stubFetcher{}
}

// stubFetcherFn is a Fetcher adapter for tests that don't need the
// full argustest harness.
type stubFetcherFn func(context.Context) ([]Device, error)

func (f stubFetcherFn) Fetch(ctx context.Context) ([]Device, error) { return f(ctx) }

func contextTODO(_ *testing.T) context.Context {
	return context.TODO()
}
