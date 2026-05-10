package argus

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Config 控制 Watcher 的轮询节奏、离线判定阈值、冷却期与弱信号分级。
// 字段为零值时会在 New 中回退到默认值, 因此最小用法是 New() 不带任何 Option。
type Config struct {
	// PollInterval 是相邻两次拉取之间的间隔。默认 1s。
	PollInterval time.Duration
	// OfflineMisses 是设备连续多少次未出现在拉取结果里才判定为离线 (默认路径, 不含 RSSI/ARP 加速)。
	// 默认 5。
	OfflineMisses int
	// FetchTimeout 限制单次拉取调用的耗时。默认 3s。
	FetchTimeout time.Duration

	// OfflineCooldown 设备刚判离线后的冷却期: 冷却期内重复的 EventOffline 被压制,
	// 且 RSSI < CooldownReleaseRSSI 的重新接入不触发 EventOnline。
	//
	// 只要设备持续处于弱信号状态, 冷却期会被每次轮询刷新, 避免抑制后再次触发重复离线。
	// 设为 time.Nanosecond 可实际禁用冷却期。默认值见 DefaultConfig。
	OfflineCooldown time.Duration
	// CooldownReleaseRSSI 冷却期内 RSSI 恢复到此值 (含) 之上才允许触发 EventOnline。
	// RSSI 为负值, 默认 -65 (强信号)。零值 (0 dBm) 无实际意义, 用作"使用默认"的标记。
	CooldownReleaseRSSI int
	// WeakRSSI 信号弱的阈值, 低于此值触发 diff 的加速离线判定。默认 -80。
	WeakRSSI int
	// ExtremelyWeakRSSI 信号极弱的阈值, 触发更激进的 threshold。默认 -88。
	ExtremelyWeakRSSI int
	// WeakMissThreshold WeakRSSI ~ ExtremelyWeakRSSI 区间内的离线计数阈值。默认 5。
	WeakMissThreshold int
	// ExtremelyWeakMissThreshold < ExtremelyWeakRSSI 的离线计数阈值。默认 2。
	ExtremelyWeakMissThreshold int

	// FlapSuppressionWindow 抖动抑制窗口: 一台设备在此窗口内不会连续触发两个同类事件
	// (两次 Online / 两次 Offline)。用于抵消中等信号设备的快闪。默认 30s。
	// WithConfig 对零值按"保留默认"处理; 若需显式关闭, 请使用 DisableFlapSuppression。
	FlapSuppressionWindow time.Duration

	// DisableCooldown 显式关闭冷却期机制。设置为 true 时, OfflineCooldown 相关的
	// 所有抑制逻辑 (COOLDOWN_SUPPRESS_ONLINE / COOLDOWN_SUPPRESS_OFFLINE /
	// COOLDOWN_CLEARED) 都不再触发, 离线事件后重新出现的设备立即触发上线。
	// 默认 false (启用冷却期)。此字段与 OfflineCooldown 数值不冲突: true 时忽略数值。
	DisableCooldown bool
	// DisableFlapSuppression 显式关闭抖动抑制窗口 (与 FlapSuppressionWindow=0 等价,
	// 但语义更清晰, 不依赖"零值 = 关闭"的约定)。默认 false (启用抑制)。
	DisableFlapSuppression bool
}

// DefaultConfig 返回库的默认配置:
//   - 上线 / 状态变更 ≈1s
//   - 正常路径离线检测 ≈5s (OfflineMisses × PollInterval)
//   - 信号极弱时离线检测 ≈2s
//   - 弱信号边缘设备的上下线抖动被 OfflineCooldown (默认 90s) 压制
//
// 所有字段均可通过 WithConfig 覆盖, 传零值保留默认。
func DefaultConfig() Config {
	return Config{
		PollInterval:               1 * time.Second,
		OfflineMisses:              5,
		FetchTimeout:               3 * time.Second,
		OfflineCooldown:            90 * time.Second,
		CooldownReleaseRSSI:        -65,
		WeakRSSI:                   -80,
		ExtremelyWeakRSSI:          -88,
		WeakMissThreshold:          5,
		ExtremelyWeakMissThreshold: 2,
		FlapSuppressionWindow:      30 * time.Second,
	}
}

// String 返回配置的可读摘要, 用于启动 banner。
func (c Config) String() string {
	offlineAfter := time.Duration(c.OfflineMisses) * c.PollInterval
	return fmt.Sprintf("轮询间隔=%s, 离线判定=连续 %d 次未发现 ≈%s, 冷却期=%s",
		c.PollInterval, c.OfflineMisses, formatDuration(offlineAfter), formatDuration(c.OfflineCooldown))
}

// formatDuration 将 Duration 格式化为人类友好字符串:
// < 1s 显示 "500ms", ≥ 1s 按秒显示 "5s"。
func formatDuration(d time.Duration) string {
	if d < time.Second {
		return d.Round(time.Millisecond).String()
	}
	return d.Round(time.Second).String()
}

