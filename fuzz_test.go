package argus

import (
	"os"
	"path/filepath"
	"testing"
)

// FuzzParseSyslogLine exercises the syslog parser with arbitrary inputs.
// Syslog is an untrusted-text surface (anything running on the router
// can emit lines via logger(1)); the parser must never panic.
//
// Seed corpus covers the real-world shapes observed on MT7981 routers;
// the fuzzer mutates those plus any discoveries in testdata/fuzz/.
func FuzzParseSyslogLine(f *testing.F) {
	seeds := []string{
		"Sat May  9 08:55:45 2026 kern.warn kernel: [129318.193011] 7981@C13L2,MacTableInsertEntry() 1559: New Sta:ba:79:97:73:89:8d",
		"Sat May  9 08:55:45 2026 kern.warn kernel: [129502.111993] 7981@C13L2,MacTableDeleteEntry() 1921: Del Sta:ba:79:97:73:89:8d",
		"Sat May  9 09:00:00 2026 kern.warn kernel: [129533.774564] 7981@C15L2,PeerPairMsg4Action() 6999: AP SETKEYS DONE(rax0) - from ba:79:97:73:89:8d",
		"Sat May  9 08:55:45 2026 daemon.info dnsmasq-dhcp[4508]: DHCPACK(br-lan) 192.168.1.213 ba:79:97:73:89:8d",
		"Sat May  9 08:58:49 2026 kern.notice kernel: wifi_sys_disconn_act() Addr=ba:79:97:73:89:8d",
		"Sat May  9 10:03:10 2026 kern.notice kernel: ap_peer_deauth_action(): AUTH - receive DE-AUTH(seq-1860) from ba:79:97:73:89:8d, reason=8",
		"",
		"completely unrelated line",
		"Addr=malformed",
		"\x00\x01\x02\xff",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, line string) {
		// The only requirement: must not panic.
		_, _ = parseSyslogLine(line)
	})
}

// FuzzLoadDHCPLeases exercises the /tmp/dhcp.leases parser by writing
// fuzzed content to a tempfile. The file is read via bufio.Scanner so
// the parser must cope with arbitrary whitespace, field counts, and
// encoding.
func FuzzLoadDHCPLeases(f *testing.F) {
	seeds := []string{
		"1778284462 ba:79:97:73:89:8d 192.168.1.213 * 01:ba:79:97:73:89:8d\n",
		"0 b0:fc:36:32:94:61 192.168.1.5 lenovo 01:b0:fc:36:32:94:61\n",
		"short line\n",
		"",
		"\n\n\n",
		"col1 col2 col3",                     // missing IP/host
		"\x00\xff\x01 mac ip host",           // non-UTF8 leading
		"1 AA:BB:CC:DD:EE:FF 10.0.0.1 host\n", // uppercase MAC
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, content string) {
		dir := t.TempDir()
		path := filepath.Join(dir, "leases")
		// ignore write errors — we're testing the parser, not the FS
		_ = os.WriteFile(path, []byte(content), 0644)

		hints := map[string]Hint{}
		loadDHCPLeases(path, hints) // must not panic
	})
}
