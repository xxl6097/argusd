package argus

import (
	"encoding/json"
	"fmt"
	"time"
)

// DecisionKind 表示判定链路上的决策点类型。
// 每个决策代表 Watcher 内部的一次具体判断 (进入/跳过某个分支),
// 供上层观测库的"为什么触发/为什么没触发"过程, 便于调参和调试。
type DecisionKind int

const (
	// --- 上线判定 (handleConnectHint / diff) ---

	// DecisionConnectHintReceived: 收到 syslog 接入事件 hint (WPA完成/MAC表新增/DHCP分配)。
	DecisionConnectHintReceived DecisionKind = 1
	// DecisionConnectSkippedKnown: 已在 known 中, 防重复跳过。
	DecisionConnectSkippedKnown DecisionKind = 2
	// DecisionConnectEmitted: 基于 hint 信息构建基础记录并触发 EventOnline。
	DecisionConnectEmitted DecisionKind = 3
	// DecisionCooldownSuppressOnline: 冷却期 + 弱信号, 静默更新 known, 不触发 Online。
	DecisionCooldownSuppressOnline DecisionKind = 4
	// DecisionCooldownCleared: 冷却期内信号恢复到强 (或 RSSI 未知), 清除冷却, 允许 Online。
	DecisionCooldownCleared DecisionKind = 5
	// DecisionFlapSuppressOnline: 窗口期内同类 Online 事件被压制。
	DecisionFlapSuppressOnline DecisionKind = 6

	// --- 离线判定 (handleDisconnectHint / diff) ---

	// DecisionDisconnectHintReceived: 收到 syslog 断开事件 hint (Disconnect/Deauth/Del Sta)。
	DecisionDisconnectHintReceived DecisionKind = 20
	// DecisionDisconnectIgnoredUnknown: 不在 known 中, 忽略。
	DecisionDisconnectIgnoredUnknown DecisionKind = 21
	// DecisionDisconnectPingOK: 500ms 后 ping 仍可达 (漫游), 不触发离线。
	DecisionDisconnectPingOK DecisionKind = 22
	// DecisionOfflineEmitted: 触发 EventOffline。
	DecisionOfflineEmitted DecisionKind = 23
	// DecisionFlapSuppressOffline: 窗口期内同类 Offline 事件被压制。
	DecisionFlapSuppressOffline DecisionKind = 24
	// DecisionCooldownSuppressOffline: 冷却期内重复离线被静默移除, 不重复触发事件。
	DecisionCooldownSuppressOffline DecisionKind = 25
	// DecisionDisconnectSkippedInflight: 同一 MAC 已有正在处理的 disconnect worker,
	// 后续重复 hint (典型: disconnect/deauth/Del Sta 三连发) 直接跳过, 不再各自
	// 进入 500ms Sleep + ping 流程。
	DecisionDisconnectSkippedInflight DecisionKind = 26
	// DecisionOfflineReverted: 离线判定中途收到接入 hint, 撤销本次 Offline 触发。
	// v1.1.0+: 修复"边缘信号闪断 → 立即重连"场景被错误识别为离线的问题。
	DecisionOfflineReverted DecisionKind = 27
	// DecisionDisconnectAlreadyOffline: handleDisconnectHint 收到断开 hint 时,
	// 该 MAC 仍在 offlineCooldown 窗口内 — 之前 diff 路径已经发出过 Offline。
	// 静默清理 known, 不重复 emit。
	// v1.2.1+: 修复弱信号设备先被 diff 报离线、8 分钟后真断开时又被
	// handleDisconnectHint 重报一次的 bug。
	DecisionDisconnectAlreadyOffline DecisionKind = 28
	// DecisionOfflineDedupedAppLayer: 应用层视角已感知该 MAC 离线
	// (lastEventAt[mac].kind == EventOffline 且其后未发出过 Online),
	// 任何重复的离线触发都跳过。覆盖 syslog 路径和 diff 路径。
	// v1.2.2+: 修复"设备睡眠 → diff 报离线 → 8 分钟后真离开 AP →
	// syslog 又报一次离线"的应用层重复事件问题; 比 v1.2.1 的 90s
	// cooldown 检查更宽松, 不受时间限制。
	DecisionOfflineDedupedAppLayer DecisionKind = 29

	// --- diff 轮询分支 ---

	// DecisionPollAPSleepProtected: 设备在 AP 关联表, RSSI 正常 ping 不通 → 息屏保护, 不计离线。
	DecisionPollAPSleepProtected DecisionKind = 40
	// DecisionPollWeakSignalMiss: 弱信号且 ping 不通, 累加 miss 计数 (可能尚未达阈值)。
	DecisionPollWeakSignalMiss DecisionKind = 41
	// DecisionPollARPFailedOffline: AP 关联表也没了, ARP 状态 FAILED/INCOMPLETE, 立即离线。
	DecisionPollARPFailedOffline DecisionKind = 42
	// DecisionPollMissesExhausted: 默认 miss 计数达阈值, 触发离线。
	DecisionPollMissesExhausted DecisionKind = 43
)