// Validate 校验配置合法性, 防止零值 / 负值导致死循环或 panic。
func (c Config) Validate() error {
	if c.PollInterval <= 0 {
		return fmt.Errorf("PollInterval 必须大于 0, 当前: %s", c.PollInterval)
	}
	if c.OfflineMisses <= 0 {
		return fmt.Errorf("OfflineMisses 必须大于 0, 当前: %d", c.OfflineMisses)
	}
	if c.OfflineCooldown < 0 {
		return fmt.Errorf("OfflineCooldown 不能为负, 当前: %s", c.OfflineCooldown)
	}
	if c.CooldownReleaseRSSI > 0 {
		return fmt.Errorf("CooldownReleaseRSSI 必须 ≤ 0 (dBm), 当前: %d", c.CooldownReleaseRSSI)
	}
	if c.WeakRSSI > 0 || c.ExtremelyWeakRSSI > 0 {
		return fmt.Errorf("RSSI 阈值必须 ≤ 0 (dBm)")
	}
	if c.ExtremelyWeakRSSI >= c.WeakRSSI {
		return fmt.Errorf("ExtremelyWeakRSSI (%d) 必须严格低于 WeakRSSI (%d)", c.ExtremelyWeakRSSI, c.WeakRSSI)
	}
	if c.WeakMissThreshold <= 0 || c.ExtremelyWeakMissThreshold <= 0 {
		return fmt.Errorf("MissThreshold 必须大于 0")
	}
	return nil
}

// EventHandler 接收实时事件回调。回调内不应阻塞过久, 以免影响下一轮拉取。
type EventHandler func(Event)

// ErrorHandler 接收非致命的拉取错误。可为 nil; 为 nil 时错误被丢弃。
type ErrorHandler func(error)

// Option 用于自定义 Watcher。
type Option func(*Watcher)

// WithConfig 设置自定义轮询配置。零值字段会保留默认值 (Config 字段的零值 0 通常不合法,
// 安全地作为"使用默认"的哨兵)。如需显式关闭冷却期或抖动抑制, 请使用
// Config.DisableCooldown / DisableFlapSuppression 而非 OfflineCooldown=0。
func WithConfig(c Config) Option {
	return func(w *Watcher) {
		if c.PollInterval > 0 {
			w.cfg.PollInterval = c.PollInterval
		}
		if c.OfflineMisses > 0 {
			w.cfg.OfflineMisses = c.OfflineMisses
		}
		if c.FetchTimeout > 0 {
			w.cfg.FetchTimeout = c.FetchTimeout
		}
		if c.OfflineCooldown > 0 {
			w.cfg.OfflineCooldown = c.OfflineCooldown
		}
		if c.CooldownReleaseRSSI < 0 {
			w.cfg.CooldownReleaseRSSI = c.CooldownReleaseRSSI
		}
		if c.WeakRSSI < 0 {
			w.cfg.WeakRSSI = c.WeakRSSI
		}
		if c.ExtremelyWeakRSSI < 0 {
			w.cfg.ExtremelyWeakRSSI = c.ExtremelyWeakRSSI
		}
		if c.WeakMissThreshold > 0 {
			w.cfg.WeakMissThreshold = c.WeakMissThreshold
		}
		if c.ExtremelyWeakMissThreshold > 0 {
			w.cfg.ExtremelyWeakMissThreshold = c.ExtremelyWeakMissThreshold
		}
		if c.FlapSuppressionWindow > 0 {
			w.cfg.FlapSuppressionWindow = c.FlapSuppressionWindow
		}
		// Disable* 字段: 零值 (false) 即不修改, 用户传 true 时才覆盖。
		if c.DisableCooldown {
			w.cfg.DisableCooldown = true
		}
		if c.DisableFlapSuppression {
			w.cfg.DisableFlapSuppression = true
		}
	}
}

// WithFetcher 注入自定义 Fetcher (测试 / 接其他数据源)。
func WithFetcher(f Fetcher) Option {
	return func(w *Watcher) { w.fetcher = f }
}

// WithProber 注入活性探测器, 用于识别 AP 关联表残留 (例如手机走出信号范围
// 但 AP 还未将其踢出关联表的场景)。传 nil 可关闭活性探测。
// 默认使用 ICMPProber{Timeout: 1s}。
func WithProber(p Prober) Option {
	return func(w *Watcher) {
		w.prober = p
		w.proberSet = true
	}
}

// OnFetcherDetected 注册回调, 在自动探测完成时被调用一次, 报告选中的 Fetcher 类型。
// 显式 WithFetcher 注入时不会触发此回调。
func OnFetcherDetected(cb func(FetcherKind)) Option {
	return func(w *Watcher) { w.onDetect = cb }
}

// WithDecisionHandler 注册决策观测回调, 用于记录 Watcher 内部判定链路 (上线/离线/
// 冷却期/抖动抑制等决策点)。适合日志 / 调参 / 排障, 业务侧通常用 EventHandler 即可。
// 回调内不应阻塞, 决策产生频率较高。传 nil (或不调用本 Option) 完全不收集决策。
func WithDecisionHandler(cb DecisionHandler) Option {
	return func(w *Watcher) { w.onDecision = cb }
}

// WithBaseline 以 baseline 为已知设备基线初始化 Watcher。Watcher 在启动时会跳过
// 对这些 MAC 的"新上线"识别 (不触发 EventOnline), 直接视为历史已知。
//
// 典型场景: 进程重启 / 热重载时, 把旧 Watcher 的 Known() 快照传给新 Watcher,
// 避免重启瞬间所有设备被识别为"新上线"导致业务事件风暴。
//
// 注意: baseline 是浅拷贝; 传入后请勿再并发修改这份 map。
// 不会触发 EnsureFetcher, 传入的设备字段由调用方保证正确。
func WithBaseline(baseline map[string]Device) Option {
	return func(w *Watcher) {
		for mac, d := range baseline {
			w.known[mac] = d
		}
	}
}

