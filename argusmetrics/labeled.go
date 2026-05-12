package argusmetrics

import (
	"strings"
	"sync"

	argus "github.com/xxl6097/argusd"
)

// LabelExtractor pulls label values from a Decision in the order
// declared when constructing the LabeledCounters. It is called on every
// Decision, so implementations must be cheap and allocation-free in the
// common case.
//
// Example extractor for per-SSID per-band counters:
//
//	func extract(d argus.Decision) []string {
//	    // Decision currently carries no SSID/band fields directly, so
//	    // production code would read them from a parallel device
//	    // registry keyed by d.MAC. The example keeps the contract tight.
//	    return []string{lookupSSID(d.MAC), lookupBand(d.MAC)}
//	}
type LabelExtractor func(argus.Decision) []string

// LabeledCounters aggregates decision counts across a user-supplied
// label tuple. The zero-dep Prometheus-style `CounterVec` equivalent,
// minus the Prometheus dependency.
//
// Keys in the Snapshot output are "<decision_kind>|<label1>|<label2>..."
// so consumers can split on "|" at export time if their backend wants
// structured labels.
//
// Thread-safe via a single internal mutex. If you have extreme
// cardinality requirements (> 10 K label combinations at > 1 K Hz),
// prefer maintaining your own sharded counters in a custom
// DecisionHandler — LabeledCounters trades some throughput for
// simplicity.
type LabeledCounters struct {
	labels   []string
	extract  LabelExtractor
	mu       sync.Mutex
	counters map[string]uint64
}

// NewLabeled constructs a LabeledCounters aggregator.
//
// labels is the ordered list of label names (used in Snapshot keys +
// LabelNames). extract returns values in the same order. If extract
// returns a slice of a different length, the Decision is silently
// dropped (prevents cardinality leaks from a broken extractor).
//
// A nil extractor is treated as "no labels" — equivalent to the
// unlabeled Counters type.
func NewLabeled(labels []string, extract LabelExtractor) *LabeledCounters {
	copied := make([]string, len(labels))
	copy(copied, labels)
	return &LabeledCounters{
		labels:   copied,
		extract:  extract,
		counters: make(map[string]uint64, 16),
	}
}

// OnDecision satisfies argus.DecisionHandler. Extracts the label
// values from the Decision and increments the counter for
// (DecisionKind, labels...) by 1.
//
// NOT zero-alloc: the mutex + map lookup path is ~100 ns/op with one
// alloc for the joined key (the string builder). If that matters to
// your hot path, wrap the unlabeled Counters instead.
func (c *LabeledCounters) OnDecision(d argus.Decision) {
	if c == nil {
		return
	}
	var vals []string
	if c.extract != nil {
		vals = c.extract(d)
		if len(vals) != len(c.labels) {
			return // extractor returned wrong arity; drop
		}
	}
	key := buildKey(d.Kind.String(), vals)

	c.mu.Lock()
	c.counters[key]++
	c.mu.Unlock()
}

// Snapshot returns a point-in-time copy of all counter values.
// Keys are "<decision_kind>|<label1>|<label2>..." (empty trailing
// segment when no labels are configured).
func (c *LabeledCounters) Snapshot() map[string]uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()

	out := make(map[string]uint64, len(c.counters))
	for k, v := range c.counters {
		out[k] = v
	}
	return out
}

// LabelNames returns a defensive copy of the label names in their
// declared order. Useful for emitting to Prometheus where the label
// set has to be declared upfront.
func (c *LabeledCounters) LabelNames() []string {
	out := make([]string, len(c.labels))
	copy(out, c.labels)
	return out
}

// Reset zeroes all counters. Mainly for tests.
func (c *LabeledCounters) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.counters {
		delete(c.counters, k)
	}
}

// buildKey produces the "kind|v1|v2|..." Snapshot key. When there are
// no label values, the key is just the kind (no trailing "|").
func buildKey(kind string, vals []string) string {
	if len(vals) == 0 {
		return kind
	}
	var sb strings.Builder
	sb.Grow(len(kind) + sumLen(vals) + len(vals))
	sb.WriteString(kind)
	for _, v := range vals {
		sb.WriteByte('|')
		sb.WriteString(v)
	}
	return sb.String()
}

func sumLen(ss []string) int {
	n := 0
	for _, s := range ss {
		n += len(s)
	}
	return n
}
