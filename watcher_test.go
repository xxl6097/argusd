package argus

import (
	"context"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	c := DefaultConfig()
	if c.PollInterval <= 0 || c.OfflineMisses <= 0 || c.FetchTimeout <= 0 {
		t.Errorf("DefaultConfig 字段应均为正值: %+v", c)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("DefaultConfig 应通过校验, err=%v", err)
	}
}

func TestConfigValidate(t *testing.T) {
	bad := []Config{
		{PollInterval: 0, OfflineMisses: 5, FetchTimeout: time.Second},
		{PollInterval: time.Second, OfflineMisses: 0, FetchTimeout: time.Second},
		{PollInterval: -1, OfflineMisses: 5, FetchTimeout: time.Second},
		{PollInterval: time.Second, OfflineMisses: -1, FetchTimeout: time.Second},
	}
	for i, c := range bad {
		if err := c.Validate(); err == nil {
			t.Errorf("case %d 应校验失败: %+v", i, c)
		}
	}

	// 测试 Config 的 RSSI 相关校验
	rssiBad := []Config{
		// CooldownReleaseRSSI 为正 (非法)
		func() Config { c := DefaultConfig(); c.CooldownReleaseRSSI = 10; return c }(),
		// WeakRSSI 为正
		func() Config { c := DefaultConfig(); c.WeakRSSI = 1; return c }(),
		// ExtremelyWeakRSSI >= WeakRSSI (非法: 极弱应严格低于弱)
		func() Config { c := DefaultConfig(); c.ExtremelyWeakRSSI = -70; c.WeakRSSI = -80; return c }(),
		// MissThreshold 为 0
		func() Config { c := DefaultConfig(); c.WeakMissThreshold = 0; return c }(),
		func() Config { c := DefaultConfig(); c.ExtremelyWeakMissThreshold = 0; return c }(),
	}
	for i, c := range rssiBad {
		if err := c.Validate(); err == nil {
			t.Errorf("rssiBad case %d 应校验失败: %+v", i, c)
		}
	}
}

func TestWithConfigOverridesRSSIFields(t *testing.T) {
	w := New(WithConfig(Config{
		OfflineCooldown:     30 * time.Second,
		CooldownReleaseRSSI: -55,
		WeakRSSI:            -70,
		ExtremelyWeakRSSI:   -85,
		WeakMissThreshold:   3,
	}))
	if w.cfg.OfflineCooldown != 30*time.Second {
		t.Errorf("OfflineCooldown = %s, want 30s", w.cfg.OfflineCooldown)
	}
	if w.cfg.CooldownReleaseRSSI != -55 {
		t.Errorf("CooldownReleaseRSSI = %d", w.cfg.CooldownReleaseRSSI)
	}
	if w.cfg.WeakRSSI != -70 {
		t.Errorf("WeakRSSI = %d", w.cfg.WeakRSSI)
	}
	// 未设置的字段保留默认
	if w.cfg.ExtremelyWeakMissThreshold != 2 {
		t.Errorf("ExtremelyWeakMissThreshold 零值应保留默认 2, got %d", w.cfg.ExtremelyWeakMissThreshold)
	}
}

func TestDiffCustomCooldownDuration(t *testing.T) {
	// 自定义冷却期很短 (1ns), 冷却记录应立即过期
	cooldown := map[string]time.Time{
		"aa": time.Now().Add(-time.Millisecond), // 1ms 前
	}
	cfg := DefaultConfig()
	cfg.OfflineCooldown = time.Nanosecond // 极短冷却期

	diff(map[string]Device{}, map[string]Device{}, map[string]int{},
		map[string]Device{}, map[string]Device{}, cfg,
		newDiffCtx(), nil, cooldown, map[string]lastEvent{}, func(Event) {}, nil)
	if _, ok := cooldown["aa"]; ok {
		t.Error("冷却期=1ns 时所有记录应被清除")
	}
}