// Watcher 是库的主入口, 管理一份"已知设备"的状态, 通过周期拉取识别变化。
//
//	w := argus.New()
//	devices, _ := w.List(ctx)               // 一次性拉取
//	w.Run(ctx, func(e Event) {...}, nil)    // 实时监听 (阻塞)
//
// 未通过 WithFetcher 显式指定时, 首次 List/Run 会用 DetectFetcher 在 ubus 上
// 自动识别 ahsapd / hostapd 并选择对应实现; 探测结果会被缓存。
//
// Run 内部同时启动 logread -f 监听系统日志: 当收到 wifi_sys_disconn_act /
// deauth / Del Sta 等事件时, 立即触发对该设备的活性探测并可即时判定离线。
//
// 并发模型: 轮询 diff、日志驱动 handleConnectHint 和 handleDisconnectHint 可能
// 在不同 goroutine 中被调度, 统一通过 stateMu 保护 known / misses / cooldown。
type Watcher struct {
	cfg       Config
	fetcher   Fetcher
	prober    Prober
	proberSet bool

	detectOnce sync.Once
	detectKind FetcherKind
	detectErr  error
	onDetect   func(FetcherKind)

	// onDecision 观测内部决策的回调, 可为 nil (零成本)。
	onDecision DecisionHandler

	// syslogHints 接收系统日志事件, 由 syslog goroutine 写入,
	// 主循环读取。channel 带缓冲避免阻塞日志解析。
	syslogHints chan syslogHint

	// stateMu 保护下面三个 map 的并发访问
	stateMu sync.Mutex
	// known 记录当前被库视为"在线"的设备集 (MAC -> Device)
	known map[string]Device
	// misses 记录每台 known 设备在最近几轮轮询中未被发现的次数
	misses map[string]int
	// offlineCooldown 记录每台设备最近一次被判定离线的时刻,
	// 在冷却期内即使设备重新出现也不触发上线事件, 防止弱信号区抖动。
	offlineCooldown map[string]time.Time
	// lastEventAt 记录每台设备最近一次触发外发事件的时刻和类型,
	// 用于 FlapSuppressionWindow 抑制中等信号设备的连续快闪。
	lastEventAt map[string]lastEvent
	// disconnectInFlight 记录当前正在执行 handleDisconnectHint 的 MAC 集合,
	// 用于去重: 同一断开通常会在毫秒内连发 disconnect/deauth/Del Sta 三条 hint,
	// 由 runSyslogConsumer 派生 3 个 worker, 但只有第一个需要进入 500ms Sleep + ping
	// 流程, 后两个直接静默跳过 (DecisionDisconnectSkippedInflight)。
	disconnectInFlight map[string]struct{}

	// droppedHints 记录因 syslogHints channel 已满而丢弃的事件数,
	// 周期性通过 onError 报告。
	droppedHints uint64
}

// emitDecision 安全触发决策回调。onDecision 为 nil 时完全不做任何事 (零成本)。
func (w *Watcher) emitDecision(kind DecisionKind, mac string, detail string) {
	if w.onDecision == nil {
		return
	}
	w.safeInvokeDecision(w.onDecision, Decision{Time: time.Now(), Kind: kind, MAC: mac, Detail: detail})
}

// safeInvokeEvent 以 panic-safe 方式调用 EventHandler, panic 通过 onError 上报。
// 库假定用户回调可能 panic; 一次 panic 不应杀死 watcher 的任何 goroutine。
// cb 为 nil 时直接返回 (内部调用前应先检查, 这里再兜底)。
func (w *Watcher) safeInvokeEvent(cb EventHandler, onError ErrorHandler, e Event) {
	if cb == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			w.reportCallbackPanic(onError, "EventHandler", r)
		}
	}()
	cb(e)
}

// safeInvokeError panic-safe 调用 ErrorHandler; 它本身的 panic 只能吞掉 (避免递归)。
func (w *Watcher) safeInvokeError(cb ErrorHandler, err error) {
	if cb == nil {
		return
	}
	defer func() { _ = recover() }()
	cb(err)
}

// safeInvokeDecision panic-safe 调用 DecisionHandler, panic 被静默吞 (决策通道频率高,
// 不值得为诊断回调做递归上报)。
func (w *Watcher) safeInvokeDecision(cb DecisionHandler, d Decision) {
	if cb == nil {
		return
	}
	defer func() { _ = recover() }()
	cb(d)
}

// reportCallbackPanic 把用户回调的 panic 以 error 形式上报给 onError。
// onError 自身 panic 会被 safeInvokeError 吞掉, 避免递归。
func (w *Watcher) reportCallbackPanic(onError ErrorHandler, which string, r any) {
	err := fmt.Errorf("argus: %s panicked: %v", which, r)
	w.safeInvokeError(onError, err)
}

// syslogHint 封装系统日志中提取的设备事件提示。
type syslogHint struct {
	MAC        string
	IP         string // 仅 DHCP_ACK 有值
	Disconnect bool   // true=断开, false=接入
}

// lastEvent 记录一次外发事件的时刻和类型, 供 FlapSuppressionWindow 使用。
type lastEvent struct {
	at   time.Time
	kind EventKind
}