// String 返回决策类型的稳定英文标识, 便于日志 / grep / 序列化。
func (k DecisionKind) String() string {
	switch k {
	case DecisionConnectHintReceived:
		return "CONNECT_HINT"
	case DecisionConnectSkippedKnown:
		return "CONNECT_SKIP_KNOWN"
	case DecisionConnectEmitted:
		return "CONNECT_EMIT"
	case DecisionCooldownSuppressOnline:
		return "COOLDOWN_SUPPRESS_ONLINE"
	case DecisionCooldownCleared:
		return "COOLDOWN_CLEARED"
	case DecisionFlapSuppressOnline:
		return "FLAP_SUPPRESS_ONLINE"
	case DecisionDisconnectHintReceived:
		return "DISCONNECT_HINT"
	case DecisionDisconnectIgnoredUnknown:
		return "DISCONNECT_IGNORE_UNKNOWN"
	case DecisionDisconnectPingOK:
		return "DISCONNECT_PING_OK"
	case DecisionOfflineEmitted:
		return "OFFLINE_EMIT"
	case DecisionFlapSuppressOffline:
		return "FLAP_SUPPRESS_OFFLINE"
	case DecisionCooldownSuppressOffline:
		return "COOLDOWN_SUPPRESS_OFFLINE"
	case DecisionDisconnectSkippedInflight:
		return "DISCONNECT_SKIP_INFLIGHT"
	case DecisionOfflineReverted:
		return "OFFLINE_REVERTED"
	case DecisionDisconnectAlreadyOffline:
		return "DISCONNECT_ALREADY_OFFLINE"
	case DecisionOfflineDedupedAppLayer:
		return "OFFLINE_DEDUPED_APPLAYER"
	case DecisionPollAPSleepProtected:
		return "POLL_SLEEP_PROTECT"
	case DecisionPollWeakSignalMiss:
		return "POLL_WEAK_MISS"
	case DecisionPollARPFailedOffline:
		return "POLL_ARP_FAILED"
	case DecisionPollMissesExhausted:
		return "POLL_MISSES_EXHAUSTED"
	}
	return "DECISION_UNKNOWN"
}

// Label 返回中文文案, 适合直接展示。
func (k DecisionKind) Label() string {
	switch k {
	case DecisionConnectHintReceived:
		return "收到接入提示"
	case DecisionConnectSkippedKnown:
		return "跳过(已知)"
	case DecisionConnectEmitted:
		return "发出上线"
	case DecisionCooldownSuppressOnline:
		return "冷却期抑制上线"
	case DecisionCooldownCleared:
		return "冷却期解除"
	case DecisionFlapSuppressOnline:
		return "抖动抑制上线"
	case DecisionDisconnectHintReceived:
		return "收到断开提示"
	case DecisionDisconnectIgnoredUnknown:
		return "跳过(未知)"
	case DecisionDisconnectPingOK:
		return "断开后ping可达"
	case DecisionOfflineEmitted:
		return "发出离线"
	case DecisionFlapSuppressOffline:
		return "抖动抑制离线"
	case DecisionCooldownSuppressOffline:
		return "冷却期抑制离线"
	case DecisionDisconnectSkippedInflight:
		return "跳过(已在处理)"
	case DecisionOfflineReverted:
		return "撤销离线(立即重连)"
	case DecisionDisconnectAlreadyOffline:
		return "断开提示(已离线,跳过)"
	case DecisionOfflineDedupedAppLayer:
		return "已离线(应用层去重,跳过)"
	case DecisionPollAPSleepProtected:
		return "息屏保护"
	case DecisionPollWeakSignalMiss:
		return "弱信号计数"
	case DecisionPollARPFailedOffline:
		return "ARP失败立即离线"
	case DecisionPollMissesExhausted:
		return "连续缺失达阈值"
	}
	return "未知决策"
}

// Decision 是一次内部决策的观测记录, 通过 DecisionHandler 暴露给上层。
// 用于日志 / 监控 / 调试; 业务侧通常只关心 Event, 但需要调参或排障时 Decision
// 提供了完整的判定链路信息。
//
// JSON 序列化: Kind 用英文稳定标识 (见 DecisionKind.MarshalJSON)。
type Decision struct {
	Time   time.Time    `json:"time"`
	Kind   DecisionKind `json:"kind"`
	MAC    string       `json:"mac"`
	Detail string       `json:"detail,omitempty"` // 可选的人类可读上下文 (如 "RSSI=-75 misses=3/5")
}

// String 返回紧凑单行表示, 适合直接写入日志。
func (d Decision) String() string {
	ts := d.Time.Format("2006-01-02 15:04:05")
	if d.Detail == "" {
		return fmt.Sprintf("[%s] [决策] %s %s", ts, d.Kind.Label(), d.MAC)
	}
	return fmt.Sprintf("[%s] [决策] %s %s (%s)", ts, d.Kind.Label(), d.MAC, d.Detail)
}

// MarshalJSON 序列化为 DecisionKind.String() 的英文稳定标识 (如 "CONNECT_EMIT"),
// 而非整数值。整数值在 STABILITY.md 中被标为 Evolving, 字符串则稳定。
func (k DecisionKind) MarshalJSON() ([]byte, error) {
	return json.Marshal(k.String())
}

// DecisionHandler 接收内部决策观测。可为 nil, 为 nil 时决策不收集, 完全零成本。
type DecisionHandler func(Decision)
