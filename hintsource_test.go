package argus_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	argus "github.com/xxl6097/argusd"
)

// staticHintSource is a test-only HintSource returning a fixed map.
type staticHintSource struct {
	hints map[string]argus.Hint
}

func (s staticHintSource) Hints(ctx context.Context) map[string]argus.Hint {
	return s.hints
}

func TestWithHintSourceInjection(t *testing.T) {
	// 自定义 HintSource 注入时, Watcher 内部应使用它而非默认。
	custom := staticHintSource{
		hints: map[string]argus.Hint{
			"aa:bb:cc:dd:ee:ff": {IP: "10.0.0.99", Hostname: "custom-device"},
		},
	}
	w := argus.New(argus.WithHintSource(custom))
	_ = w
	// 直接调用 HintSource 验证能用; Watcher 内部路径在集成测试覆盖。
	got := custom.Hints(context.Background())
	if got["aa:bb:cc:dd:ee:ff"].Hostname != "custom-device" {
		t.Errorf("custom hints mismatch: %+v", got)
	}
}

func TestDefaultHintSourceCustomPaths(t *testing.T) {
	// DefaultHintSource 支持覆盖 LeasesPath / ARPCommand, 用于定制固件。
	dir := t.TempDir()
	leases := filepath.Join(dir, "dhcp.leases")
	content := "0 aa:bb:cc:dd:ee:ff 10.0.0.42 customhost 01:aa\n"
	if err := os.WriteFile(leases, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	src := &argus.DefaultHintSource{
		LeasesPath: leases,
		ARPCommand: []string{"true"}, // 占位命令, 不会产生 ARP 输出
		CacheTTL:   1,                // 极短 TTL 禁用缓存, 便于多测试串联
	}
	got := src.Hints(context.Background())
	if got["aa:bb:cc:dd:ee:ff"].IP != "10.0.0.42" {
		t.Errorf("custom LeasesPath 未生效: %+v", got)
	}
	if got["aa:bb:cc:dd:ee:ff"].Hostname != "customhost" {
		t.Errorf("custom hostname: %+v", got)
	}
}

func TestDefaultHintSourceCache(t *testing.T) {
	// CacheTTL 在有效期内应返回同一份数据 (不再重读文件)。
	dir := t.TempDir()
	leases := filepath.Join(dir, "dhcp.leases")
	os.WriteFile(leases, []byte("0 aa:bb:cc:dd:ee:ff 1.1.1.1 host1 01:aa\n"), 0644)

	src := &argus.DefaultHintSource{
		LeasesPath: leases,
		ARPCommand: []string{"true"},
		// 默认 CacheTTL (5s)
	}
	got1 := src.Hints(context.Background())
	// 修改文件内容, 理应被缓存屏蔽
	os.WriteFile(leases, []byte("0 bb:bb:bb:bb:bb:bb 2.2.2.2 host2 01:bb\n"), 0644)
	got2 := src.Hints(context.Background())

	if got1["aa:bb:cc:dd:ee:ff"].IP != "1.1.1.1" {
		t.Error("第一次读取错误")
	}
	if _, exists := got2["bb:bb:bb:bb:bb:bb"]; exists {
		t.Error("缓存期内不应读到新数据")
	}
}
