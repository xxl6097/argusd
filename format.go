package argus

import (
	"fmt"
	"strconv"
	"strings"
)

// 表格列在终端中的目标显示宽度 (按 1 列等宽英文 / 2 列汉字计算)。
const (
	colMAC    = 20
	colIP     = 16
	colHost   = 17
	colVendor = 8
	colType   = 7
	colSignal = 12
)

// TableHeader 返回设备表格的表头和分隔线 (调用方可自行决定是否使用)。
func TableHeader() (header, separator string) {
	header = padRight("MAC 地址", colMAC) + " " +
		padRight("IP 地址", colIP) + " " +
		padRight("主机名", colHost) + " " +
		padRight("厂商", colVendor) + " " +
		padRight("类型", colType) + " " +
		padRight("信号", colSignal) + " " +
		"无线"
	separator = strings.Repeat("─", displayWidth(header))
	return
}

// RenderTable 把设备列表渲染为完整表格 (表头 + 分隔 + 行 + 分隔 + 汇总)。
func RenderTable(devs []Device) string {
	header, sep := TableHeader()
	var b strings.Builder
	b.WriteString(header)
	b.WriteByte('\n')
	b.WriteString(sep)
	b.WriteByte('\n')
	wifi := 0
	for _, d := range devs {
		b.WriteString(d.Row())
		b.WriteByte('\n')
		if !d.Wired() {
			wifi++
		}
	}
	b.WriteString(sep)
	b.WriteByte('\n')
	fmt.Fprintf(&b, "共 %d 台设备在线 (WiFi: %d, 有线: %d)", len(devs), wifi, len(devs)-wifi)
	return b.String()
}

// Row 返回该设备在表格中对应的一行 (列已按显示宽度对齐)。
func (d Device) Row() string {
	host, vendor := hostAndVendor(d.Hostname, d.Vendor)
	return padRight(strings.ToUpper(d.MAC), colMAC) + " " +
		padRight(orDash(d.IP), colIP) + " " +
		padRight(host, colHost) + " " +
		padRight(vendor, colVendor) + " " +
		padRight(typeOrFallback(d.Type, d.Wired()), colType) + " " +
		padRight(signalLabel(d.RSSI), colSignal) + " " +
		wirelessField(d.Radio, d.SSID)
}

// String 返回适合事件日志一行的紧凑形式, 不做列对齐。
func (d Device) String() string {
	host, vendor := hostAndVendor(d.Hostname, d.Vendor)
	return fmt.Sprintf("%s %s %s %s %s %s %s",
		strings.ToUpper(d.MAC),
		orDash(d.IP),
		host,
		vendor,
		typeOrFallback(d.Type, d.Wired()),
		signalLabel(d.RSSI),
		wirelessField(d.Radio, d.SSID),
	)
}

// hostAndVendor 决定主机名和厂商两列的展示值:
// 主机名缺失时用厂商兜底, 厂商列保持原样独立展示。
func hostAndVendor(host, vendor string) (string, string) {
	displayHost := host
	if displayHost == "" {
		displayHost = vendor
	}
	if displayHost == "" {
		displayHost = "Unknown"
	}
	return displayHost, truncVendor(vendor)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// typeOrFallback 在 staType 缺失时, 对有线设备给出 "wired" 兜底, 其他用 "-"。
func typeOrFallback(t string, wired bool) string {
	if t != "" {
		return t
	}
	if wired {
		return "wired"
	}
	return "-"
}

// truncVendor 厂商字段最多保留 6 个显示列, 超出截断为 4 列 + ".."。
func truncVendor(v string) string {
	if v == "" {
		return "-"
	}
	if displayWidth(v) <= 6 {
		return v
	}
	var b strings.Builder
	used := 0
	for _, r := range v {
		w := 1
		if isWide(r) {
			w = 2
		}
		if used+w > 4 {
			break
		}
		b.WriteRune(r)
		used += w
	}
	b.WriteString("..")
	return b.String()
}

// signalLabel 把 RSSI 渲染为 "-45(极强)" 这种带中文强度标签的形式。
func signalLabel(rssi int) string {
	if rssi == 0 {
		return "-"
	}
	var tag string
	switch {
	case rssi >= -50:
		tag = "极强"
	case rssi >= -60:
		tag = "强"
	case rssi >= -70:
		tag = "中"
	case rssi >= -80:
		tag = "弱"
	default:
		tag = "极弱"
	}
	return strconv.Itoa(rssi) + "(" + tag + ")"
}

// wirelessField 渲染 "无线" 列: 有线显示 "wired", 否则 "<频段>/<SSID>"。
func wirelessField(radio, ssid string) string {
	if radio == "" {
		return "wired"
	}
	if ssid == "" {
		return radio
	}
	return radio + "/" + ssid
}

// displayWidth 估算字符串在等宽终端中的显示列数 (CJK / 全角 = 2 列)。
func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		if isWide(r) {
			w += 2
		} else {
			w++
		}
	}
	return w
}

func isWide(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115F:
		return true
	case r >= 0x2E80 && r <= 0x9FFF:
		return true
	case r >= 0xA000 && r <= 0xA4CF:
		return true
	case r >= 0xAC00 && r <= 0xD7A3:
		return true
	case r >= 0xF900 && r <= 0xFAFF:
		return true
	case r >= 0xFE30 && r <= 0xFE4F:
		return true
	case r >= 0xFF00 && r <= 0xFF60:
		return true
	case r >= 0xFFE0 && r <= 0xFFE6:
		return true
	}
	return false
}

// padRight 按显示宽度右补空格。
func padRight(s string, n int) string {
	diff := n - displayWidth(s)
	if diff <= 0 {
		return s
	}
	return s + strings.Repeat(" ", diff)
}