// shouldSuppressFlap 判断是否应该压制即将触发的事件。
// 在 FlapSuppressionWindow 窗口内, 同一 MAC 产生的同类事件 (Online→Online 或
// Offline→Offline) 会被压制。相反类型的事件 (Online→Offline 或反过来) 不压制。
// 注意: 调用方应持 stateMu。
func (w *Watcher) shouldSuppressFlap(mac string, kind EventKind, now time.Time) bool {
	if w.cfg.DisableFlapSuppression || w.cfg.FlapSuppressionWindow <= 0 {
		return false
	}
	last, ok := w.lastEventAt[mac]
	if !ok {
		return false
	}
	// 仅压制同类重复事件
	if last.kind != kind {
		return false
	}
	return now.Sub(last.at) < w.cfg.FlapSuppressionWindow
}

// recordEvent 更新最近事件记录, 调用方应持 stateMu。
func (w *Watcher) recordEvent(mac string, kind EventKind, now time.Time) {
	w.lastEventAt[mac] = lastEvent{at: now, kind: kind}
}

// New 创建一个 Watcher。Option 用于覆盖默认配置或注入自定义组件。
func New(opts ...Option) *Watcher {
	w := &Watcher{
		cfg:                DefaultConfig(),
		syslogHints:        make(chan syslogHint, 256),
		known:              make(map[string]Device),
		misses:             make(map[string]int),
		offlineCooldown:    make(map[string]time.Time),
		lastEventAt:        make(map[string]lastEvent),
		disconnectInFlight: make(map[string]struct{}),
	}
	for _, opt := range opts {
		opt(w)
	}
	if !w.proberSet {
		w.prober = ICMPProber{Timeout: 1 * time.Second}
	}
	return w
}

// FetcherKind 返回当前 Watcher 使用的 Fetcher 类型。
// 仅在首次 List / Run 调用 (或显式调用 EnsureFetcher) 之后才有意义。
// 用户显式 WithFetcher 注入时返回空串。
func (w *Watcher) FetcherKind() FetcherKind { return w.detectKind }

// Known 返回当前库认为"在线"的设备集的深拷贝快照, 按小写 MAC 索引。
// 并发安全, 可随时调用。
//
// 典型用途: 进程热重载时, 把快照传给新 Watcher 的 WithBaseline 以避免
// 重启瞬间所有设备被识别为"新上线"。
func (w *Watcher) Known() map[string]Device {
	w.stateMu.Lock()
	defer w.stateMu.Unlock()
	out := make(map[string]Device, len(w.known))
	for mac, d := range w.known {
		out[mac] = d
	}
	return out
}

// EnsureFetcher 在 fetcher 未显式指定时, 触发一次 ubus 探测。多次调用安全。
// 通常无需手动调用 - List / Run 内部会自动触发。
func (w *Watcher) EnsureFetcher(ctx context.Context) error {
	w.detectOnce.Do(func() {
		if w.fetcher != nil {
			return
		}
		f, kind, err := DetectFetcher(ctx, w.cfg.FetchTimeout)
		if err != nil {
			w.detectErr = fmt.Errorf("%w: %v", ErrNoFetcher, err)
			return
		}
		w.fetcher = f
		w.detectKind = kind
		if w.onDetect != nil {
			func() {
				defer func() { _ = recover() }()
				w.onDetect(kind)
			}()
		}
	})
	return w.detectErr
}

// List 立即拉取一次当前接入设备列表, 不会启动后台监听。
// 启用了 Prober 时, 不可达的"假在线"设备会被过滤掉; 如果 Run 正在运行且设备处于
// 离线冷却期 (刚判离线且 RSSI 仍弱), 该设备也会被过滤, 与 Run 的"在线"定义保持一致。
// 返回切片按 MAC 排序。
func (w *Watcher) List(ctx context.Context) ([]Device, error) {
	if err := w.EnsureFetcher(ctx); err != nil {
		return nil, err
	}
	m, err := w.fetchByMAC(ctx)
	if err != nil {
		return nil, err
	}

	// 应用冷却期过滤, 保持与 diff / handleConnectHint 语义一致。
	w.stateMu.Lock()
	now := time.Now()
	for mac, d := range m {
		if w.cfg.DisableCooldown {
			continue
		}
		cdTime, inCD := w.offlineCooldown[mac]
		if !inCD || now.Sub(cdTime) >= w.cfg.OfflineCooldown {
			continue
		}
		// 冷却期内, 弱信号视为尚未真正上线
		if d.RSSI != 0 && d.RSSI < w.cfg.CooldownReleaseRSSI {
			delete(m, mac)
		}
	}
	w.stateMu.Unlock()

	devs := make([]Device, 0, len(m))
	for _, d := range m {
		devs = append(devs, d)
	}
	sort.Slice(devs, func(i, j int) bool { return devs[i].MAC < devs[j].MAC })
	return devs, nil
}

