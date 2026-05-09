package argus

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDHCPLeases(t *testing.T) {
	// 创建临时租约文件
	dir := t.TempDir()
	path := filepath.Join(dir, "dhcp.leases")
	content := `1778284462 ba:79:97:73:89:8d 192.168.1.213 * 01:ba:79:97:73:89:8d
0 b0:fc:36:32:94:61 192.168.1.5 lenovo 01:b0:fc:36:32:94:61
1778282048 ca:5e:67:ab:ff:87 192.168.1.154 iphone 01:ca:5e:67:ab:ff:87
short line
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	hints := map[string]hint{}
	loadDHCPLeases(path, hints)

	// 期望 3 条解析成功
	if len(hints) != 3 {
		t.Errorf("解析租约数 = %d, want 3", len(hints))
	}
	// * 主机名被转为空串
	if h := hints["ba:79:97:73:89:8d"]; h.Hostname != "" || h.IP != "192.168.1.213" {
		t.Errorf("ba:79:... hint = %+v", h)
	}
	// 正常主机名
	if h := hints["b0:fc:36:32:94:61"]; h.Hostname != "lenovo" || h.IP != "192.168.1.5" {
		t.Errorf("lenovo hint = %+v", h)
	}
	if h := hints["ca:5e:67:ab:ff:87"]; h.Hostname != "iphone" {
		t.Errorf("iphone hint = %+v", h)
	}
}

func TestLoadDHCPLeasesMissingFile(t *testing.T) {
	hints := map[string]hint{}
	loadDHCPLeases("/nonexistent/path/dhcp.leases", hints)
	if len(hints) != 0 {
		t.Error("不存在的文件应不影响 hints")
	}
}

func TestApplyHints(t *testing.T) {
	// 空字段时填入 hint
	d := Device{MAC: "aa:bb:cc:dd:ee:ff"}
	h := hint{IP: "192.168.1.1", Hostname: "host"}
	got := applyHints(d, h)
	if got.IP != "192.168.1.1" || got.Hostname != "host" {
		t.Errorf("空字段应被填入: %+v", got)
	}

	// 已有值不覆盖
	d2 := Device{MAC: "aa:bb:cc:dd:ee:ff", IP: "10.0.0.1", Hostname: "original"}
	got2 := applyHints(d2, h)
	if got2.IP != "10.0.0.1" || got2.Hostname != "original" {
		t.Errorf("已有值不应被覆盖: %+v", got2)
	}
}