func TestDiffCustomWeakThreshold(t *testing.T) {
	// 自定义阈值: WeakRSSI=-70, WeakMissThreshold=2 → 连续 2 次 RSSI<-70 触发离线
	d := Device{MAC: "aa", IP: "1.1.1.1", RSSI: -75}
	known := map[string]Device{"aa": d}
	misses := map[string]int{}
	cooldown := map[string]time.Time{}
	apSet := map[string]Device{"aa": d}
	p := fakeProber{reachable: map[string]bool{}} // ping 全失败
	col := &eventCollector{}

	cfg := DefaultConfig()
	cfg.WeakRSSI = -70
	cfg.WeakMissThreshold = 2

	diff(known, map[string]Device{}, misses, apSet, apSet, cfg, newDiffCtx(), p, cooldown, map[string]lastEvent{}, col.emit, nil)
	if len(col.events) != 0 {
		t.Error("第 1 次不应触发")
	}
	diff(known, map[string]Device{}, misses, apSet, apSet, cfg, newDiffCtx(), p, cooldown, map[string]lastEvent{}, col.emit, nil)
	if len(col.events) != 1 || col.events[0].Kind != EventOffline {
		t.Errorf("第 2 次应触发 EventOffline, got %+v", col.events)
	}
}

func TestDiffFlapSuppressionBlocksRepeatOffline(t *testing.T) {
	// 中等信号设备 (RSSI > WeakRSSI) 在 FlapSuppressionWindow 内连续两次离线时, 第二次应被压制
	d := Device{MAC: "aa", IP: "1.1.1.1", RSSI: -50}
	known := map[string]Device{"aa": d}
	misses := map[string]int{}
	cooldown := map[string]time.Time{}
	lastAt := map[string]lastEvent{
		"aa": {at: time.Now().Add(-5 * time.Second), kind: EventOffline},
	}
	emptyCur := map[string]Device{}
	emptyRaw := map[string]Device{}
	col := &eventCollector{}

	// 走默认 miss 计数路径, 连续 5 次未发现
	for i := 0; i < 5; i++ {
		diff(known, emptyCur, misses, emptyRaw, emptyRaw, DefaultConfig(), newDiffCtx(), nil, cooldown, lastAt, col.emit, nil)
	}
	if len(col.events) != 0 {
		t.Errorf("5s 前刚离线, FlapSuppressionWindow (30s) 内应压制, got %+v", col.events)
	}
}

func TestDiffFlapSuppressionAllowsAfterWindow(t *testing.T) {
	// 超出 FlapSuppressionWindow 后同类事件应正常触发
	d := Device{MAC: "aa", IP: "1.1.1.1"}
	known := map[string]Device{"aa": d}
	misses := map[string]int{}
	cooldown := map[string]time.Time{}
	// 上次事件在 60s 前, 默认窗口 30s
	lastAt := map[string]lastEvent{
		"aa": {at: time.Now().Add(-60 * time.Second), kind: EventOffline},
	}
	emptyCur := map[string]Device{}
	emptyRaw := map[string]Device{}
	col := &eventCollector{}

	for i := 0; i < 5; i++ {
		diff(known, emptyCur, misses, emptyRaw, emptyRaw, DefaultConfig(), newDiffCtx(), nil, cooldown, lastAt, col.emit, nil)
	}
	if len(col.events) != 1 || col.events[0].Kind != EventOffline {
		t.Errorf("超窗口后应正常触发 EventOffline, got %+v", col.events)
	}
}

func TestDiffFlapSuppressionDisabled(t *testing.T) {
	// FlapSuppressionWindow=0 时不压制
	d := Device{MAC: "aa", IP: "1.1.1.1"}
	known := map[string]Device{"aa": d}
	misses := map[string]int{}
	cooldown := map[string]time.Time{}
	lastAt := map[string]lastEvent{
		"aa": {at: time.Now(), kind: EventOffline},
	}
	emptyCur := map[string]Device{}
	col := &eventCollector{}

	cfg := DefaultConfig()
	cfg.FlapSuppressionWindow = 0 // 关闭

	for i := 0; i < 5; i++ {
		diff(known, emptyCur, misses, emptyCur, emptyCur, cfg, newDiffCtx(), nil, cooldown, lastAt, col.emit, nil)
	}
	if len(col.events) != 1 {
		t.Errorf("FlapSuppressionWindow=0 时不应压制, got %d events", len(col.events))
	}
}