// Run 启动后台监听并阻塞直到 ctx 被取消:
//   - 启动时先做一次基线拉取, 这次不会触发 onEvent;
//   - 之后每个轮询周期通过 onEvent 投递设备上线 / 离线 / 状态变更事件;
//   - 系统日志 hint 在独立 goroutine 中异步处理, 不阻塞主循环;
//   - 单次拉取失败通过 onError 上报但不会终止循环 (onError 可为 nil)。
//
// 仅在初始基线拉取失败时返回 error, ctx 取消时返回 nil。
//
// Watcher 不支持多次 Run: 自动探测的 Fetcher 会被 sync.Once 缓存, 且 known/misses/
// cooldown 状态在 Run 返回后仍保留。如需重新启动, 请创建新的 Watcher。
//
// 错误可通过 errors.Is 判别: ErrHandlerRequired / ErrInvalidConfig / ErrNoFetcher /
// ErrFetchFailed。
func (w *Watcher) Run(ctx context.Context, onEvent EventHandler, onError ErrorHandler) error {
	if onEvent == nil {
		return fmt.Errorf("%w: onEvent 不能为 nil", ErrHandlerRequired)
	}
	if err := w.cfg.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	if err := w.EnsureFetcher(ctx); err != nil {
		return err // EnsureFetcher 已 wrap ErrNoFetcher
	}
	baseline, err := w.fetchByMAC(ctx)
	if err != nil {
		return fmt.Errorf("%w: 初始基线拉取失败: %v", ErrFetchFailed, err)
	}
	// 将基线填入 watcher state (WithBaseline 的内容保持, 新拉取覆盖同 MAC)
	w.stateMu.Lock()
	for mac, d := range baseline {
		w.known[mac] = d
	}
	w.stateMu.Unlock()

	// 启动系统日志监听 goroutine
	go w.runSyslog(ctx, onError)

	// 启动 syslog hint 消费 goroutine (独立于主轮询, 避免 500ms 阻塞主循环)
	go w.runSyslogConsumer(ctx, onEvent, onError)

	t := time.NewTicker(w.cfg.PollInterval)
	defer t.Stop()

	// 周期性上报 syslogHints 丢弃计数
	reportTicker := time.NewTicker(30 * time.Second)
	defer reportTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-reportTicker.C:
			if dropped := atomic.SwapUint64(&w.droppedHints, 0); dropped > 0 {
				w.safeInvokeError(onError, fmt.Errorf("最近 30s 内因缓冲区满丢弃 %d 条 syslog 事件", dropped))
			}
		case <-t.C:
			apRaw, cur, err := w.fetchWithAPSet(ctx)
			if err != nil {
				w.safeInvokeError(onError, err)
				continue
			}
			w.stateMu.Lock()
			pending := diff(w.known, cur, w.misses, apRaw, apRaw, w.cfg, ctx, w.prober, w.offlineCooldown, w.lastEventAt, w.onDecision)
			w.stateMu.Unlock()
			// 在锁外发射事件: 用户回调 panic / 阻塞都不会影响 Watcher 共享状态。
			for _, ev := range pending {
				w.safeInvokeEvent(onEvent, onError, ev)
			}
		}
	}
}

// runSyslog 在独立 goroutine 中运行 WatchSyslog, 将事件转成 syslogHint 放入 channel。
func (w *Watcher) runSyslog(ctx context.Context, onError ErrorHandler) {
	err := WatchSyslog(ctx, func(e SyslogEvent) {
		mac := normalizeMAC(e.MAC)
		if mac == "" {
			return
		}
		var h syslogHint
		h.MAC = mac
		if e.Kind.IsDisconnect() {
			h.Disconnect = true
		} else if e.Kind.IsConnect() {
			h.Disconnect = false
			h.IP = e.IP
		} else {
			return
		}
		select {
		case w.syslogHints <- h:
		default:
			atomic.AddUint64(&w.droppedHints, 1)
		}
	}, onError)
	if err != nil && ctx.Err() == nil {
		w.safeInvokeError(onError, fmt.Errorf("系统日志监听异常退出: %w", err))
	}
}

// runSyslogConsumer 从 syslogHints 读取事件并分发到 handle 函数。
// 每个 hint 在独立 goroutine 中处理, 避免 handleDisconnectHint 的 500ms Sleep
// 阻塞后续事件; worker 数通过 MaxConcurrentHints 限制防止 goroutine 爆炸。
func (w *Watcher) runSyslogConsumer(ctx context.Context, onEvent EventHandler, onError ErrorHandler) {
	sem := make(chan struct{}, 16) // 最多 16 个并发 handle goroutine
	for {
		select {
		case <-ctx.Done():
			return
		case h := <-w.syslogHints:
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			go func(h syslogHint) {
				defer func() { <-sem }()
				if h.Disconnect {
					w.handleDisconnectHint(ctx, h.MAC, onEvent, onError)
				} else {
					w.handleConnectHint(ctx, h, onEvent, onError)
				}
			}(h)
		}
	}
}

