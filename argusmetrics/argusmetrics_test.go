package argusmetrics_test

import (
	"sync"
	"testing"
	"time"

	argus "github.com/xxl6097/argusd"
	"github.com/xxl6097/argusd/argusmetrics"
)

func TestCountersOnDecision(t *testing.T) {
	m := argusmetrics.New()
	m.OnDecision(argus.Decision{Kind: argus.DecisionConnectEmitted, MAC: "aa"})
	m.OnDecision(argus.Decision{Kind: argus.DecisionConnectEmitted, MAC: "bb"})
	m.OnDecision(argus.Decision{Kind: argus.DecisionOfflineEmitted, MAC: "cc"})

	snap := m.Snapshot()
	if snap["CONNECT_EMIT"] != 2 {
		t.Errorf("CONNECT_EMIT=%d want 2", snap["CONNECT_EMIT"])
	}
	if snap["OFFLINE_EMIT"] != 1 {
		t.Errorf("OFFLINE_EMIT=%d want 1", snap["OFFLINE_EMIT"])
	}
	if _, exists := snap["_dropped"]; exists {
		t.Error("_dropped 不应出现在有效 kind 的情况下")
	}
}

func TestCountersOnEvent(t *testing.T) {
	m := argusmetrics.New()
	m.OnEvent(argus.Event{Kind: argus.EventOnline})
	m.OnEvent(argus.Event{Kind: argus.EventOnline})
	m.OnEvent(argus.Event{Kind: argus.EventOffline})

	snap := m.Snapshot()
	if snap["EVENT_ONLINE"] != 2 {
		t.Errorf("EVENT_ONLINE=%d", snap["EVENT_ONLINE"])
	}
	if snap["EVENT_OFFLINE"] != 1 {
		t.Errorf("EVENT_OFFLINE=%d", snap["EVENT_OFFLINE"])
	}
}

func TestCountersConcurrentSafe(t *testing.T) {
	m := argusmetrics.New()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				m.OnDecision(argus.Decision{Kind: argus.DecisionConnectEmitted})
			}
		}()
	}
	wg.Wait()
	snap := m.Snapshot()
	if snap["CONNECT_EMIT"] != 10000 {
		t.Errorf("并发累加出错: %d", snap["CONNECT_EMIT"])
	}
}

func TestCountersReset(t *testing.T) {
	m := argusmetrics.New()
	m.OnDecision(argus.Decision{Kind: argus.DecisionConnectEmitted})
	m.OnEvent(argus.Event{Kind: argus.EventOnline})
	m.Reset()
	if len(m.Snapshot()) != 0 {
		t.Errorf("Reset 后 Snapshot 应为空, got %+v", m.Snapshot())
	}
}

// BenchmarkOnDecision 验证 OnDecision 热路径零分配 / 纳秒级。
func BenchmarkOnDecision(b *testing.B) {
	m := argusmetrics.New()
	d := argus.Decision{Kind: argus.DecisionConnectEmitted, MAC: "aa", Time: time.Now()}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.OnDecision(d)
	}
}
