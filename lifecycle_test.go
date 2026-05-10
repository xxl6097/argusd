package argus

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestRunConcurrentReturnsAlreadyRunning 确保同一 Watcher 同时只能有一个 Run,
// 第二个并发调用返回 ErrAlreadyRunning。
func TestRunConcurrentReturnsAlreadyRunning(t *testing.T) {
	w := New(WithFetcher(staticFetcher{}), WithProber(nil))

	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()

	var wg sync.WaitGroup
	var err1, err2 error

	wg.Add(2)
	go func() {
		defer wg.Done()
		err1 = w.Run(ctx1, func(Event) {}, nil)
	}()
	// 等第一个 Run 进入 (CAS 成功)
	time.Sleep(10 * time.Millisecond)
	go func() {
		defer wg.Done()
		err2 = w.Run(context.Background(), func(Event) {}, nil)
	}()

	// 等第二个 Run 返回
	time.Sleep(10 * time.Millisecond)
	cancel1()
	wg.Wait()

	if !errors.Is(err2, ErrAlreadyRunning) {
		t.Errorf("第二个并发 Run 应返回 ErrAlreadyRunning, got %v", err2)
	}
	// err1 应是 nil (正常 ctx cancel)
	if err1 != nil {
		t.Errorf("第一个 Run 应正常退出, got %v", err1)
	}
}

// TestStopIdempotent 确保连续两次 Stop 安全 (第二次立即返回 nil)。
func TestStopIdempotent(t *testing.T) {
	w := New(WithFetcher(staticFetcher{}), WithProber(nil))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go w.Run(ctx, func(Event) {}, nil)
	time.Sleep(10 * time.Millisecond) // 等 Run 启动

	stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
	defer stopCancel()

	if err := w.Stop(stopCtx); err != nil {
		t.Fatalf("第一次 Stop 失败: %v", err)
	}
	if err := w.Stop(stopCtx); err != nil {
		t.Errorf("第二次 Stop 应幂等返回 nil, got %v", err)
	}
}

// TestStopBeforeRun 确保从未 Run 就 Stop, 返回 nil (幂等)。
func TestStopBeforeRun(t *testing.T) {
	w := New(WithFetcher(staticFetcher{}), WithProber(nil))
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := w.Stop(stopCtx); err != nil {
		t.Errorf("Stop 在 Run 之前应返回 nil, got %v", err)
	}
}

// TestRunAfterStopSucceeds 确保 Stop 后可以再次 Run (重启语义)。
func TestRunAfterStopSucceeds(t *testing.T) {
	w := New(WithFetcher(staticFetcher{}), WithProber(nil))

	// 第一轮 Run
	ctx1, cancel1 := context.WithCancel(context.Background())
	go w.Run(ctx1, func(Event) {}, nil)
	time.Sleep(10 * time.Millisecond)

	stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
	defer stopCancel()
	if err := w.Stop(stopCtx); err != nil {
		t.Fatalf("Stop 失败: %v", err)
	}
	cancel1()

	// 第二轮 Run 应成功
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	done := make(chan error, 1)
	go func() { done <- w.Run(ctx2, func(Event) {}, nil) }()

	time.Sleep(10 * time.Millisecond)
	cancel2()
	if err := <-done; err != nil {
		t.Errorf("第二轮 Run 应成功, got %v", err)
	}
}