// handleDisconnectHint 当系统日志报告某设备断开时, 立即探测并判断是否真正离线。
// 在独立 goroutine 中执行, 不阻塞主循环。
//
// 同一断开通常在毫秒内连发 disconnect/deauth/Del Sta 三条 syslog, 派生 3 个 worker。
// 入口处的 disconnectInFlight 集合保证只有第一个 worker 进入 500ms Sleep + ping
// 流程, 后续重复 hint 直接发 DISCONNECT_SKIP_INFLIGHT 决策返回, 节省 ~2 × 1.5s
// 的 worker 时间和一次冗余 ping。
func (w *Watcher) handleDisconnectHint(ctx context.Context, mac string, onEvent EventHandler, onError ErrorHandler) {
	w.emitDecision(DecisionDisconnectHintReceived, mac, "")

	w.stateMu.Lock()
	if _, inflight := w.disconnectInFlight[mac]; inflight {
		w.stateMu.Unlock()
		w.emitDecision(DecisionDisconnectSkippedInflight, mac, "")
		return
	}
	d, ok := w.known[mac]
	if !ok {
		w.stateMu.Unlock()
		w.emitDecision(DecisionDisconnectIgnoredUnknown, mac, "")
		return
	}
	w.disconnectInFlight[mac] = struct{}{}
	w.stateMu.Unlock()
	defer func() {
		w.stateMu.Lock()
		delete(w.disconnectInFlight, mac)
		w.stateMu.Unlock()
	}()

	// 等一小段时间让设备有机会重新关联 (漫游/瞬断)
	select {
	case <-time.After(500 * time.Millisecond):
	case <-ctx.Done():
		return
	}

	w.drainHintsFor(mac)

	if w.prober != nil && d.IP != "" {
		if w.prober.Reachable(ctx, d.IP) {
			w.stateMu.Lock()
			delete(w.misses, mac)
			w.stateMu.Unlock()
			w.emitDecision(DecisionDisconnectPingOK, mac, "IP="+d.IP)
			return
		}
	}

	w.stateMu.Lock()
	// 再次确认: 可能在 Sleep 期间被 diff 或其他 handler 删除
	if _, stillKnown := w.known[mac]; !stillKnown {
		w.stateMu.Unlock()
		return
	}
	delete(w.known, mac)
	delete(w.misses, mac)
	if !w.cfg.DisableCooldown {
		w.offlineCooldown[mac] = time.Now()
	}
	// 抖动抑制检查: 窗口期内同类事件压制
	now := time.Now()
	if w.shouldSuppressFlap(mac, EventOffline, now) {
		w.stateMu.Unlock()
		w.emitDecision(DecisionFlapSuppressOffline, mac, "")
		return
	}
	w.recordEvent(mac, EventOffline, now)
	w.stateMu.Unlock()

	w.emitDecision(DecisionOfflineEmitted, mac, "via=syslog")
	w.safeInvokeEvent(onEvent, onError, Event{Time: now, Kind: EventOffline, Device: d})
}

// handleConnectHint 当系统日志报告设备接入 (WPA 完成 / MAC 表新增 / DHCP 分配) 时,
// 立即触发上线事件, 无需等待下一轮轮询。
//
// 设计原则: 不依赖 ubus Fetch。路由器在 WiFi 握手期间 CPU 紧张,
// ubus call 可能反复 "signal: killed", 盲目重试只会拖慢流程、丢失事件。
// 本函数只用 syslog 提供的 MAC + DHCP 日志中的 IP + /tmp/dhcp.leases + ARP
// 构建"基础记录"上线。后续 diff 轮询会在 ubus 恢复后通过 EventChange 补齐完整字段。
//
// 冷却期策略与 diff() 一致: 刚判离线 (≤OfflineCooldown) 的设备, 只有 RSSI ≥ CooldownReleaseRSSI 才触发上线,
// 否则静默更新 known, 防止弱信号边缘抖动。
func (w *Watcher) handleConnectHint(ctx context.Context, h syslogHint, onEvent EventHandler, onError ErrorHandler) {
	w.emitDecision(DecisionConnectHintReceived, h.MAC, "IP="+h.IP)

	w.stateMu.Lock()
	if _, alreadyKnown := w.known[h.MAC]; alreadyKnown {
		w.stateMu.Unlock()
		w.emitDecision(DecisionConnectSkippedKnown, h.MAC, "")
		return // 防重复
	}
	w.stateMu.Unlock()

	// 立即用 DHCP/ARP hints 构建基础记录触发上线。后续 diff 会补齐 RSSI/Radio/SSID。
	hints := loadHints(ctx)
	d := applyHints(Device{
		MAC:      h.MAC,
		IP:       h.IP,
		LastSeen: time.Now(),
	}, hints[h.MAC])
	w.emitConnectEvent(d, onEvent, onError)
}

// emitConnectEvent 统一的上线事件发射点, 应用冷却期策略。
func (w *Watcher) emitConnectEvent(d Device, onEvent EventHandler, onError ErrorHandler) {
	now := time.Now()
	w.stateMu.Lock()
	// 双重检查: handle 流程被 diff 并发覆盖时可能已加入 known
	if _, already := w.known[d.MAC]; already {
		w.stateMu.Unlock()
		w.emitDecision(DecisionConnectSkippedKnown, d.MAC, "double-check")
		return
	}
	if !w.cfg.DisableCooldown {
		if cdTime, inCD := w.offlineCooldown[d.MAC]; inCD && now.Sub(cdTime) < w.cfg.OfflineCooldown {
			// 冷却期内: 弱信号时静默更新, 不触发事件
			if d.RSSI != 0 && d.RSSI < w.cfg.CooldownReleaseRSSI {
				// 同时刷新 cooldown 保持抑制, 避免冷却期自然过期后重新走离线流程
				w.offlineCooldown[d.MAC] = now
				w.known[d.MAC] = d
				w.stateMu.Unlock()
				w.emitDecision(DecisionCooldownSuppressOnline, d.MAC, fmt.Sprintf("RSSI=%d", d.RSSI))
				return
			}
			delete(w.offlineCooldown, d.MAC)
			w.emitDecision(DecisionCooldownCleared, d.MAC, fmt.Sprintf("RSSI=%d", d.RSSI))
		}
	}
	// 抖动抑制: 窗口期内同类事件静默更新
	if w.shouldSuppressFlap(d.MAC, EventOnline, now) {
		w.known[d.MAC] = d
		w.stateMu.Unlock()
		w.emitDecision(DecisionFlapSuppressOnline, d.MAC, "")
		return
	}
	w.known[d.MAC] = d
	w.recordEvent(d.MAC, EventOnline, now)
	w.stateMu.Unlock()

	w.emitDecision(DecisionConnectEmitted, d.MAC, fmt.Sprintf("IP=%s", d.IP))
	w.safeInvokeEvent(onEvent, onError, Event{Time: now, Kind: EventOnline, Device: d})
}

