package argus

import (
	"testing"
	"time"
)

func TestDeviceWired(t *testing.T) {
	cases := []struct {
		name  string
		radio string
		want  bool
	}{
		{"radio 为空表示有线", "", true},
		{"5G 为无线", "5G", false},
		{"2.4G 为无线", "2.4G", false},
		{"6G 为无线", "6G", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := Device{Radio: c.radio}
			if got := d.Wired(); got != c.want {
				t.Errorf("Wired(radio=%q) = %v, want %v", c.radio, got, c.want)
			}
		})
	}
}

func TestDeviceZeroValue(t *testing.T) {
	var d Device
	if !d.Wired() {
		t.Error("零值 Device 应该为有线 (Radio 为空)")
	}
	if d.AccessTime != (time.Time{}) {
		t.Error("零值 Device 的 AccessTime 应为零时间")
	}
}
