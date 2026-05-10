package argus

import (
	"context"
	"testing"
	"time"
)

// fakeProber 用于测试, 通过 map 控制 IP 可达性
type fakeProber struct {
	reachable map[string]bool
}

func (f fakeProber) Reachable(ctx context.Context, ip string) bool {
	return f.reachable[ip]
}

func TestFilterAliveNilProber(t *testing.T) {
	devs := map[string]Device{
		"aa:bb:cc:dd:ee:ff": {MAC: "aa:bb:cc:dd:ee:ff", IP: "1.1.1.1"},
	}
	got := filterAlive(context.Background(), devs, nil)
	if len(got) != 1 {
		t.Errorf("nil prober 应返回全部设备, got %d", len(got))
	}
}

func TestFilterAliveEmptyIP(t *testing.T) {
	devs := map[string]Device{
		"aa:bb:cc:dd:ee:ff": {MAC: "aa:bb:cc:dd:ee:ff", IP: ""},
	}
	// 即使 prober 会返回 false, 空 IP 设备也被保留
	p := fakeProber{reachable: map[string]bool{}}
	got := filterAlive(context.Background(), devs, p)
	if len(got) != 1 {
		t.Errorf("空 IP 设备应被保留, got %d", len(got))
	}
}

func TestFilterAliveMixed(t *testing.T) {
	devs := map[string]Device{
		"aa": {MAC: "aa", IP: "1.1.1.1"}, // 可达
		"bb": {MAC: "bb", IP: "2.2.2.2"}, // 不可达
		"cc": {MAC: "cc", IP: ""},        // 无 IP, 保留
	}
	p := fakeProber{reachable: map[string]bool{
		"1.1.1.1": true,
		"2.2.2.2": false,
	}}
	got := filterAlive(context.Background(), devs, p)
	if _, ok := got["aa"]; !ok {
		t.Error("aa (可达) 应被保留")
	}
	if _, ok := got["bb"]; ok {
		t.Error("bb (不可达) 应被剔除")
	}
	if _, ok := got["cc"]; !ok {
		t.Error("cc (空 IP) 应被保留")
	}
}

func TestICMPProberInvalidIP(t *testing.T) {
	p := ICMPProber{Timeout: 100 * time.Millisecond}
	// 恶意 IP 应被正则拦截, 不触发 ping
	if p.Reachable(context.Background(), "127.0.0.1; rm -rf /") {
		t.Error("非法 IP 应返回 false")
	}
	if p.Reachable(context.Background(), "not-an-ip") {
		t.Error("非法格式应返回 false")
	}
	if p.Reachable(context.Background(), "256.256.256.256") {
		t.Error("超出范围的 IP 应返回 false")
	}
}

func TestICMPProberEmptyIP(t *testing.T) {
	p := ICMPProber{Timeout: 100 * time.Millisecond}
	// 空 IP 视为信任 AP 关联表, 返回 true
	if !p.Reachable(context.Background(), "") {
		t.Error("空 IP 应返回 true")
	}
}

func TestICMPProberZeroTimeoutRoundsUp(t *testing.T) {
	// Zero Timeout rounds up to the 1s minimum; we only care that the call
	// path is exercised, not the outcome (ping may need root).
	p := ICMPProber{}
	// 127.0.0.1 should be reachable on loopback without root; the test
	// does not assert the return value because CI sandboxes vary.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = p.Reachable(ctx, "127.0.0.1")
}