func TestDiffFlapSuppressionDifferentKindNotBlocked(t *testing.T) {
	// 上次是 Offline, 这次触发 Online, 不同类型应不被压制
	lastAt := map[string]lastEvent{
		"aa": {at: time.Now(), kind: EventOffline},
	}
	cur := map[string]Device{
		"aa": {MAC: "aa", IP: "1.1.1.1", Radio: "5G", RSSI: -30},
	}
	col := &eventCollector{}

	diff(map[string]Device{}, cur, map[string]int{},
		cur, cur, DefaultConfig(), newDiffCtx(), nil, map[string]time.Time{}, lastAt, col.emit, nil)
	if len(col.events) != 1 || col.events[0].Kind != EventOnline {
		t.Errorf("不同类型事件不应被压制, got %+v", col.events)
	}
}

func TestChangedFields(t *testing.T) {
	cases := []struct {
		name      string
		prev, cur Device
		wantLen   int
		wantField string
	}{
		{"无变化", Device{IP: "1", Hostname: "h"}, Device{IP: "1", Hostname: "h"}, 0, ""},
		{"IP 变", Device{IP: "1"}, Device{IP: "2"}, 1, "IP"},
		{"空->非空 Hostname 触发", Device{Hostname: ""}, Device{Hostname: "h"}, 1, "Hostname"},
		{"非空->空 Hostname 不触发", Device{Hostname: "h"}, Device{Hostname: ""}, 0, ""},
		{"Radio 变", Device{Radio: "5G"}, Device{Radio: "2.4G"}, 1, "Radio"},
		{"SSID 变", Device{SSID: "a"}, Device{SSID: "b"}, 1, "SSID"},
		{"RSSI 变不触发", Device{RSSI: -40}, Device{RSSI: -60}, 0, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cs := changedFields(c.prev, c.cur)
			if len(cs) != c.wantLen {
				t.Errorf("len(changes) = %d, want %d (changes=%+v)", len(cs), c.wantLen, cs)
			}
			if c.wantLen > 0 && cs[0].Field != c.wantField {
				t.Errorf("第一个变化 Field = %q, want %q", cs[0].Field, c.wantField)
			}
		})
	}
}

// --- diff 函数核心场景测试 ---

// 收集器: 聚合 onEvent 回调
type eventCollector struct{ events []Event }

func (e *eventCollector) emit(ev Event) { e.events = append(e.events, ev) }

func newDiffCtx() context.Context { return context.Background() }

func TestDiffNewDeviceTriggersOnline(t *testing.T) {
	known := map[string]Device{}
	misses := map[string]int{}
	cooldown := map[string]time.Time{}
	cur := map[string]Device{
		"aa": {MAC: "aa", IP: "1.1.1.1", Radio: "5G"},
	}
	col := &eventCollector{}
	diff(known, cur, misses, cur, cur, DefaultConfig(), newDiffCtx(), nil, cooldown, map[string]lastEvent{}, col.emit, nil)

	if len(col.events) != 1 || col.events[0].Kind != EventOnline {
		t.Errorf("新设备应产生 EventOnline, got %+v", col.events)
	}
	if _, ok := known["aa"]; !ok {
		t.Error("known 应包含 aa")
	}
}

func TestDiffKnownDeviceUnchanged(t *testing.T) {
	d := Device{MAC: "aa", IP: "1.1.1.1"}
	known := map[string]Device{"aa": d}
	misses := map[string]int{}
	cooldown := map[string]time.Time{}
	cur := map[string]Device{"aa": d}
	col := &eventCollector{}
	diff(known, cur, misses, cur, cur, DefaultConfig(), newDiffCtx(), nil, cooldown, map[string]lastEvent{}, col.emit, nil)

	if len(col.events) != 0 {
		t.Errorf("无变化不应产生事件, got %+v", col.events)
	}
}

