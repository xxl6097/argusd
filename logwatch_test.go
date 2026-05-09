package argus

import "testing"

func TestParseSyslogLineMacTableInsert(t *testing.T) {
	line := "Sat May  9 08:55:45 2026 kern.warn kernel: [129318.193011] 7981@C13L2,MacTableInsertEntry() 1559: New Sta:ba:79:97:73:89:8d"
	ev, ok := parseSyslogLine(line)
	if !ok {
		t.Fatal("应解析成功")
	}
	if ev.Kind != SyslogMacTableInsert {
		t.Errorf("Kind = %v, want SyslogMacTableInsert", ev.Kind)
	}
	if ev.MAC != "ba:79:97:73:89:8d" {
		t.Errorf("MAC = %q", ev.MAC)
	}
}

func TestParseSyslogLineMacTableDelete(t *testing.T) {
	line := "Sat May  9 08:55:45 2026 kern.warn kernel: [129502.111993] 7981@C13L2,MacTableDeleteEntry() 1921: Del Sta:ba:79:97:73:89:8d"
	ev, ok := parseSyslogLine(line)
	if !ok {
		t.Fatal("应解析成功")
	}
	if ev.Kind != SyslogMacTableDelete {
		t.Errorf("Kind = %v, want SyslogMacTableDelete", ev.Kind)
	}
	if ev.MAC != "ba:79:97:73:89:8d" {
		t.Errorf("MAC = %q", ev.MAC)
	}
}

func TestParseSyslogLineWPAComplete(t *testing.T) {
	line := "Sat May  9 09:00:00 2026 kern.warn kernel: [129533.774564] 7981@C15L2,PeerPairMsg4Action() 6999: AP SETKEYS DONE(rax0) - AKMMap=WPA2PSK, PairwiseCipher=AES, GroupCipher=AES, wcid=3 from ba:79:97:73:89:8d"
	ev, ok := parseSyslogLine(line)
	if !ok {
		t.Fatal("应解析成功")
	}
	if ev.Kind != SyslogWPAComplete {
		t.Errorf("Kind = %v, want SyslogWPAComplete", ev.Kind)
	}
	if ev.MAC != "ba:79:97:73:89:8d" {
		t.Errorf("MAC = %q", ev.MAC)
	}
	if ev.Iface != "rax0" {
		t.Errorf("Iface = %q", ev.Iface)
	}
}

func TestParseSyslogLineDHCPAck(t *testing.T) {
	line := "Sat May  9 08:55:45 2026 daemon.info dnsmasq-dhcp[4508]: DHCPACK(br-lan) 192.168.1.213 ba:79:97:73:89:8d"
	ev, ok := parseSyslogLine(line)
	if !ok {
		t.Fatal("应解析成功")
	}
	if ev.Kind != SyslogDHCPAck {
		t.Errorf("Kind = %v, want SyslogDHCPAck", ev.Kind)
	}
	if ev.IP != "192.168.1.213" {
		t.Errorf("IP = %q", ev.IP)
	}
	if ev.MAC != "ba:79:97:73:89:8d" {
		t.Errorf("MAC = %q", ev.MAC)
	}
}

func TestParseSyslogLineWifiDisconnect(t *testing.T) {
	line := "Sat May  9 08:58:49 2026 kern.notice kernel: [129502.112224] 7981@C00L3,wifi_sys_disconn_act() 1023: entry->bSw=0, entry->wcid=3, entry->hw_wcid=3, entry->Addr=ba:79:97:73:89:8d"
	ev, ok := parseSyslogLine(line)
	if !ok {
		t.Fatal("应解析成功")
	}
	if ev.Kind != SyslogWifiDisconnect {
		t.Errorf("Kind = %v, want SyslogWifiDisconnect", ev.Kind)
	}
	if ev.MAC != "ba:79:97:73:89:8d" {
		t.Errorf("MAC = %q", ev.MAC)
	}
}

func TestParseSyslogLineDeauth(t *testing.T) {
	line := "Sat May  9 10:03:10 2026 kern.notice kernel: [133362.228661] 7981@C08L3,ap_peer_deauth_action() 430: AUTH - receive DE-AUTH(seq-1860) from ba:79:97:73:89:8d, reason=8"
	ev, ok := parseSyslogLine(line)
	if !ok {
		t.Fatal("应解析成功")
	}
	if ev.Kind != SyslogDeauth {
		t.Errorf("Kind = %v, want SyslogDeauth", ev.Kind)
	}
	if ev.MAC != "ba:79:97:73:89:8d" {
		t.Errorf("MAC = %q", ev.MAC)
	}
}