// TestRestartPreservesKnownAndCooldown 确保重启后 known / offlineCooldown 保留,
// 不会对已知设备重新触发 EventOnline。
func TestRestartPreservesKnownAndCooldown(t *testing.T) {
	w := New(WithFetcher(staticFetcher{}), WithProber(nil))

	// 第一轮: 手工注入 known + cooldown (在 Run 之前)
	w.known["aa:bb:cc:dd:ee:ff"] = Device{MAC: "aa:bb:cc:dd:ee:ff", IP: "1.1.1.1"}
	w.offlineCooldown["aa:bb:cc:dd:ee:ff"] = time.Now()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events := make(chan Event, 10)
	runDone := make(chan struct{})
	go func() {
		w.Run(ctx, func(e Event) { events <- e }, nil)
		close(runDone)
	}()
	time.Sleep(50 * time.Millisecond)

	stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
	defer stopCancel()
	w.Stop(stopCtx)
	cancel()
	<-runDone

	// 第二轮 Run: 同 MAC 不应触发 Online (因为 known 保留)
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	runDone2 := make(chan struct{})
	go func() {
		w.Run(ctx2, func(e Event) { events <- e }, nil)
		close(runDone2)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel2()
	<-runDone2

	// 检查 events: 不应有 aa:bb:cc:dd:ee:ff 的 EventOnline
	close(events)
	for e := range events {
		if e.Kind == EventOnline && e.Device.MAC == "aa:bb:cc:dd:ee:ff" {
			t.Error("重启后已知设备不应触发 EventOnline")
		}
	}

	// 验证 known / cooldown 仍存在
	if _, ok := w.known["aa:bb:cc:dd:ee:ff"]; !ok {
		t.Error("known 应在重启后保留")
	}
	if _, ok := w.offlineCooldown["aa:bb:cc:dd:ee:ff"]; !ok {
		t.Error("offlineCooldown 应在重启后保留")
	}
}

// TestRestartResetsTransients 确保重启时 misses / disconnectInFlight / droppedHints 被重置。
func TestRestartResetsTransients(t *testing.T) {
	w := New(WithFetcher(staticFetcher{}), WithProber(nil))

	// 第一轮: 手工注入瞬态 (在 Run 之前)
	w.misses["aa"] = 3
	w.disconnectInFlight["bb"] = struct{}{}
	atomic.StoreUint64(&w.droppedHints, 99)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runDone := make(chan struct{})
	go func() {
		w.Run(ctx, func(Event) {}, nil)
		close(runDone)
	}()
	time.Sleep(10 * time.Millisecond)

	stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
	defer stopCancel()
	w.Stop(stopCtx)
	cancel()
	<-runDone

	// 验证瞬态已重置 (Run 入口重置)
	if len(w.misses) != 0 {
		t.Errorf("misses 应在重启时重置, got %+v", w.misses)
	}
	if len(w.disconnectInFlight) != 0 {
		t.Errorf("disconnectInFlight 应在重启时重置, got %+v", w.disconnectInFlight)
	}
	if atomic.LoadUint64(&w.droppedHints) != 0 {
		t.Errorf("droppedHints 应在重启时重置, got %d", w.droppedHints)
	}
}

// slowProber 模拟一个慢 ping: Reachable 阻塞 duration 或直到 ctx 取消 (但只要 Reachable
// 返回, Stop 的 runWG.Wait 就必须等到它)。
type slowProber struct {
	duration time.Duration
	started  chan struct{}
}

func (p *slowProber) Reachable(ctx context.Context, ip string) bool {
	select {
	case p.started <- struct{}{}:
	default:
	}
	// 故意不响应 ctx.Done() — 模拟真实 ping 在 subprocess 里不能快速取消的场景
	time.Sleep(p.duration)
	return false
}

// TestStopWaitsForDisconnectWorker 确保 Stop 等待 in-flight hint worker 完成。
// 用 slowProber 让 worker 的 prober.Reachable 阻塞 500ms, 此时 Stop 的 runWG.Wait
// 必须等到这个 worker 返回。
func TestStopWaitsForDisconnectWorker(t *testing.T) {
	prober := &slowProber{duration: 500 * time.Millisecond, started: make(chan struct{}, 1)}
	w := New(WithFetcher(staticFetcher{}), WithProber(prober))
	w.known["aa:bb:cc:dd:ee:ff"] = Device{MAC: "aa:bb:cc:dd:ee:ff", IP: "1.1.1.1"}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runDone := make(chan struct{})
	go func() {
		w.Run(ctx, func(Event) {}, nil)
		close(runDone)
	}()
	time.Sleep(30 * time.Millisecond)

	// 注入 disconnect hint
	w.stateMu.Lock()
	hints := w.syslogHints
	w.stateMu.Unlock()
	hints <- syslogHint{MAC: "aa:bb:cc:dd:ee:ff", Disconnect: true}

	// handleDisconnectHint 先 Sleep 500ms (响应 ctx.Done()), 再调用 prober.Reachable。
	// 等 prober 被调用 (started channel 有信号) 才触发 Stop, 此时 worker 已跨过
	// Sleep 阶段, ctx 取消不能让它立刻退出, 必须等 prober 的 500ms 返回。
	select {
	case <-prober.started:
	case <-time.After(2 * time.Second):
		t.Fatal("prober 未被调用")
	}

	// 在 prober 阻塞期间调用 Stop, 测量耗时
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer stopCancel()

	start := time.Now()
	if err := w.Stop(stopCtx); err != nil {
		t.Fatalf("Stop 应等待 worker 完成, got %v", err)
	}
	elapsed := time.Since(start)

	// prober 阻塞 500ms, Stop 至少应等 300ms 才返回 (留些误差)
	if elapsed < 300*time.Millisecond {
		t.Errorf("Stop 过早返回 (%.0fms), 应等待 prober 完成", float64(elapsed)/float64(time.Millisecond))
	}

	cancel()
	<-runDone
}

// TestStopWithTimeout 确保 Stop 的 ctx 超时时返回 context.DeadlineExceeded。
func TestStopWithTimeout(t *testing.T) {
	// 用 slowProber 让 worker 阻塞 500ms 不响应 ctx.Done(), Stop 的 10ms timeout 必超时。
	prober := &slowProber{duration: 500 * time.Millisecond, started: make(chan struct{}, 1)}
	w := New(WithFetcher(staticFetcher{}), WithProber(prober))
	w.known["aa:bb:cc:dd:ee:ff"] = Device{MAC: "aa:bb:cc:dd:ee:ff", IP: "1.1.1.1"}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runDone := make(chan struct{})
	go func() {
		w.Run(ctx, func(Event) {}, nil)
		close(runDone)
	}()
	time.Sleep(30 * time.Millisecond)

	// 注入 disconnect hint, 等 prober 被调用
	w.stateMu.Lock()
	hints := w.syslogHints
	w.stateMu.Unlock()
	hints <- syslogHint{MAC: "aa:bb:cc:dd:ee:ff", Disconnect: true}

	select {
	case <-prober.started:
	case <-time.After(2 * time.Second):
		t.Fatal("prober 未被调用")
	}

	// Stop ctx 仅 10ms, prober 需要 500ms 不响应 ctx.Done, 必超时
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer stopCancel()

	err := w.Stop(stopCtx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Stop 超时应返回 context.DeadlineExceeded, got %v", err)
	}

	// 清理: 等 prober 返回 + Run 退出
	cancel()
	select {
	case <-runDone:
	case <-time.After(3 * time.Second):
		t.Fatal("Run 未按时退出")
	}
}

// TestGoroutineLeakOnRestart 确保 30 轮 Run/Stop 循环后 goroutine 数量稳定。
func TestGoroutineLeakOnRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过 goroutine leak 测试 (耗时)")
	}

	w := New(WithFetcher(staticFetcher{}), WithProber(nil))

	runtime.GC()
	time.Sleep(100 * time.Millisecond)
	before := runtime.NumGoroutine()

	for i := 0; i < 30; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		runDone := make(chan struct{})
		go func() {
			w.Run(ctx, func(Event) {}, nil)
			close(runDone)
		}()
		time.Sleep(10 * time.Millisecond)

		stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
		w.Stop(stopCtx)
		stopCancel()
		cancel()
		<-runDone
	}

	runtime.GC()
	time.Sleep(100 * time.Millisecond)
	after := runtime.NumGoroutine()

	// 允许 ±5 个 goroutine 的误差 (runtime 自身的波动)
	if after > before+5 {
		t.Errorf("goroutine 泄漏: before=%d, after=%d (增长 %d)", before, after, after-before)
	}
}