func TestDiffChangeEvent(t *testing.T) {
	known := map[string]Device{"aa": {MAC: "aa", IP: "1.1.1.1"}}
	misses := map[string]int{}
	cooldown := map[string]time.Time{}
	cur := map[string]Device{"aa": {MAC: "aa", IP: "2.2.2.2"}}
	col := &eventCollector{}
	diff(known, cur, misses, cur, cur, DefaultConfig(), newDiffCtx(), nil, cooldown, map[string]lastEvent{}, col.emit, nil)

	if len(col.events) != 1 || col.events[0].Kind != EventChange {
		t.Fatalf("IP 变化应触发 EventChange, got %+v", col.events)
	}
	if len(col.events[0].Changes) == 0 || col.events[0].Changes[0].Field != "IP" {
		t.Errorf("Change 字段应为 IP, got %+v", col.events[0].Changes)
	}
}

func TestDiffOfflineAfterMisses(t *testing.T) {
	d := Device{MAC: "aa", IP: "1.1.1.1"}
	known := map[string]Device{"aa": d}
	misses := map[string]int{}
	cooldown := map[string]time.Time{}
	emptyCur := map[string]Device{}
	emptyRaw := map[string]Device{}
	col := &eventCollector{}
	// 连续 5 次 diff, 第 5 次触发离线
	for i := 0; i < 5; i++ {
		diff(known, emptyCur, misses, emptyRaw, emptyRaw, DefaultConfig(), newDiffCtx(), nil, cooldown, map[string]lastEvent{}, col.emit, nil)
	}
	if len(col.events) != 1 || col.events[0].Kind != EventOffline {
		t.Fatalf("第 5 次应触发 EventOffline, got %+v", col.events)
	}
	if _, ok := known["aa"]; ok {
		t.Error("known 中应已移除 aa")
	}
	if _, ok := cooldown["aa"]; !ok {
		t.Error("cooldown 中应记录 aa")
	}
}

func TestDiffAPStillPresentSleeping(t *testing.T) {
	// 设备在 AP 关联表, RSSI 正常, ping 不通 → 息屏保护, 不计离线
	d := Device{MAC: "aa", IP: "1.1.1.1", RSSI: -50}
	known := map[string]Device{"aa": d}
	misses := map[string]int{}
	cooldown := map[string]time.Time{}
	emptyCur := map[string]Device{} // 不在 alive
	apSet := map[string]Device{"aa": d}
	// prober 返回 ping 不通
	p := fakeProber{reachable: map[string]bool{}}
	col := &eventCollector{}
	for i := 0; i < 10; i++ {
		diff(known, emptyCur, misses, apSet, apSet, DefaultConfig(), newDiffCtx(), p, cooldown, map[string]lastEvent{}, col.emit, nil)
	}
	if len(col.events) != 0 {
		t.Errorf("RSSI 正常的息屏场景不应触发事件, got %+v", col.events)
	}
	if _, ok := known["aa"]; !ok {
		t.Error("息屏设备应保留在 known 中")
	}
}

func TestDiffAPPresentExtremelyWeak(t *testing.T) {
	// RSSI < -88 + ping 不通 → 连续 2 次判离线
	d := Device{MAC: "aa", IP: "1.1.1.1", RSSI: -95}
	known := map[string]Device{"aa": d}
	misses := map[string]int{}
	cooldown := map[string]time.Time{}
	emptyCur := map[string]Device{}
	apSet := map[string]Device{"aa": d}
	p := fakeProber{reachable: map[string]bool{}}
	col := &eventCollector{}

	diff(known, emptyCur, misses, apSet, apSet, DefaultConfig(), newDiffCtx(), p, cooldown, map[string]lastEvent{}, col.emit, nil)
	if len(col.events) != 0 {
		t.Error("第 1 次不应触发")
	}
	diff(known, emptyCur, misses, apSet, apSet, DefaultConfig(), newDiffCtx(), p, cooldown, map[string]lastEvent{}, col.emit, nil)
	if len(col.events) != 1 || col.events[0].Kind != EventOffline {
		t.Errorf("第 2 次极弱信号应触发 EventOffline, got %+v", col.events)
	}
}

