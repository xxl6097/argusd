package argusmetrics_test

import (
	"fmt"
	"sort"

	argus "github.com/xxl6097/argus"
	"github.com/xxl6097/argus/argusmetrics"
)

// ExampleCounters shows how to wire argusmetrics into a Watcher and later
// bridge the snapshot to a third-party metrics backend (Prometheus, OTel,
// StatsD) without adding a direct dependency.
func ExampleCounters() {
	m := argusmetrics.New()

	// 1) Register as argus DecisionHandler (zero-alloc hot path).
	w := argus.New(
		argus.WithDecisionHandler(m.OnDecision),
	)
	_ = w

	// 2) Simulate traffic (in real code these come from the Watcher).
	m.OnDecision(argus.Decision{Kind: argus.DecisionConnectEmitted})
	m.OnDecision(argus.Decision{Kind: argus.DecisionConnectEmitted})
	m.OnDecision(argus.Decision{Kind: argus.DecisionOfflineEmitted})
	m.OnEvent(argus.Event{Kind: argus.EventOnline})

	// 3) Periodically snapshot and bridge to your metrics system.
	snap := m.Snapshot()

	keys := make([]string, 0, len(snap))
	for k := range snap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("%s = %d\n", k, snap[k])
	}
	// Output:
	// CONNECT_EMIT = 2
	// EVENT_ONLINE = 1
	// OFFLINE_EMIT = 1
}
