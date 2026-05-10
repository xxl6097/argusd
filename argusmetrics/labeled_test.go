package argusmetrics_test

import (
	"sync"
	"testing"

	argus "github.com/xxl6097/argus"
	"github.com/xxl6097/argus/argusmetrics"
)

func TestLabeledCountersBasicKeying(t *testing.T) {
	m := argusmetrics.NewLabeled([]string{"mac"}, func(d argus.Decision) []string {
		return []string{d.MAC}
	})

	m.OnDecision(argus.Decision{Kind: argus.DecisionConnectEmitted, MAC: "aa"})
	m.OnDecision(argus.Decision{Kind: argus.DecisionConnectEmitted, MAC: "aa"})
	m.OnDecision(argus.Decision{Kind: argus.DecisionConnectEmitted, MAC: "bb"})
	m.OnDecision(argus.Decision{Kind: argus.DecisionOfflineEmitted, MAC: "aa"})

	snap := m.Snapshot()
	if snap["CONNECT_EMIT|aa"] != 2 {
		t.Errorf("CONNECT_EMIT|aa = %d, want 2", snap["CONNECT_EMIT|aa"])
	}
	if snap["CONNECT_EMIT|bb"] != 1 {
		t.Errorf("CONNECT_EMIT|bb = %d, want 1", snap["CONNECT_EMIT|bb"])
	}
	if snap["OFFLINE_EMIT|aa"] != 1 {
		t.Errorf("OFFLINE_EMIT|aa = %d, want 1", snap["OFFLINE_EMIT|aa"])
	}
}

func TestLabeledCountersMultiLabel(t *testing.T) {
	m := argusmetrics.NewLabeled([]string{"ssid", "band"}, func(d argus.Decision) []string {
		// Derive from detail for this test; in production you'd look up
		// ssid/band from a parallel device registry.
		if d.MAC == "aa" {
			return []string{"home", "5G"}
		}
		return []string{"home", "2.4G"}
	})

	m.OnDecision(argus.Decision{Kind: argus.DecisionConnectEmitted, MAC: "aa"})
	m.OnDecision(argus.Decision{Kind: argus.DecisionConnectEmitted, MAC: "bb"})
	m.OnDecision(argus.Decision{Kind: argus.DecisionConnectEmitted, MAC: "bb"})

	snap := m.Snapshot()
	if snap["CONNECT_EMIT|home|5G"] != 1 {
		t.Errorf("5G = %d, want 1", snap["CONNECT_EMIT|home|5G"])
	}
	if snap["CONNECT_EMIT|home|2.4G"] != 2 {
		t.Errorf("2.4G = %d, want 2", snap["CONNECT_EMIT|home|2.4G"])
	}
}

func TestLabeledCountersArityMismatchDropped(t *testing.T) {
	// A broken extractor that returns the wrong arity must drop the
	// Decision silently (prevents cardinality leak from a buggy user
	// extractor).
	m := argusmetrics.NewLabeled([]string{"a", "b"}, func(d argus.Decision) []string {
		return []string{"only-one"}
	})
	m.OnDecision(argus.Decision{Kind: argus.DecisionConnectEmitted, MAC: "aa"})
	if len(m.Snapshot()) != 0 {
		t.Errorf("arity-mismatch decisions should drop, got %+v", m.Snapshot())
	}
}

func TestLabeledCountersNilExtractor(t *testing.T) {
	// nil extractor = plain kind keys, no label suffix.
	m := argusmetrics.NewLabeled(nil, nil)
	m.OnDecision(argus.Decision{Kind: argus.DecisionConnectEmitted})
	m.OnDecision(argus.Decision{Kind: argus.DecisionConnectEmitted})
	snap := m.Snapshot()
	if snap["CONNECT_EMIT"] != 2 {
		t.Errorf("got %+v", snap)
	}
}

func TestLabeledCountersConcurrentSafe(t *testing.T) {
	m := argusmetrics.NewLabeled([]string{"mac"}, func(d argus.Decision) []string {
		return []string{d.MAC}
	})

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				m.OnDecision(argus.Decision{Kind: argus.DecisionConnectEmitted, MAC: "aa"})
			}
		}()
	}
	wg.Wait()

	snap := m.Snapshot()
	if snap["CONNECT_EMIT|aa"] != 10000 {
		t.Errorf("concurrent accumulation off: got %d, want 10000", snap["CONNECT_EMIT|aa"])
	}
}

func TestLabeledCountersReset(t *testing.T) {
	m := argusmetrics.NewLabeled([]string{"mac"}, func(d argus.Decision) []string {
		return []string{d.MAC}
	})
	m.OnDecision(argus.Decision{Kind: argus.DecisionConnectEmitted, MAC: "aa"})
	m.Reset()
	if len(m.Snapshot()) != 0 {
		t.Errorf("Reset should clear all counters, got %+v", m.Snapshot())
	}
}

func TestLabeledCountersLabelNamesIsCopy(t *testing.T) {
	m := argusmetrics.NewLabeled([]string{"a", "b"}, nil)
	names := m.LabelNames()
	names[0] = "mutated"
	names2 := m.LabelNames()
	if names2[0] != "a" {
		t.Errorf("LabelNames should return a defensive copy, got %v", names2)
	}
}

// BenchmarkLabeledOnDecision — label path cost.
// Expect ~100-200 ns/op with 1 alloc (the joined key).
func BenchmarkLabeledOnDecision(b *testing.B) {
	m := argusmetrics.NewLabeled([]string{"mac"}, func(d argus.Decision) []string {
		return []string{d.MAC}
	})
	d := argus.Decision{Kind: argus.DecisionConnectEmitted, MAC: "aa"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.OnDecision(d)
	}
}