func TestDiffCooldownSuppressRepeatOffline(t *testing.T) {
	// 冷却期内重复离线应静默 (cooldown 已记录)
	d := Device{MAC: "aa", IP: "1.1.1.1"}
	known := map[string]Device{"aa": d}
	misses := map[string]int{}
	now := time.Now()
	cooldown := map[string]time.Time{"aa": now}
	emptyCur := map[string]Device{}
	emptyRaw := map[string]Device{}
	col := &eventCollector{}
	for i := 0; i < 5; i++ {
		diff(known, emptyCur, misses, emptyRaw, emptyRaw, DefaultConfig(), newDiffCtx(), nil, cooldown, map[string]lastEvent{}, col.emit, nil)
	}
	if len(col.events) != 0 {
		t.Errorf("冷却期内不应触发 EventOffline, got %+v", col.events)
	}
	if _, ok := known["aa"]; ok {
		t.Error("known 中应已移除 aa (静默移除)")
	}
}

func TestDiffCooldownSuppressWeakOnline(t *testing.T) {
	// 冷却期内设备重新出现, 但 RSSI 弱 → 静默更新, 不触发上线
	known := map[string]Device{} // aa 已被移除 (刚离线)
	misses := map[string]int{}
	now := time.Now()
	cooldown := map[string]time.Time{"aa": now}
	cur := map[string]Device{
		"aa": {MAC: "aa", IP: "1.1.1.1", RSSI: -75},
	}
	col := &eventCollector{}
	diff(known, cur, misses, cur, cur, DefaultConfig(), newDiffCtx(), nil, cooldown, map[string]lastEvent{}, col.emit, nil)
	if len(col.events) != 0 {
		t.Errorf("冷却期内弱信号不应触发上线, got %+v", col.events)
	}
	if _, ok := known["aa"]; !ok {
		t.Error("known 应静默更新包含 aa")
	}
}

func TestDiffCooldownStrongSignalClears(t *testing.T) {
	// 冷却期内 RSSI 强 (>= -65) → 清除冷却, 正常上线
	known := map[string]Device{}
	misses := map[string]int{}
	now := time.Now()
	cooldown := map[string]time.Time{"aa": now}
	cur := map[string]Device{
		"aa": {MAC: "aa", IP: "1.1.1.1", RSSI: -40},
	}
	col := &eventCollector{}
	diff(known, cur, misses, cur, cur, DefaultConfig(), newDiffCtx(), nil, cooldown, map[string]lastEvent{}, col.emit, nil)
	if len(col.events) != 1 || col.events[0].Kind != EventOnline {
		t.Errorf("强信号恢复应触发 EventOnline, got %+v", col.events)
	}
	if _, ok := cooldown["aa"]; ok {
		t.Error("强信号应清除 cooldown")
	}
}

func TestDiffCooldownExpiration(t *testing.T) {
	// 过期的冷却记录应在 diff 开始被清理
	cooldown := map[string]time.Time{
		"old": time.Now().Add(-120 * time.Second),
		"new": time.Now(),
	}
	diff(map[string]Device{}, map[string]Device{}, map[string]int{},
		map[string]Device{}, map[string]Device{}, DefaultConfig(),
		newDiffCtx(), nil, cooldown, map[string]lastEvent{}, func(Event) {}, nil)
	if _, ok := cooldown["old"]; ok {
		t.Error("过期冷却记录应被清除")
	}
	if _, ok := cooldown["new"]; !ok {
		t.Error("未过期冷却记录应保留")
	}
}

// --- Watcher 通过 WithFetcher 注入 fakeFetcher 测试 List/Run 基本路径 ---

