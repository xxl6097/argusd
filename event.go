package argus

import (
	"encoding/json"
	"fmt"
	"time"
)

// EventKind 表示设备状态迁移的类型。
type EventKind int

const (
	// EventOnline: 之前未见到的设备出现在了拉取结果里。
	EventOnline EventKind = iota + 1
	// EventOffline: 已知设备连续多次未出现在拉取结果里。
	EventOffline
	// EventChange: 已知设备的关键属性 (IP / 主机名 / 频段 / SSID) 发生了变化。
	EventChange
)

// String 返回事件类型的稳定状态码 (英文, 适合日志 / 序列化 / 上报)。
// 需要展示给终端用户的中文文案请使用 Label。
func (k EventKind) String() string {
	switch k {
	case EventOnline:
		return "ONLINE"
	case EventOffline:
		return "OFFLINE"
	case EventChange:
		return "CHANGE"
	}
	return "UNKNOWN"
}

// Label 返回事件类型对应的中文文案, 适合直接展示。
func (k EventKind) Label() string {
	switch k {
	case EventOnline:
		return "设备上线"
	case EventOffline:
		return "设备离线"
	case EventChange:
		return "状态变更"
	}
	return "未知事件"
}

// MarshalJSON 将 EventKind 序列化为稳定字符串 (String() 的结果),
// 而非整数值。整数值可能在 minor 版本间变化; 字符串保证稳定。
func (k EventKind) MarshalJSON() ([]byte, error) {
	return json.Marshal(k.String())
}

// UnmarshalJSON 支持双向兼容: 既接受字符串 ("ONLINE" / "OFFLINE" / "CHANGE"),
// 也接受老的整数表示, 便于从外部数据源回读。
func (k *EventKind) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		switch s {
		case "ONLINE":
			*k = EventOnline
		case "OFFLINE":
			*k = EventOffline
		case "CHANGE":
			*k = EventChange
		default:
			return fmt.Errorf("argus: unknown EventKind %q", s)
		}
		return nil
	}
	// 回退: 尝试 int
	var n int
	if err := json.Unmarshal(data, &n); err != nil {
		return err
	}
	*k = EventKind(n)
	return nil
}

// Change 描述某个字段从旧值变为新值的细节, 仅在 EventChange 中使用。
type Change struct {
	Field string `json:"field"` // "IP" / "Hostname" / "Radio" / "SSID"
	Old   string `json:"old"`
	New   string `json:"new"`
}

// Event 是一次设备状态迁移的完整描述, 通过 Watcher.Run 的回调投递。
//
// JSON 序列化: 字段名固化在下方 `json:` 标签中, 从 v0.6.0 起这些 JSON key
// 属于 STABILITY.md 中的 Stable 公共面。Kind 序列化为英文字符串 (ONLINE /
// OFFLINE / CHANGE), 保证跨版本稳定——详见 EventKind.MarshalJSON。
type Event struct {
	Time    time.Time `json:"time"`
	Kind    EventKind `json:"kind"`
	Device  Device    `json:"device"`            // 当前快照 (对 EventOffline 表示设备最后一次的快照)
	Changes []Change  `json:"changes,omitempty"` // 仅 EventChange 时填充
}
