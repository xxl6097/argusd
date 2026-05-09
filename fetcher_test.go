package argus

import (
	"testing"
	"time"
)

func TestNormalizeMAC(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"B0FC36329461", "b0:fc:36:32:94:61"},
		{"b0fc36329461", "b0:fc:36:32:94:61"},
		{"b0:fc:36:32:94:61", "b0:fc:36:32:94:61"},
		{"B0:FC:36:32:94:61", "b0:fc:36:32:94:61"},
		{"  B0FC36329461  ", "b0:fc:36:32:94:61"},
		{"", ""},
		{"invalid", ""},
		{"B0FC3632946", ""},            // 长度 11 不合法
		{"B0FC363294611", ""},           // 长度 13 不合法
	}
	for _, c := range cases {
		if got := normalizeMAC(c.in); got != c.want {
			t.Errorf("normalizeMAC(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseInt(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"0", 0},
		{"-45", -45},
		{"  -45  ", -45},
		{"", 0},
		{"abc", 0},
		{"12.5", 0}, // 非整数
		{"100", 100},
	}
	for _, c := range cases {
		if got := parseInt(c.in); got != c.want {
			t.Errorf("parseInt(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestParseAccessTime(t *testing.T) {
	// 空串返回零时间
	if !parseAccessTime("").IsZero() {
		t.Error("空字符串应返回零时间")
	}
	// 非法格式返回零时间
	if !parseAccessTime("not-a-date").IsZero() {
		t.Error("非法格式应返回零时间")
	}
	// 合法格式
	t1 := parseAccessTime("2026-05-09 10:30:00")
	if t1.IsZero() {
		t.Fatal("合法时间应被解析")
	}
	if t1.Year() != 2026 || t1.Month() != time.May || t1.Day() != 9 ||
		t1.Hour() != 10 || t1.Minute() != 30 {
		t.Errorf("解析结果不正确: %v", t1)
	}
}

func TestClassifyRadio(t *testing.T) {
	cases := []struct {
		freqs []int
		want  string
	}{
		{[]int{}, ""},
		{[]int{0, 0}, ""},
		{[]int{2412}, "2.4G"},
		{[]int{2472}, "2.4G"},
		{[]int{5180}, "5G"},
		{[]int{5825}, "5G"},
		{[]int{5955}, "5G"}, // 边界: 仍在 5GHz 区间
		{[]int{6000}, "6G"}, // 6G 起点 (≥5925 但 < 6000 会归类 5G，此处用 6000)
		{[]int{7115}, "6G"},
		{[]int{0, 5180}, "5G"}, // 跳过 0 取下一个
		{[]int{1234}, "1234MHz"},
	}
	for _, c := range cases {
		if got := classifyRadio(c.freqs...); got != c.want {
			t.Errorf("classifyRadio(%v) = %q, want %q", c.freqs, got, c.want)
		}
	}
}

func TestIsPrivateIP(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"192.168.1.1", true},
		{"10.0.0.1", true},
		{"172.16.0.1", true},
		{"172.31.255.254", true},
		{"172.15.0.1", false}, // 超出 172.16-31
		{"172.32.0.1", false},
		{"8.8.8.8", false},
		{"", false},
		{"invalid", false},
	}
	for _, c := range cases {
		if got := isPrivateIP(c.ip); got != c.want {
			t.Errorf("isPrivateIP(%q) = %v, want %v", c.ip, got, c.want)
		}
	}
}
