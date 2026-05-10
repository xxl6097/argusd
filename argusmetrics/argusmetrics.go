// Package argusmetrics provides in-process metric counters for Argus
// decision traces, without pulling in any third-party metrics library.
//
// The design deliberately avoids a direct dependency on Prometheus: Argus
// stays pure stdlib, and consumers bridge to Prometheus (or OpenTelemetry,
// StatsD, etc.) in ~10 lines with the provided Counters snapshot.
//
// Typical usage:
//
//	import (
//	    argus "github.com/xxl6097/argus"
//	    "github.com/xxl6097/argus/argusmetrics"
//	)
//
//	m := argusmetrics.New()
//	w := argus.New(
//	    argus.WithDecisionHandler(m.OnDecision),
//	)
//	// ... later, bridge to Prometheus:
//	snap := m.Snapshot()
//	promOnlineCounter.Add(float64(snap["CONNECT_EMIT"]))
//
// Zero allocations on the hot path: OnDecision uses an atomic increment
// on a fixed-size array indexed by DecisionKind, no map lookup per call.
package argusmetrics

import (
	"sync"
	"sync/atomic"

	argus "github.com/xxl6097/argus"
)

// maxKind 是 Counters 内部数组大小的上限, 涵盖当前所有 DecisionKind 值
// (最大 43), 留出增长空间。
const maxKind = 128

// Counters 按 DecisionKind 聚合决策次数, 并发安全, 零分配。
//
// OnDecision 方法满足 argus.DecisionHandler 签名, 可直接作为
// WithDecisionHandler 的参数使用。
type Counters struct {
	counts [maxKind]uint64

	// eventCounts 额外统计业务级 Event (需要 OnEvent 手工注册),
	// 按 EventKind 整数值索引, 3 + 1 个槽位足够。
	eventCounts [8]uint64

	// dropped 记录索引越界的 DecisionKind (防御性, 未来若新增
	// kind 超过 maxKind 时不会 panic)。
	dropped atomic.Uint64

	// snapshotLock 仅 Snapshot 读快照时用 (避免与写并发产生 slice 创建的
	// 内存开销); 实际计数用 atomic, 不加锁。
	snapshotLock sync.Mutex
}

// New 创建零值 Counters。
func New() *Counters {
	return &Counters{}
}

// OnDecision 原子累加对应 DecisionKind 的计数。可直接用作
// argus.DecisionHandler: WithDecisionHandler(m.OnDecision)。
//
// 热路径: 1 次原子加, 0 分配。
func (c *Counters) OnDecision(d argus.Decision) {
	k := int(d.Kind)
	if k < 0 || k >= maxKind {
		c.dropped.Add(1)
		return
	}
	atomic.AddUint64(&c.counts[k], 1)
}

// OnEvent 原子累加对应 EventKind 的计数。可直接用作 argus.EventHandler
// 的一部分 (需要包装, 因为 EventHandler 签名也要转发给业务回调)。
func (c *Counters) OnEvent(e argus.Event) {
	k := int(e.Kind)
	if k < 0 || k >= len(c.eventCounts) {
		return
	}
	atomic.AddUint64(&c.eventCounts[k], 1)
}

// Snapshot 返回当前所有计数的快照, 按 DecisionKind 的稳定字符串
// (如 "CONNECT_EMIT" / "OFFLINE_EMIT") 索引。
//
// 典型用法: 桥接到 Prometheus / OpenTelemetry 前取快照。
func (c *Counters) Snapshot() map[string]uint64 {
	c.snapshotLock.Lock()
	defer c.snapshotLock.Unlock()

	out := make(map[string]uint64, 32)
	for i := 0; i < maxKind; i++ {
		v := atomic.LoadUint64(&c.counts[i])
		if v == 0 {
			continue
		}
		name := argus.DecisionKind(i).String()
		if name == "DECISION_UNKNOWN" {
			continue
		}
		out[name] = v
	}
	// Event 计数用带 "EVENT_" 前缀避免与决策冲突
	for i := 0; i < len(c.eventCounts); i++ {
		v := atomic.LoadUint64(&c.eventCounts[i])
		if v == 0 {
			continue
		}
		name := argus.EventKind(i).String()
		if name == "UNKNOWN" {
			continue
		}
		out["EVENT_"+name] = v
	}
	if d := c.dropped.Load(); d > 0 {
		out["_dropped"] = d
	}
	return out
}

// Reset 将所有计数器归零 (主要用于测试)。
func (c *Counters) Reset() {
	for i := range c.counts {
		atomic.StoreUint64(&c.counts[i], 0)
	}
	for i := range c.eventCounts {
		atomic.StoreUint64(&c.eventCounts[i], 0)
	}
	c.dropped.Store(0)
}
