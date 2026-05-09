package argus

import (
	"strings"
	"testing"
)

func TestSignalLabel(t *testing.T) {
	cases := []struct {
		rssi int
		want string
	}{
		{0, "-"},
		{-40, "-40(极强)"},
		{-50, "-50(极强)"},
		{-55, "-55(强)"},
		{-60, "-60(强)"},
		{-65, "-65(中)"},
		{-70, "-70(中)"},
		{-75, "-75(弱)"},
		{-80, "-80(弱)"},
		{-85, "-85(极弱)"},
		{-95, "-95(极弱)"},
	}
	for _, c := range cases {
		if got := signalLabel(c.rssi); got != c.want {
			t.Errorf("signalLabel(%d) = %q, want %q", c.rssi, got, c.want)
		}
	}
}

func TestOrDash(t *testing.T) {
	if orDash("") != "-" {
		t.Error("空字符串应返回 -")
	}
	if orDash("abc") != "abc" {
		t.Error("非空字符串应原样返回")
	}
}

func TestTypeOrFallback(t *testing.T) {
	cases := []struct {
		typ   string
		wired bool
		want  string
	}{
		{"Phone", false, "Phone"},
		{"Phone", true, "Phone"}, // 有值优先
		{"", true, "wired"},
		{"", false, "-"},
	}
	for _, c := range cases {
		if got := typeOrFallback(c.typ, c.wired); got != c.want {
			t.Errorf("typeOrFallback(%q, %v) = %q, want %q", c.typ, c.wired, got, c.want)
		}
	}
}

