package argus

import "time"

// Device 是已接入设备的归一化记录, 由 Fetcher 产出, 也是事件回调中携带的载荷。
//
// 调用方需要的所有信息都在结构体字段里, 不再依赖原始 ubus 字符串。
type Device struct {
	MAC        string        // 小写冒号格式, 例如 "aa:bb:cc:dd:ee:ff"
	IP         string        // IPv4 地址, 拿不到时为空
	Hostname   string        // 主机名, 拿不到时为空
	Vendor     string        // 厂商或设备型号 (来自 ahsapd staVendor)
	Type       string        // 设备类别, 如 "Phone" / "PC", 拿不到时为空
	Radio      string        // "2.4G" / "5G", 有线接入时为空
	SSID       string        // 关联的 SSID, 有线时为空
	Channel    int           // 信道号, 0 表示未知
	RSSI       int           // 信号强度 dBm, 0 表示未知
	UpTime     time.Duration // 已接入时长
	AccessTime time.Time     // 设备接入时刻 (路由器本机时区)
	LastSeen   time.Time     // 库最近一次观察到该设备的时刻
}

// Wired 判断设备是否走有线接入 (没有无线频段信息)。
func (d Device) Wired() bool { return d.Radio == "" }