type staticFetcher struct{ devs []Device }

func (f staticFetcher) Fetch(ctx context.Context) ([]Device, error) {
	return f.devs, nil
}

func TestWatcherList(t *testing.T) {
	ctx := context.Background()
	f := staticFetcher{devs: []Device{
		{MAC: "bb:bb:bb:bb:bb:bb", IP: "10.0.0.2"},
		{MAC: "aa:aa:aa:aa:aa:aa", IP: "10.0.0.1"},
	}}
	w := New(WithFetcher(f), WithProber(nil))

	devs, err := w.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(devs) != 2 {
		t.Fatalf("len(devs) = %d, want 2", len(devs))
	}
	// 应按 MAC 升序
	if devs[0].MAC != "aa:aa:aa:aa:aa:aa" || devs[1].MAC != "bb:bb:bb:bb:bb:bb" {
		t.Errorf("List 应按 MAC 升序, got %+v", devs)
	}
}

func TestWatcherNewOptions(t *testing.T) {
	w := New(WithConfig(Config{PollInterval: 3 * time.Second}))
	if w.cfg.PollInterval != 3*time.Second {
		t.Error("PollInterval 未覆盖")
	}
	// 零值字段保留默认
	if w.cfg.OfflineMisses == 0 {
		t.Error("OfflineMisses 零值应保留默认")
	}
}

func TestConfigString(t *testing.T) {
	c := Config{PollInterval: time.Second, OfflineMisses: 5, FetchTimeout: 3 * time.Second}
	s := c.String()
	// 包含关键字段描述
	if !contains_(s, "轮询间隔") || !contains_(s, "1s") {
		t.Errorf("Config.String = %q, 缺少轮询间隔", s)
	}
	if !contains_(s, "5") {
		t.Errorf("Config.String = %q, 缺少 misses 数字", s)
	}
}

