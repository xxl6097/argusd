package argus_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	argus "github.com/xxl6097/argus"
)

// TestEventJSONRoundTrip 验证 Event → JSON → Event 的稳定性。
// 字段名使用 STABILITY.md 承诺的 snake_case key; Kind 使用英文字符串而非整数。
func TestEventJSONRoundTrip(t *testing.T) {
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	original := argus.Event{
		Time: now,
		Kind: argus.EventOnline,
		Device: argus.Device{
			MAC:      "aa:bb:cc:dd:ee:ff",
			IP:       "192.168.1.1",
			Hostname: "test",
			RSSI:     -50,
			Radio:    "5G",
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal err=%v", err)
	}

	s := string(data)
	// 关键承诺: 稳定字段名
	for _, want := range []string{`"kind":"ONLINE"`, `"mac":"aa:bb:cc:dd:ee:ff"`, `"ip":"192.168.1.1"`, `"rssi":-50`, `"radio":"5G"`} {
		if !strings.Contains(s, want) {
			t.Errorf("JSON 应包含 %s, 实际: %s", want, s)
		}
	}
	// omitempty 生效: 未填充字段 (Vendor, Type 等) 不应出现
	for _, unwanted := range []string{`"vendor"`, `"type"`, `"ssid"`, `"changes"`} {
		if strings.Contains(s, unwanted) {
			t.Errorf("JSON 不应包含空字段 %s, 实际: %s", unwanted, s)
		}
	}

	var back argus.Event
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("Unmarshal err=%v", err)
	}
	if back.Kind != original.Kind {
		t.Errorf("Kind 往返后不一致: %v vs %v", back.Kind, original.Kind)
	}
	if back.Device.MAC != original.Device.MAC {
		t.Errorf("MAC 往返后不一致: %q vs %q", back.Device.MAC, original.Device.MAC)
	}
	if back.Device.RSSI != -50 {
		t.Errorf("RSSI 往返后不一致: %d", back.Device.RSSI)
	}
}

func TestEventKindUnmarshalFromInt(t *testing.T) {
	// 向后兼容: 可以从整数反序列化 (支持旧数据)
	var k argus.EventKind
	if err := json.Unmarshal([]byte("1"), &k); err != nil {
		t.Fatalf("从整数反序列化失败: %v", err)
	}
	if k != argus.EventOnline {
		t.Errorf("got %v, want EventOnline", k)
	}
}

func TestEventKindUnmarshalUnknown(t *testing.T) {
	var k argus.EventKind
	err := json.Unmarshal([]byte(`"NONSENSE"`), &k)
	if err == nil {
		t.Error("未知字符串应返回错误")
	}
}

func TestChangeJSONFields(t *testing.T) {
	c := argus.Change{Field: "IP", Old: "1.1.1.1", New: "2.2.2.2"}
	data, _ := json.Marshal(c)
	s := string(data)
	for _, want := range []string{`"field":"IP"`, `"old":"1.1.1.1"`, `"new":"2.2.2.2"`} {
		if !strings.Contains(s, want) {
			t.Errorf("JSON 应含 %s, got %s", want, s)
		}
	}
}

func TestConfigJSONRoundTrip(t *testing.T) {
	cfg := argus.Config{
		PollInterval:      2 * time.Second,
		OfflineMisses:     10,
		WeakRSSI:          -75,
		DisableCooldown:   true,
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	s := string(data)
	for _, want := range []string{
		`"poll_interval":2000000000`,
		`"offline_misses":10`,
		`"weak_rssi":-75`,
		`"disable_cooldown":true`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("JSON 应含 %s, got %s", want, s)
		}
	}
	// omitempty 生效: 未设置的字段不出现
	if strings.Contains(s, "fetch_timeout") {
		t.Errorf("零值字段不应出现: %s", s)
	}

	var back argus.Config
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if back.PollInterval != cfg.PollInterval {
		t.Errorf("PollInterval: %v vs %v", back.PollInterval, cfg.PollInterval)
	}
	if back.DisableCooldown != true {
		t.Errorf("DisableCooldown: %v", back.DisableCooldown)
	}
}

func TestDecisionJSONFields(t *testing.T) {
	d := argus.Decision{
		Time:   time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC),
		Kind:   argus.DecisionConnectEmitted,
		MAC:    "aa:bb:cc:dd:ee:ff",
		Detail: "IP=1.2.3.4",
	}
	data, _ := json.Marshal(d)
	s := string(data)
	for _, want := range []string{
		`"kind":"CONNECT_EMIT"`,
		`"mac":"aa:bb:cc:dd:ee:ff"`,
		`"detail":"IP=1.2.3.4"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("JSON 应含 %s, got %s", want, s)
		}
	}
}