func TestTruncVendor(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "-"},
		{"abc", "abc"},           // 3 列, 不截断
		{"abcdef", "abcdef"},     // 6 列, 不截断 (边界)
		{"abcdefg", "abcd.."},    // 7 列 -> 截断到 4+..
		{"raspberrypi", "rasp.."}, // 11 列
		{"中文", "中文"},          // 4 列 (CJK 各 2), 不截断
		{"中文测试", "中文.."},    // 8 列 -> 截断到 2 个汉字+..
	}
	for _, c := range cases {
		if got := truncVendor(c.in); got != c.want {
			t.Errorf("truncVendor(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHostAndVendor(t *testing.T) {
	cases := []struct {
		host, vendor, wantH, wantV string
	}{
		{"iphone", "Apple", "iphone", "Apple"},
		{"", "raspberrypi", "raspberrypi", "rasp.."},
		{"", "", "Unknown", "-"},
		{"lenovo", "", "lenovo", "-"},
	}
	for _, c := range cases {
		h, v := hostAndVendor(c.host, c.vendor)
		if h != c.wantH || v != c.wantV {
			t.Errorf("hostAndVendor(%q, %q) = (%q,%q), want (%q,%q)",
				c.host, c.vendor, h, v, c.wantH, c.wantV)
		}
	}
}

func TestWirelessField(t *testing.T) {
	cases := []struct {
		radio, ssid, want string
	}{
		{"", "", "wired"},
		{"", "avgb", "wired"},
		{"5G", "", "5G"},
		{"5G", "avgb", "5G/avgb"},
	}
	for _, c := range cases {
		if got := wirelessField(c.radio, c.ssid); got != c.want {
			t.Errorf("wirelessField(%q,%q) = %q, want %q", c.radio, c.ssid, got, c.want)
		}
	}
}

func TestDisplayWidth(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"abc", 3},
		{"中文", 4},
		{"a中b", 4},
		{"MAC 地址", 8}, // M A C 空 地 址 = 1+1+1+1+2+2 = 8
	}
	for _, c := range cases {
		if got := displayWidth(c.in); got != c.want {
			t.Errorf("displayWidth(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestPadRight(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"abc", 5, "abc  "},
		{"abc", 3, "abc"},
		{"abc", 2, "abc"},   // 不截断
		{"中文", 5, "中文 "}, // 宽 4, 补 1 空格
	}
	for _, c := range cases {
		if got := padRight(c.in, c.n); got != c.want {
			t.Errorf("padRight(%q,%d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
}

func TestRenderTableShape(t *testing.T) {
	devs := []Device{
		{MAC: "aa:bb:cc:dd:ee:ff", IP: "192.168.1.1", Hostname: "h1", Radio: "5G", SSID: "wifi", RSSI: -50},
		{MAC: "11:22:33:44:55:66", IP: "192.168.1.2", Hostname: "h2"}, // 有线
	}
	out := RenderTable(devs)
	if !strings.Contains(out, "MAC 地址") {
		t.Error("应包含表头 'MAC 地址'")
	}
	if !strings.Contains(out, "共 2 台设备在线 (WiFi: 1, 有线: 1)") {
		t.Error("应包含正确汇总")
	}
	if !strings.Contains(out, "AA:BB:CC:DD:EE:FF") {
		t.Error("MAC 应大写显示")
	}
}

func TestDeviceRowAlignment(t *testing.T) {
	d := Device{MAC: "aa:bb:cc:dd:ee:ff", IP: "192.168.1.1", Radio: "5G", SSID: "wifi", RSSI: -45}
	row := d.Row()
	if !strings.HasPrefix(row, "AA:BB:CC:DD:EE:FF") {
		t.Errorf("Row 应以大写 MAC 开头: %q", row)
	}
	if !strings.Contains(row, "-45(极强)") {
		t.Errorf("Row 应包含信号标签: %q", row)
	}
}

func TestDeviceStringFormat(t *testing.T) {
	d := Device{MAC: "aa:bb:cc:dd:ee:ff", IP: "1.1.1.1", Hostname: "host", Vendor: "Apple", Type: "Phone", Radio: "5G", SSID: "wifi", RSSI: -50}
	s := d.String()
	if !strings.Contains(s, "AA:BB:CC:DD:EE:FF") {
		t.Error("String 应包含大写 MAC")
	}
	if !strings.Contains(s, "5G/wifi") {
		t.Error("String 应包含无线信息")
	}
	if !strings.Contains(s, "-50(极强)") {
		t.Error("String 应包含信号标签")
	}
}

func TestDeviceStringWired(t *testing.T) {
	d := Device{MAC: "aa:bb:cc:dd:ee:ff", IP: "1.1.1.1"}
	s := d.String()
	if !strings.Contains(s, "wired") {
		t.Error("有线设备 String 应包含 wired")
	}
}

func TestIsWideCJKRanges(t *testing.T) {
	// 覆盖更多 CJK 范围
	cases := []struct {
		r    rune
		want bool
	}{
		{'a', false},
		{'中', true},                  // 0x4E2D, CJK 主区
		{'あ', true},                  // 0x3042, 在 0x2E80-0x9FFF 范围
		{'가', true},                  // 0xAC00, 韩文
		{rune(0xFF01), true},          // 全角感叹号
		{rune(0xFFE5), true},          // 全角人民币
		{rune(0xFE30), true},          // CJK 兼容形式起点
		{rune(0x1100), true},          // 谚文初声起点
		{rune(0xF900), true},          // CJK 兼容起点
		{rune(0xA000), true},          // 彝文起点
		{rune(0xFE4F), true},          // CJK 兼容形式终点
	}
	for _, c := range cases {
		if got := isWide(c.r); got != c.want {
			t.Errorf("isWide(%U) = %v, want %v", c.r, got, c.want)
		}
	}
}

func TestTableHeader(t *testing.T) {
	h, sep := TableHeader()
	if !strings.Contains(h, "MAC 地址") || !strings.Contains(h, "IP 地址") {
		t.Errorf("表头缺字段: %q", h)
	}
	// 分隔线长度应匹配显示宽度
	if displayWidth(sep) != displayWidth(h) {
		t.Errorf("分隔线宽度 %d 不等于表头宽度 %d", displayWidth(sep), displayWidth(h))
	}
}
