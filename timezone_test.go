package argus

import (
	"testing"
	"time"
)

func TestParsePosixTZValid(t *testing.T) {
	cases := []struct {
		in     string
		name   string
		offset int // 预期偏移秒数 (UTC+8 = 28800)
	}{
		{"CST-8", "CST", 8 * 3600},
		{"UTC0", "UTC", 0},
		{"BST-1", "BST", 1 * 3600},
	}
	for _, c := range cases {
		loc := parsePosixTZ(c.in)
		if loc == nil {
			t.Errorf("parsePosixTZ(%q) 返回 nil", c.in)
			continue
		}
		// 用一个固定时间戳取偏移
		now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).In(loc)
		_, off := now.Zone()
		if off != c.offset {
			t.Errorf("parsePosixTZ(%q) 偏移 = %d, want %d", c.in, off, c.offset)
		}
	}
}

func TestParsePosixTZEST5EDT(t *testing.T) {
	// "EST5EDT" 只解析前 2 段: EST + 5
	loc := parsePosixTZ("EST5EDT,M3.2.0,M11.1.0")
	if loc == nil {
		t.Fatal("parsePosixTZ 应返回非 nil")
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).In(loc)
	_, off := now.Zone()
	if off != -5*3600 {
		t.Errorf("EST5 偏移 = %d, want -18000", off)
	}
}

func TestParsePosixTZInvalid(t *testing.T) {
	cases := []string{"", "no-digits", ":invalid", "123"}
	for _, c := range cases {
		if loc := parsePosixTZ(c); loc != nil {
			t.Errorf("parsePosixTZ(%q) 应返回 nil, got %v", c, loc)
		}
	}
}

func TestDetectLocalLocationSafe(t *testing.T) {
	// DetectLocalLocation reads /etc/TZ or TZ env var, never mutates globals.
	// On a dev machine with neither set, it legitimately returns nil —
	// we only verify it doesn't panic. SetupLocalTimezone (Deprecated) is
	// intentionally NOT tested here because it mutates time.Local, which
	// would race with any parallel test that reads time.Now().
	_ = DetectLocalLocation()
}
