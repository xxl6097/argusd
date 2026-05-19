package argus

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// HostapdFetcher 适用于 OpenWrt 官方固件: 通过 `ubus call hostapd.<iface> get_clients`
// 列出每个无线接口的关联终端, 通过 `get_status` 取 SSID / 频率 / 信道, 并合并 ARP 表
// 中的有线设备, 最后用 DHCP 租约补全主机名 / IP。
//
// Interfaces 留空时会在每次 Fetch 前自动探测 (调用 `ubus list hostapd.*`)。
type HostapdFetcher struct {
	// Interfaces 是要查询的 hostapd 服务名列表 (例如 ["hostapd.wlan0", "hostapd.wlan1"])。
	// 留空表示动态探测。
	Interfaces []string
	// Timeout 限制单次 ubus 调用耗时; 0 表示不超时。
	Timeout time.Duration
}

// hostapdStatus 对应 `ubus call hostapd.<iface> get_status` 中我们关心的字段。
type hostapdStatus struct {
	SSID    string `json:"ssid"`
	Freq    int    `json:"freq"`
	Channel int    `json:"channel"`
}

// hostapdClients 对应 `ubus call hostapd.<iface> get_clients` 的简化模型。
type hostapdClients struct {
	Freq    int                       `json:"freq"`
	Clients map[string]hostapdClient `json:"clients"`
}

type hostapdClient struct {
	Auth       bool `json:"auth"`
	Assoc      bool `json:"assoc"`
	Authorized bool `json:"authorized"`
	Signal     int  `json:"signal"`
}

// hostapdServiceRe 校验 hostapd 接口名, 仅允许字母数字点下划线短横,
// 避免通过伪造服务名向 exec.Command 传入恶意参数。
//
// 真机接口名形如 `hostapd.phy0-ap0` / `hostapd.wlan0` / `hostapd.ra0`,
// 主线 OpenWrt 的 phy 命名带短横, 必须包含在白名单内 (v1.2.5)。
var hostapdServiceRe = regexp.MustCompile(`^hostapd\.[\w-]+$`)

// Fetch 实现 Fetcher 接口。
func (f HostapdFetcher) Fetch(ctx context.Context) ([]Device, error) {
	if f.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, f.Timeout)
		defer cancel()
	}
	ifaces := f.Interfaces
	if len(ifaces) == 0 {
		ifs, err := listHostapdInterfaces(ctx)
		if err != nil {
			return nil, fmt.Errorf("detect hostapd interfaces: %w", err)
		}
		ifaces = ifs
	}

	now := time.Now()
	hints := loadHints(ctx)
	devs := map[string]Device{}

	// 收集每个无线接口的关联客户端
	for _, iface := range ifaces {
		if !hostapdServiceRe.MatchString(iface) {
			continue
		}
		status, _ := hostapdGetStatus(ctx, iface)
		clients, err := hostapdGetClients(ctx, iface)
		if err != nil {
			continue
		}
		radio := classifyRadio(status.Freq, clients.Freq)
		for mac, c := range clients.Clients {
			// Assoc=true 是 hostapd 客户端在 AP 关联表里的硬指标。
			// 历史上还要求 Authorized=true, 但 OpenWrt mainline (kernel
			// 6.6+) 的 hostapd ubus 接口不再返回 authorized 字段, 默认
			// 零值 false 会让所有 WiFi 客户端被错误跳过, 全部当成有线
			// (v1.2.5 修复)。仅依赖 Assoc 即可正确识别。
			if !c.Assoc {
				continue
			}
			m := normalizeMAC(mac)
			if m == "" {
				continue
			}
			devs[m] = applyHints(Device{
				MAC:      m,
				Radio:    radio,
				SSID:     status.SSID,
				Channel:  status.Channel,
				RSSI:     c.Signal,
				LastSeen: now,
			}, hints[m])
		}
	}

	// 把 ARP/DHCP 中尚未列入的设备视为有线终端 (仅保留路由器 LAN 子网内的)
	for mac, h := range hints {
		if _, ok := devs[mac]; ok {
			continue
		}
		if h.IP == "" || !isPrivateIP(h.IP) {
			continue
		}
		devs[mac] = applyHints(Device{
			MAC:      mac,
			LastSeen: now,
		}, h)
	}

	out := make([]Device, 0, len(devs))
	for _, d := range devs {
		out = append(out, d)
	}
	return out, nil
}

// listHostapdInterfaces 解析 `ubus list hostapd.*`, 返回经过校验的完整服务名列表。
func listHostapdInterfaces(ctx context.Context) ([]string, error) {
	out, err := exec.CommandContext(ctx, "ubus", "list", "hostapd.*").Output()
	if err != nil {
		return nil, err
	}
	var ifaces []string
	for _, line := range strings.Fields(string(out)) {
		if hostapdServiceRe.MatchString(line) {
			ifaces = append(ifaces, line)
		}
	}
	return ifaces, nil
}

func hostapdGetStatus(ctx context.Context, iface string) (hostapdStatus, error) {
	var s hostapdStatus
	out, err := exec.CommandContext(ctx, "ubus", "call", iface, "get_status").Output()
	if err != nil {
		return s, fmt.Errorf("ubus call %s get_status: %w", iface, err)
	}
	if err := json.Unmarshal(out, &s); err != nil {
		return s, fmt.Errorf("parse %s get_status JSON: %w", iface, err)
	}
	return s, nil
}

func hostapdGetClients(ctx context.Context, iface string) (hostapdClients, error) {
	var c hostapdClients
	out, err := exec.CommandContext(ctx, "ubus", "call", iface, "get_clients").Output()
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(out, &c); err != nil {
		return c, err
	}
	return c, nil
}

// classifyRadio 根据频率粗略归类到 2.4G / 5G / 6G。
func classifyRadio(freqs ...int) string {
	for _, f := range freqs {
		if f <= 0 {
			continue
		}
		switch {
		case f >= 2400 && f < 2500:
			return "2.4G"
		case f >= 5000 && f < 6000:
			return "5G"
		case f >= 5925:
			return "6G"
		default:
			return strconv.Itoa(f) + "MHz"
		}
	}
	return ""
}

// isPrivateIP 判断 IPv4 是否属于 RFC 1918 私网段, 避免把 WAN 侧地址引入设备列表。
func isPrivateIP(ip string) bool {
	return strings.HasPrefix(ip, "10.") ||
		strings.HasPrefix(ip, "192.168.") ||
		isIn172(ip)
}

func isIn172(ip string) bool {
	if !strings.HasPrefix(ip, "172.") {
		return false
	}
	parts := strings.SplitN(ip, ".", 3)
	if len(parts) < 2 {
		return false
	}
	n, err := strconv.Atoi(parts[1])
	if err != nil {
		return false
	}
	return n >= 16 && n <= 31
}