// drainHintsFor 清空 syslogHints 中属于指定 MAC 的后续事件, 其它 MAC 的放回。
func (w *Watcher) drainHintsFor(mac string) {
	for {
		select {
		case h := <-w.syslogHints:
			if h.MAC != mac {
				select {
				case w.syslogHints <- h:
				default:
				}
				return
			}
		default:
			return
		}
	}
}

// fetchByMAC 拉取一次并按 MAC 索引为 map; 若启用了 Prober, 同时过滤掉不可达设备
// (避免 AP 关联表残留导致的"假在线")。
func (w *Watcher) fetchByMAC(ctx context.Context) (map[string]Device, error) {
	_, cur, err := w.fetchWithAPSet(ctx)
	return cur, err
}

// fetchWithAPSet 拉取一次, 返回:
//   - apRaw: 原始 AP 关联表的全部设备 (含 RSSI 等实时字段, 按 MAC 索引)
//   - alive: 经 ping 过滤后的可达设备
func (w *Watcher) fetchWithAPSet(ctx context.Context) (apRaw map[string]Device, alive map[string]Device, err error) {
	devs, err := w.fetcher.Fetch(ctx)
	if err != nil {
		return nil, nil, err
	}
	raw := make(map[string]Device, len(devs))
	for _, d := range devs {
		raw[d.MAC] = d
	}
	alive = filterAlive(ctx, raw, w.prober)
	return raw, alive, nil
}

