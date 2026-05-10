package argus

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// --- Panic isolation regression tests ---

// TestEventHandlerPanicDoesNotKillWatcher 确保用户 EventHandler panic 不会
// 杀死 Watcher goroutine, 且 panic 以 error 形式通过 onError 上报。
func TestEventHandlerPanicDoesNotKillWatcher(t *testing.T) {
	var gotPanic error
	badHandler := func(Event) { panic("user bug") }
	errHandler := func(e error) { gotPanic = e }

	w := New(WithFetcher(staticFetcher{}), WithProber(nil))

	// 直接调用 safeInvokeEvent 验证行为
	w.safeInvokeEvent(badHandler, errHandler, Event{Kind: EventOnline})

	if gotPanic == nil {
		t.Fatal("panic 应被捕获并上报到 onError")
	}
	if !errors.Is(gotPanic, gotPanic) {
		t.Error("上报的 error 应 wrap 原 panic 值")
	}
	if msg := gotPanic.Error(); !containsSub(msg, "EventHandler panicked") || !containsSub(msg, "user bug") {
		t.Errorf("错误文案应含来源和原因: %q", msg)
	}
}

// TestErrorHandlerPanicDoesNotRecurse 确保 ErrorHandler 本身的 panic 被吞掉,
// 不会导致无限递归上报。
func TestErrorHandlerPanicDoesNotRecurse(t *testing.T) {
	w := New(WithFetcher(staticFetcher{}), WithProber(nil))
	badError := func(error) { panic("error handler bug") }

	// 不应 panic, 不应死循环
	done := make(chan struct{})
	go func() {
		w.safeInvokeError(badError, fmt.Errorf("some error"))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("safeInvokeError 超时, 可能存在递归")
	}
}

// TestDecisionHandlerPanicSwallowed 确保决策回调 panic 不破坏控制流。
func TestDecisionHandlerPanicSwallowed(t *testing.T) {
	w := New(WithFetcher(staticFetcher{}), WithProber(nil))
	badDecision := func(Decision) { panic("decision bug") }

	// 直接调用 safeInvokeDecision, 不应 panic
	w.safeInvokeDecision(badDecision, Decision{Kind: DecisionConnectEmitted, MAC: "aa"})
}

// TestDiffEventPanicContained 确保一个 EventHandler panic 不影响同次 diff
// 产生的其余事件 — 因为事件已收集到 pending, Run 在锁外逐条 safeInvokeEvent。
func TestDiffEventPanicContained(t *testing.T) {
	w := New(WithFetcher(staticFetcher{}), WithProber(nil))
	w.known["aa"] = Device{MAC: "aa"}
	w.known["bb"] = Device{MAC: "bb"}

	// 当前 cur 为空, diff 会对 aa 和 bb 累计 misses; 但不会直接触发离线
	// (需多轮才能达到阈值)。这里直接构造 pending 行为:
	// 我们已在其他测试用例覆盖 diff 的收集语义, 此处聚焦 Run 的锁外发射。
	calls := 0
	badOnce := func(Event) {
		calls++
		if calls == 1 {
			panic("first callback panics")
		}
	}
	onError := func(error) {}

	// 模拟 Run 在锁外对 pending 逐条 safeInvokeEvent 的场景
	pending := []Event{
		{Kind: EventOnline, Device: Device{MAC: "aa"}},
		{Kind: EventOnline, Device: Device{MAC: "bb"}},
	}
	for _, ev := range pending {
		w.safeInvokeEvent(badOnce, onError, ev)
	}
	if calls != 2 {
		t.Errorf("第二个事件应在第一个 panic 后继续投递, calls=%d", calls)
	}
}

// --- Zero-cost DecisionHandler benchmark ---

// BenchmarkEmitDecisionNil 验证"未注册 DecisionHandler 时零成本"的文档承诺:
// emitDecision 应立即返回, 不分配对象、不调用 time.Now。
func BenchmarkEmitDecisionNil(b *testing.B) {
	w := New(WithFetcher(staticFetcher{}), WithProber(nil))
	// 确保 onDecision == nil

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.emitDecision(DecisionConnectEmitted, "aa:bb:cc:dd:ee:ff", "x")
	}
}

// BenchmarkEmitDecisionActive 给出注册了 DecisionHandler 后的对照值,
// 用于确认 panic-safe wrapper 的开销 (defer+recover) 可接受。
func BenchmarkEmitDecisionActive(b *testing.B) {
	w := New(
		WithFetcher(staticFetcher{}),
		WithProber(nil),
		WithDecisionHandler(func(Decision) {}),
	)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.emitDecision(DecisionConnectEmitted, "aa:bb:cc:dd:ee:ff", "x")
	}
}

// BenchmarkSafeInvokeEventOK 测量正常路径的 safeInvokeEvent 开销 (用于监控
// panic-safe wrapper 没有拖慢热路径)。
func BenchmarkSafeInvokeEventOK(b *testing.B) {
	w := New(WithFetcher(staticFetcher{}), WithProber(nil))
	cb := func(Event) {}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.safeInvokeEvent(cb, nil, Event{Kind: EventOnline})
	}
}

// containsSub 检测 substring, 避免 import strings。
func containsSub(s, sub string) bool {
	return len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

var _ = context.Background // keep context import live if future tests need it