func TestParseSyslogLineIrrelevant(t *testing.T) {
	cases := []string{
		"Sat May  9 09:01:46 2026 daemon.info acfrpc[3007]: ws://abber.cn:6400/frp 连接失败",
		"",
		"random garbage without mac",
	}
	for _, line := range cases {
		if _, ok := parseSyslogLine(line); ok {
			t.Errorf("不应解析: %q", line)
		}
	}
}

func TestSyslogKindString(t *testing.T) {
	cases := []struct {
		k    SyslogKind
		want string
	}{
		{SyslogWifiConnect, "WIFI_CONNECT"},
		{SyslogWifiDisconnect, "WIFI_DISCONNECT"},
		{SyslogDeauth, "DEAUTH"},
		{SyslogMacTableDelete, "MACTABLE_DELETE"},
		{SyslogWPAComplete, "WPA_COMPLETE"},
		{SyslogMacTableInsert, "MACTABLE_INSERT"},
		{SyslogDHCPAck, "DHCP_ACK"},
		{SyslogKind(0), "UNKNOWN"},
	}
	for _, c := range cases {
		if got := c.k.String(); got != c.want {
			t.Errorf("%d.String() = %q, want %q", c.k, got, c.want)
		}
	}
}

func TestSyslogKindIsDisconnectIsConnect(t *testing.T) {
	disconn := []SyslogKind{SyslogWifiDisconnect, SyslogDeauth, SyslogMacTableDelete}
	for _, k := range disconn {
		if !k.IsDisconnect() {
			t.Errorf("%v 应为 disconnect", k)
		}
		if k.IsConnect() {
			t.Errorf("%v 不应为 connect", k)
		}
	}
	conn := []SyslogKind{SyslogWPAComplete, SyslogMacTableInsert, SyslogDHCPAck}
	for _, k := range conn {
		if !k.IsConnect() {
			t.Errorf("%v 应为 connect", k)
		}
		if k.IsDisconnect() {
			t.Errorf("%v 不应为 disconnect", k)
		}
	}
}

func TestSyslogKindLabel(t *testing.T) {
	cases := []struct {
		k    SyslogKind
		want string
	}{
		{SyslogWifiConnect, "无线接入"},
		{SyslogWifiDisconnect, "无线断开"},
		{SyslogDeauth, "认证踢出"},
		{SyslogMacTableDelete, "MAC表移除"},
		{SyslogWPAComplete, "认证完成"},
		{SyslogMacTableInsert, "MAC表新增"},
		{SyslogDHCPAck, "DHCP分配"},
		{SyslogKind(0), "未知事件"},
	}
	for _, c := range cases {
		if got := c.k.Label(); got != c.want {
			t.Errorf("%d.Label() = %q, want %q", c.k, got, c.want)
		}
	}
}

func TestParseSyslogLineWifiConnect(t *testing.T) {
	line := "Sat May  9 09:00:00 2026 kern.notice kernel: [129533.644474] 7981@C00L3,wifi_sys_conn_act() 1143: entry->bSw=0, entry->wcid=3, entry->hw_wcid=3, entry->Addr=ba:79:97:73:89:8d"
	ev, ok := parseSyslogLine(line)
	if !ok {
		t.Fatal("应解析成功")
	}
	if ev.Kind != SyslogWifiConnect {
		t.Errorf("Kind = %v, want SyslogWifiConnect", ev.Kind)
	}
}

func TestParseSyslogTimestampNoMatch(t *testing.T) {
	// 不匹配时间前缀时应返回 time.Now (非零)
	ts := parseSyslogTimestamp("no timestamp here")
	if ts.IsZero() {
		t.Error("无匹配应回退到 time.Now(), 不应为零时间")
	}
}

func TestParseSyslogTimestamp(t *testing.T) {
	line := "Sat May  9 08:55:45 2026 kern.warn kernel: test"
	ts := parseSyslogTimestamp(line)
	if ts.Year() != 2026 || ts.Month() != 5 || ts.Day() != 9 {
		t.Errorf("日期不对: %v", ts)
	}
	if ts.Hour() != 8 || ts.Minute() != 55 || ts.Second() != 45 {
		t.Errorf("时间不对: %v", ts)
	}
}