// diff 比较前后两次快照, 修改 known/misses, 通过 onEvent 输出事件。
// 综合 ping / AP 关联表 / ARP 状态 / RSSI 信号强度多维判断是否真正离线。
// cooldown 防止弱信号区设备在上线/离线间快速抖动。
// diff 比较前后两次快照, 修改 known/misses, 收集待发射的业务事件。
// 综合 ping / AP 关联表 / ARP 状态 / RSSI 信号强度多维判断是否真正离线。
// cooldown 防止弱信号区设备在上线/离线间快速抖动。
//
// 返回收集到的事件切片, 调用方在释放 stateMu 之后再交给用户 EventHandler
// 分发, 避免用户回调阻塞或 panic 破坏共享状态。决策回调 onDecision
// 仍在持锁期间同步触发 (频率高且需要与决策时序一致, 故不走 pending)。
func diff(known, cur map[string]Device, misses map[string]int, apRaw, apSet map[string]Device, cfg Config, ctx context.Context, prober Prober, cooldown map[string]time.Time, lastEventAt map[string]lastEvent, onDecision DecisionHandler) []Event {
	now := time.Now()
	pending := make([]Event, 0, 4)

	emitDecision := func(kind DecisionKind, mac, detail string) {
		if onDecision != nil {
			func() {
				defer func() { _ = recover() }()
				onDecision(Decision{Time: now, Kind: kind, MAC: mac, Detail: detail})
			}()
		}
	}

	// emitIfNotSuppressed 收集事件到 pending, 除冷却期外再叠加 FlapSuppressionWindow 抑制,
	// 防止中等信号设备在短时间内连续触发同类事件。
	emitIfNotSuppressed := func(kind EventKind, d Device) {
		if !cfg.DisableFlapSuppression && cfg.FlapSuppressionWindow > 0 {
			if last, ok := lastEventAt[d.MAC]; ok && last.kind == kind && now.Sub(last.at) < cfg.FlapSuppressionWindow {
				// 窗口期内同类事件压制
				if kind == EventOnline {
					emitDecision(DecisionFlapSuppressOnline, d.MAC, "")
				} else {
					emitDecision(DecisionFlapSuppressOffline, d.MAC, "")
				}
				return
			}
		}
		lastEventAt[d.MAC] = lastEvent{at: now, kind: kind}
		if kind == EventOnline {
			emitDecision(DecisionConnectEmitted, d.MAC, fmt.Sprintf("via=poll IP=%s", d.IP))
		} else {
			emitDecision(DecisionOfflineEmitted, d.MAC, fmt.Sprintf("via=poll RSSI=%d", d.RSSI))
		}
		pending = append(pending, Event{Time: now, Kind: kind, Device: d})
	}

	// 清理过期的冷却记录 (DisableCooldown=true 时 cooldown map 始终为空, 无开销)
	for mac, t := range cooldown {
		if now.Sub(t) > cfg.OfflineCooldown {
			delete(cooldown, mac)
		}
	}

	// noteCooldown 统一的冷却期记录入口, DisableCooldown=true 时跳过写入。
	noteCooldown := func(mac string) {
		if cfg.DisableCooldown {
			return
		}
		cooldown[mac] = now
	}
	// inCooldown DisableCooldown=true 时永远返回 false。
	inCooldown := func(mac string) bool {
		if cfg.DisableCooldown {
			return false
		}
		cdTime, ok := cooldown[mac]
		return ok && now.Sub(cdTime) < cfg.OfflineCooldown
	}

	for mac, d := range cur {
		delete(misses, mac)
		prev, ok := known[mac]
		switch {
		case !ok:
			// 冷却期检查: 刚判离线的设备不立即触发上线
			if inCooldown(mac) {
				// 冷却期内: 信号必须恢复到较强水平才允许上线
				if d.RSSI != 0 && d.RSSI < cfg.CooldownReleaseRSSI {
					// 静默加入 known, 并刷新 cooldown 保持抑制状态,
					// 避免冷却期自然过期后又走一遍完整的 miss 计数 → 重复离线
					cooldown[mac] = now
					known[mac] = d
					emitDecision(DecisionCooldownSuppressOnline, mac, fmt.Sprintf("RSSI=%d", d.RSSI))
					continue
				}
				// 信号恢复正常或 RSSI=0 (有线设备) → 正常上线
				delete(cooldown, mac)
				emitDecision(DecisionCooldownCleared, mac, fmt.Sprintf("RSSI=%d", d.RSSI))
			}
			emitIfNotSuppressed(EventOnline, d)
		default:
			if cs := changedFields(prev, d); len(cs) > 0 {
				pending = append(pending, Event{Time: now, Kind: EventChange, Device: d, Changes: cs})
			}
		}
		known[mac] = d
	}

	arpStates := loadARPStates(ctx)

	for mac, d := range known {
		if _, ok := cur[mac]; ok {
			continue
		}

		if rawDev, inAP := apSet[mac]; inAP {
			rssi := rawDev.RSSI
			d.RSSI = rssi
			known[mac] = d

			pingOK := false
			if prober != nil && d.IP != "" {
				pingOK = prober.Reachable(ctx, d.IP)
			}

			if pingOK {
				delete(misses, mac)
				continue
			}

			// ping 不通 + 信号弱 → 加速离线判定
			if rssi != 0 && rssi < cfg.WeakRSSI {
				misses[mac]++
				threshold := cfg.WeakMissThreshold
				if rssi < cfg.ExtremelyWeakRSSI {
					threshold = cfg.ExtremelyWeakMissThreshold
				}
				emitDecision(DecisionPollWeakSignalMiss, mac, fmt.Sprintf("RSSI=%d misses=%d/%d", rssi, misses[mac], threshold))
				if misses[mac] >= threshold {
					if inCooldown(mac) {
						emitDecision(DecisionCooldownSuppressOffline, mac, "")
					} else {
						emitIfNotSuppressed(EventOffline, d)
					}
					noteCooldown(mac)
					delete(known, mac)
					delete(misses, mac)
				}
				continue
			}

			// ping 不通但信号正常 (息屏): 不计入离线
			delete(misses, mac)
			emitDecision(DecisionPollAPSleepProtected, mac, fmt.Sprintf("RSSI=%d", rssi))
			continue
		}

		// AP 关联表也没了
		arp, hasARP := arpStates[mac]
		if !hasARP && d.IP != "" {
			arp, hasARP = arpStates["_ip:"+d.IP]
		}
		if hasARP && (arp.State == "FAILED" || arp.State == "INCOMPLETE") {
			emitDecision(DecisionPollARPFailedOffline, mac, fmt.Sprintf("state=%s", arp.State))
			if inCooldown(mac) {
				emitDecision(DecisionCooldownSuppressOffline, mac, "")
			} else {
				emitIfNotSuppressed(EventOffline, d)
			}
			noteCooldown(mac)
			delete(known, mac)
			delete(misses, mac)
			continue
		}

		misses[mac]++
		if misses[mac] >= cfg.OfflineMisses {
			emitDecision(DecisionPollMissesExhausted, mac, fmt.Sprintf("misses=%d/%d", misses[mac], cfg.OfflineMisses))
			if inCooldown(mac) {
				emitDecision(DecisionCooldownSuppressOffline, mac, "")
			} else {
				emitIfNotSuppressed(EventOffline, d)
			}
			noteCooldown(mac)
			delete(known, mac)
			delete(misses, mac)
		}
	}
	return pending
}

// changedFields 列出同一 MAC 在两次快照间发生变化的关键字段;
// 没有变动时返回空切片。
func changedFields(prev, cur Device) []Change {
	var cs []Change
	if prev.IP != cur.IP {
		cs = append(cs, Change{Field: "IP", Old: prev.IP, New: cur.IP})
	}
	if prev.Hostname != cur.Hostname && cur.Hostname != "" {
		cs = append(cs, Change{Field: "Hostname", Old: prev.Hostname, New: cur.Hostname})
	}
	if prev.Radio != cur.Radio {
		cs = append(cs, Change{Field: "Radio", Old: prev.Radio, New: cur.Radio})
	}
	if prev.SSID != cur.SSID {
		cs = append(cs, Change{Field: "SSID", Old: prev.SSID, New: cur.SSID})
	}
	return cs
}