// 辅助函数, 避免与其他包冲突
func contains_(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestWatcherFetcherKindBeforeDetection(t *testing.T) {
	w := New(WithFetcher(staticFetcher{}))
	// 显式注入 fetcher 时不会触发探测
	if w.FetcherKind() != "" {
		t.Errorf("FetcherKind 应为空, got %q", w.FetcherKind())
	}
}

func TestOnFetcherDetectedOption(t *testing.T) {
	called := false
	w := New(WithFetcher(staticFetcher{}), OnFetcherDetected(func(k FetcherKind) {
		called = true
	}))
	// 显式 WithFetcher 不触发探测也不触发回调
	_ = w
	if called {
		t.Error("显式 WithFetcher 不应触发 OnFetcherDetected")
	}
}

// --- handleConnectHint 测试 ---

func TestHandleConnectHintAlreadyKnown(t *testing.T) {
	w := New(WithFetcher(staticFetcher{}), WithProber(nil))
	w.known["aa:bb:cc:dd:ee:ff"] = Device{MAC: "aa:bb:cc:dd:ee:ff"}
	col := &eventCollector{}
	h := syslogHint{MAC: "aa:bb:cc:dd:ee:ff", IP: "1.1.1.1"}
	w.handleConnectHint(context.Background(), h, col.emit, nil)
	if len(col.events) != 0 {
		t.Errorf("已知设备不应重复触发上线, got %+v", col.events)
	}
}

func TestHandleConnectHintBasicEvent(t *testing.T) {
	// 新设计: handleConnectHint 立即用 hint 触发 EventOnline,
	// 不依赖 Fetcher (diff 轮询会通过 EventChange 补齐 RSSI/Radio/SSID 等)。
	w := New(WithFetcher(staticFetcher{}), WithProber(nil))
	col := &eventCollector{}
	h := syslogHint{MAC: "aa:bb:cc:dd:ee:ff", IP: "1.1.1.1"}
	w.handleConnectHint(context.Background(), h, col.emit, nil)
	if len(col.events) != 1 || col.events[0].Kind != EventOnline {
		t.Fatalf("应立即触发 EventOnline, got %+v", col.events)
	}
	if col.events[0].Device.IP != "1.1.1.1" {
		t.Errorf("应使用 hint.IP, got %+v", col.events[0].Device)
	}
}

func TestHandleConnectHintFallbackToIP(t *testing.T) {
	// fetcher 返回空, 但 hint 有 IP → 降级为基础记录
	w := New(WithFetcher(staticFetcher{}), WithProber(nil))
	col := &eventCollector{}
	h := syslogHint{MAC: "aa:bb:cc:dd:ee:ff", IP: "1.1.1.1"}
	w.handleConnectHint(context.Background(), h, col.emit, nil)
	if len(col.events) != 1 || col.events[0].Kind != EventOnline {
		t.Fatalf("应触发 EventOnline (降级记录), got %+v", col.events)
	}
	if col.events[0].Device.IP != "1.1.1.1" {
		t.Error("降级记录应包含 hint.IP")
	}
}

func TestHandleConnectHintGiveUpWithoutIP(t *testing.T) {
	// fetcher 返回空 + 无 IP → 降级路径仍应触发事件 (仅含 MAC 的基础记录),
	// 避免彻底丢失上线信号
	w := New(WithFetcher(staticFetcher{}), WithProber(nil))
	col := &eventCollector{}
	h := syslogHint{MAC: "aa:bb:cc:dd:ee:ff"}
	w.handleConnectHint(context.Background(), h, col.emit, nil)
	if len(col.events) != 1 || col.events[0].Kind != EventOnline {
		t.Fatalf("降级路径应触发 EventOnline 即使 IP 为空, got %+v", col.events)
	}
	if col.events[0].Device.IP != "" {
		t.Errorf("IP 应为空 (没有任何数据源提供), got %q", col.events[0].Device.IP)
	}
	if _, ok := w.known["aa:bb:cc:dd:ee:ff"]; !ok {
		t.Error("known 应加入该设备")
	}
}

func TestHandleConnectHintInCooldownClearsCooldown(t *testing.T) {
	// 新设计: handleConnectHint 不再调用 Fetcher, Device.RSSI=0 (未知)
	// 冷却期内 RSSI=0 不会被判定为弱信号 → 清除冷却期, 正常触发上线。
	// (diff 轮询拿到实际 RSSI 后, 若信号弱会再次进入冷却, 保持防抖效果)
	w := New(WithFetcher(staticFetcher{}), WithProber(nil))
	w.offlineCooldown["aa:bb:cc:dd:ee:ff"] = time.Now()
	col := &eventCollector{}
	h := syslogHint{MAC: "aa:bb:cc:dd:ee:ff", IP: "1.1.1.1"}
	w.handleConnectHint(context.Background(), h, col.emit, nil)
	if len(col.events) != 1 || col.events[0].Kind != EventOnline {
		t.Errorf("冷却期 + RSSI 未知 应触发 EventOnline, got %+v", col.events)
	}
	if _, ok := w.offlineCooldown["aa:bb:cc:dd:ee:ff"]; ok {
		t.Error("应清除 cooldown")
	}
}

// delayedFetcher 在前 N 次调用返回空, 之后返回预设设备列表。(保留用于回归测试)
type delayedFetcher struct {
	emptyUntilCall int
	callCount      int
	devs           []Device
}

func (f *delayedFetcher) Fetch(ctx context.Context) ([]Device, error) {
	f.callCount++
	if f.callCount <= f.emptyUntilCall {
		return []Device{}, nil
	}
	return f.devs, nil
}

func TestHandleConnectHintFastPath(t *testing.T) {
	// 新设计: handleConnectHint 不再调用 Fetcher, 只用 hint 中的信息 + DHCP hints
	// 立即触发 EventOnline。Fetcher 调用会拖慢流程, 已被移除。
	fetcher := &delayedFetcher{emptyUntilCall: 1000, devs: nil}
	w := New(WithFetcher(fetcher), WithProber(nil))

	col := &eventCollector{}
	h := syslogHint{MAC: "aa:bb:cc:dd:ee:ff", IP: "1.1.1.1"}
	w.handleConnectHint(context.Background(), h, col.emit, nil)

	if len(col.events) != 1 || col.events[0].Kind != EventOnline {
		t.Fatalf("应立即触发 EventOnline, got %+v", col.events)
	}
	if col.events[0].Device.IP != "1.1.1.1" {
		t.Errorf("应使用 hint.IP, got %+v", col.events[0].Device)
	}
	// 新设计不调用 Fetcher
	if fetcher.callCount != 0 {
		t.Errorf("新设计不应调用 Fetcher, 实际 %d 次", fetcher.callCount)
	}
}

// --- handleDisconnectHint 测试 ---

func TestHandleDisconnectHintNotInKnown(t *testing.T) {
	w := New(WithFetcher(staticFetcher{}), WithProber(nil))
	col := &eventCollector{}
	w.handleDisconnectHint(context.Background(), "aa:bb:cc:dd:ee:ff", col.emit)
	if len(col.events) != 0 {
		t.Error("不在 known 中应忽略")
	}
}

func TestHandleDisconnectHintPingReachable(t *testing.T) {
	// ping 可达 (漫游): 不触发离线
	p := fakeProber{reachable: map[string]bool{"1.1.1.1": true}}
	w := New(WithFetcher(staticFetcher{}), WithProber(p))
	w.known["aa:bb:cc:dd:ee:ff"] = Device{MAC: "aa:bb:cc:dd:ee:ff", IP: "1.1.1.1"}
	w.misses["aa:bb:cc:dd:ee:ff"] = 2
	col := &eventCollector{}
	w.handleDisconnectHint(context.Background(), "aa:bb:cc:dd:ee:ff", col.emit)
	if len(col.events) != 0 {
		t.Error("ping 可达应不触发离线")
	}
	if _, ok := w.misses["aa:bb:cc:dd:ee:ff"]; ok {
		t.Error("ping 可达应清除 misses")
	}
	if _, ok := w.known["aa:bb:cc:dd:ee:ff"]; !ok {
		t.Error("known 不应被移除")
	}
}

func TestHandleDisconnectHintPingFailed(t *testing.T) {
	// ping 不通 → 触发离线 + 冷却期
	p := fakeProber{reachable: map[string]bool{}}
	w := New(WithFetcher(staticFetcher{}), WithProber(p))
	w.known["aa:bb:cc:dd:ee:ff"] = Device{MAC: "aa:bb:cc:dd:ee:ff", IP: "1.1.1.1"}
	col := &eventCollector{}
	w.handleDisconnectHint(context.Background(), "aa:bb:cc:dd:ee:ff", col.emit)
	if len(col.events) != 1 || col.events[0].Kind != EventOffline {
		t.Fatalf("应触发 EventOffline, got %+v", col.events)
	}
	if _, ok := w.offlineCooldown["aa:bb:cc:dd:ee:ff"]; !ok {
		t.Error("应记录冷却期")
	}
	if _, ok := w.known["aa:bb:cc:dd:ee:ff"]; ok {
		t.Error("应从 known 移除")
	}
}

// --- drainHintsFor 测试 ---

func TestDrainHintsFor(t *testing.T) {
	w := New()
	// 向 channel 塞入同一 MAC 的多条 hint
	w.syslogHints <- syslogHint{MAC: "aa"}
	w.syslogHints <- syslogHint{MAC: "aa"}
	w.syslogHints <- syslogHint{MAC: "bb"} // 不同 MAC, 应放回
	w.drainHintsFor("aa")

	// 此时 channel 中应只剩 bb
	select {
	case h := <-w.syslogHints:
		if h.MAC != "bb" {
			t.Errorf("剩余 hint 应是 bb, got %q", h.MAC)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("应有 bb hint 留在 channel")
	}
	// channel 应已空
	select {
	case <-w.syslogHints:
		t.Error("channel 应已空")
	default:
	}
}
