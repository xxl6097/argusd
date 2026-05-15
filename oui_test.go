package argus

import "testing"

func TestLookupVendor(t *testing.T) {
	cases := []struct {
		mac, want string
	}{
		// Apple iPhone — colon form, mixed case
		{"B8:27:EB:11:22:33", "Raspberry Pi"},
		{"b8:27:eb:11:22:33", "Raspberry Pi"},
		{"DCA632010203", "Raspberry Pi"}, // no separators

		// Xiaomi
		{"04:CF:8C:DE:AD:BE", "Xiaomi"},

		// Espressif (ESP8266 / ESP32 — common DIY IoT prefix)
		{"5C:CF:7F:00:00:01", "Espressif"},

		// Apple
		{"3C:07:54:AA:BB:CC", "Apple"},

		// Locally-administered (Docker default range 02:42:*) → ""
		// because the OUI is meaningless for randomized MACs.
		{"02:42:AC:11:00:02", ""},
		// iOS private MAC (random first byte with bit 1 set, e.g. AA:**)
		{"AA:BB:CC:DD:EE:FF", ""},

		// Unknown OUI → ""
		{"00:00:00:00:00:00", ""},

		// Garbage → ""
		{"", ""},
		{"not-a-mac", ""},
		{"12:34", ""},
	}
	for _, tc := range cases {
		got := LookupVendor(tc.mac)
		if got != tc.want {
			t.Errorf("LookupVendor(%q) = %q, want %q", tc.mac, got, tc.want)
		}
	}
}

func TestApplyHintsFillsVendorFromOUI(t *testing.T) {
	// fetcher 没提供 Vendor (典型 hostapd 路径), applyHints 应该从 MAC 查到。
	d := Device{MAC: "B8:27:EB:11:22:33"}
	out := applyHints(d, Hint{})
	if out.Vendor != "Raspberry Pi" {
		t.Errorf("Vendor = %q, want %q", out.Vendor, "Raspberry Pi")
	}

	// fetcher 已经提供了 Vendor (ahsapd 路径), 不应被覆盖。
	d2 := Device{MAC: "B8:27:EB:11:22:33", Vendor: "MyVendor"}
	out2 := applyHints(d2, Hint{})
	if out2.Vendor != "MyVendor" {
		t.Errorf("已有 Vendor 不应被覆盖, got %q", out2.Vendor)
	}

	// 未知 MAC → Vendor 仍为空 (UI 显示 "—")
	d3 := Device{MAC: "00:00:00:11:22:33"}
	out3 := applyHints(d3, Hint{})
	if out3.Vendor != "" {
		t.Errorf("未知 OUI 应保留空 Vendor, got %q", out3.Vendor)
	}
}
