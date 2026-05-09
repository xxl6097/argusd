package argus

import "time"

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

// Change 描述某个字段从旧值变为新值的细节, 仅在 EventChange 中使用。
type Change struct {
	Field string // "IP" / "Hostname" / "Radio" / "SSID"
	Old   string
	New   string
}

// Event 是一次设备状态迁移的完整描述, 通过 Watcher.Run 的回调投递。
type Event struct {
	Time    time.Time // 事件被观察到的时刻
	Kind    EventKind
	Device  Device   // 当前快照 (对 EventOffline 表示设备最后一次的快照)
	Changes []Change // 仅 EventChange 时填充
}
