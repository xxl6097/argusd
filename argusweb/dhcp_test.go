package argusweb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	argus "github.com/xxl6097/argusd"
	"github.com/xxl6097/argusd/argustest"
)

// --- parser tests ---------------------------------------------------

func TestParseUCIDHCPShow_MultipleHosts(t *testing.T) {
	out := `dhcp.@host[0]=host
dhcp.@host[0].name='pc'
dhcp.@host[0].mac='a0:29:42:00:7a:fd'
dhcp.@host[0].ip='192.168.10.2'
dhcp.@host[0].leasetime='infinite'
dhcp.@host[1]=host
dhcp.@host[1].name='mm'
dhcp.@host[1].mac='52:c6:0a:75:2d:1f'
dhcp.@host[1].ip='192.168.10.4'
dhcp.@host[1].leasetime='infinite'
`
	leases := parseUCIDHCPShow(out)
	if len(leases) != 2 {
		t.Fatalf("got %d leases, want 2: %+v", len(leases), leases)
	}
	pc := leases["a0:29:42:00:7a:fd"]
	if pc.IP != "192.168.10.2" {
		t.Errorf("pc.IP = %q", pc.IP)
	}
	// Name must be prefixed with "#<key>:" so the manager can find the
	// uci section key for updates.
	if !strings.HasPrefix(pc.Name, "#@host[0]:") {
		t.Errorf("pc.Name = %q, want #@host[0]: prefix", pc.Name)
	}
	if !strings.HasPrefix(leases["52:c6:0a:75:2d:1f"].Name, "#@host[1]:") {
		t.Errorf("mm.Name missing key prefix: %q", leases["52:c6:0a:75:2d:1f"].Name)
	}
}

func TestParseUCIDHCPShow_SkipsIncompleteEntries(t *testing.T) {
	// Entry missing an IP should be dropped (defensive: prevent the
	// manager from "finding" a host with an empty IP).
	out := `dhcp.@host[0]=host
dhcp.@host[0].name='orphan'
dhcp.@host[0].mac='aa:bb:cc:dd:ee:ff'
`
	leases := parseUCIDHCPShow(out)
	if len(leases) != 0 {
		t.Errorf("incomplete host should be dropped, got %+v", leases)
	}
}

func TestParseUCIDHCPShow_IgnoresUnrelatedSections(t *testing.T) {
	// Real uci output mixes host entries with dnsmasq config, dhcp
	// pools, etc. Make sure the parser ignores those cleanly.
	out := `dhcp.@dnsmasq[0]=dnsmasq
dhcp.@dnsmasq[0].domainneeded='1'
dhcp.lan=dhcp
dhcp.lan.start='100'
dhcp.@host[0]=host
dhcp.@host[0].mac='aa:bb:cc:dd:ee:ff'
dhcp.@host[0].ip='10.0.0.1'
`
	leases := parseUCIDHCPShow(out)
	if len(leases) != 1 {
		t.Fatalf("got %d, want 1", len(leases))
	}
}

func TestParseUCIDHCPShow_HandlesNamedSections(t *testing.T) {
	// The manager creates named sections like "argus_8e97"; they must
	// round-trip through the parser with the section key captured so
	// findHostSectionLocked can find them again.
	out := `dhcp.argus_8e97=host
dhcp.argus_8e97.name='speaker'
dhcp.argus_8e97.mac='ec:41:18:79:8e:97'
dhcp.argus_8e97.ip='192.168.10.209'
dhcp.argus_8e97.leasetime='infinite'
dhcp.@host[0]=host
dhcp.@host[0].mac='aa:bb:cc:dd:ee:ff'
dhcp.@host[0].ip='10.0.0.1'
`
	leases := parseUCIDHCPShow(out)
	if len(leases) != 2 {
		t.Fatalf("got %d, want 2: %+v", len(leases), leases)
	}
	named := leases["ec:41:18:79:8e:97"]
	if !strings.HasPrefix(named.Name, "#argus_8e97:") {
		t.Errorf("named section missing key prefix: %q", named.Name)
	}
	anon := leases["aa:bb:cc:dd:ee:ff"]
	if !strings.HasPrefix(anon.Name, "#@host[0]:") {
		t.Errorf("anonymous section missing key prefix: %q", anon.Name)
	}
}

// --- validator tests ------------------------------------------------

func TestValidateMAC(t *testing.T) {
	good := []string{"aa:bb:cc:dd:ee:ff", "AA:BB:CC:DD:EE:FF", "00:11:22:33:44:55"}
	for _, s := range good {
		out, err := validateMAC(s)
		if err != nil {
			t.Errorf("MAC %q rejected: %v", s, err)
		}
		if out != strings.ToLower(s) {
			t.Errorf("MAC %q not lowercased: %q", s, out)
		}
	}
	bad := []string{"", "aa:bb:cc:dd:ee", "zz:yy:xx:ww:vv:uu", "aabbccddeeff", "aa:bb:cc:dd:ee:ff:00"}
	for _, s := range bad {
		if _, err := validateMAC(s); err == nil {
			t.Errorf("MAC %q incorrectly accepted", s)
		}
	}
}

func TestValidateIPv4(t *testing.T) {
	good := []string{"0.0.0.0", "192.168.10.50", "255.255.255.255"}
	for _, s := range good {
		if _, err := validateIPv4(s); err != nil {
			t.Errorf("IP %q rejected: %v", s, err)
		}
	}
	bad := []string{"", "256.0.0.0", "not-ip", "2001:db8::1", "192.168.1.1;reboot"}
	for _, s := range bad {
		if _, err := validateIPv4(s); err == nil {
			t.Errorf("IP %q incorrectly accepted", s)
		}
	}
}

func TestValidateName(t *testing.T) {
	good := []string{
		"", "device", "my-phone", "server_01",
		"has space", "uuxia的iPhone", "客厅·音箱", "192.168.x",
		strings.Repeat("a", 63),
	}
	for _, s := range good {
		if _, err := validateName(s); err != nil {
			t.Errorf("name %q rejected: %v", s, err)
		}
	}
	bad := []string{strings.Repeat("a", 64), "shell$(pwd)", "back`tick`", "semi;colon", "pipe|cmd", "and&cmd", "quote'x", "redir>x"}
	for _, s := range bad {
		if _, err := validateName(s); err == nil {
			t.Errorf("name %q incorrectly accepted (would allow injection)", s)
		}
	}
}

// --- HTTP route tests ----------------------------------------------

// stubDHCP is an in-memory DHCPManager used by the HTTP tests so we
// can exercise route plumbing without a real OpenWrt box.
type stubDHCP struct {
	mu       sync.Mutex
	state    map[string]StaticLease
	listErr  error
	setErr   error
	setCalls int
	delCalls int
}

func newStubDHCP() *stubDHCP { return &stubDHCP{state: map[string]StaticLease{}} }

func (s *stubDHCP) List(ctx context.Context) (map[string]StaticLease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listErr != nil {
		return nil, s.listErr
	}
	out := make(map[string]StaticLease, len(s.state))
	for k, v := range s.state {
		out[k] = v
	}
	return out, nil
}

func (s *stubDHCP) Set(ctx context.Context, l StaticLease) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setCalls++
	if s.setErr != nil {
		return s.setErr
	}
	mac, err := validateMAC(l.MAC)
	if err != nil {
		return err
	}
	if _, err := validateIPv4(l.IP); err != nil {
		return err
	}
	// Mirror the real manager's IP-conflict guard so HTTP-level
	// tests of the 409 path don't depend on UCIDHCPManager being
	// instantiable (it isn't, off-OpenWrt).
	for otherMAC, other := range s.state {
		if otherMAC == mac {
			continue
		}
		if other.IP == l.IP {
			return &ErrIPAlreadyReserved{IP: l.IP, OwnerMAC: otherMAC}
		}
	}
	s.state[mac] = StaticLease{MAC: mac, IP: l.IP, Name: l.Name}
	return nil
}

func (s *stubDHCP) Delete(ctx context.Context, mac string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.delCalls++
	mac = strings.ToLower(mac)
	delete(s.state, mac)
	return nil
}

func TestDHCPWithoutManagerReturns503(t *testing.T) {
	w := argus.New(argus.WithFetcher(&argustest.FixedFetcher{}))
	srv := NewServer(w) // no WithDHCPManager
	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/dhcp")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 503 {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

func TestDHCPGetReturnsLeases(t *testing.T) {
	w := argus.New(argus.WithFetcher(&argustest.FixedFetcher{}))
	stub := newStubDHCP()
	stub.state["aa:bb:cc:dd:ee:ff"] = StaticLease{MAC: "aa:bb:cc:dd:ee:ff", IP: "192.168.10.5", Name: "#0:pc"}
	srv := NewServer(w, WithDHCPManager(stub))
	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/dhcp")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Leases map[string]StaticLease `json:"leases"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	l, ok := body.Leases["AA:BB:CC:DD:EE:FF"]
	if !ok {
		t.Fatalf("MAC not upper-cased in response: %+v", body.Leases)
	}
	if l.IP != "192.168.10.5" {
		t.Errorf("IP = %q", l.IP)
	}
	if l.Name != "pc" {
		t.Errorf("internal '#N:' prefix leaked to wire: %q", l.Name)
	}
}

func TestDHCPPostWritesAndReloadsDevices(t *testing.T) {
	w := argus.New(argus.WithFetcher(&argustest.FixedFetcher{}))
	stub := newStubDHCP()
	srv := NewServer(w,
		WithDHCPManager(stub),
		WithWriteAuth(func(r *http.Request) bool { return true }),
	)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	body := []byte(`{"mac":"AA:BB:CC:DD:EE:FF","ip":"192.168.10.50","name":"test"}`)
	resp, err := http.Post(ts.URL+"/api/dhcp", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
	if stub.setCalls != 1 {
		t.Errorf("setCalls = %d, want 1", stub.setCalls)
	}
	if l, ok := stub.state["aa:bb:cc:dd:ee:ff"]; !ok || l.IP != "192.168.10.50" {
		t.Errorf("stub state wrong: %+v", stub.state)
	}
}

func TestDHCPPostRejectsBadIP(t *testing.T) {
	w := argus.New(argus.WithFetcher(&argustest.FixedFetcher{}))
	stub := newStubDHCP()
	srv := NewServer(w,
		WithDHCPManager(stub),
		WithWriteAuth(func(r *http.Request) bool { return true }),
	)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	body := []byte(`{"mac":"AA:BB:CC:DD:EE:FF","ip":"not-an-ip"}`)
	resp, err := http.Post(ts.URL+"/api/dhcp", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestDHCPDeleteRemoves(t *testing.T) {
	w := argus.New(argus.WithFetcher(&argustest.FixedFetcher{}))
	stub := newStubDHCP()
	stub.state["aa:bb:cc:dd:ee:ff"] = StaticLease{MAC: "aa:bb:cc:dd:ee:ff", IP: "1.2.3.4"}
	srv := NewServer(w,
		WithDHCPManager(stub),
		WithWriteAuth(func(r *http.Request) bool { return true }),
	)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	req, _ := http.NewRequest("DELETE", ts.URL+"/api/dhcp?mac=AA:BB:CC:DD:EE:FF", nil)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
	if _, ok := stub.state["aa:bb:cc:dd:ee:ff"]; ok {
		t.Errorf("still in state: %+v", stub.state)
	}
}

func TestDHCPWriteRejectedByAuth(t *testing.T) {
	w := argus.New(argus.WithFetcher(&argustest.FixedFetcher{}))
	stub := newStubDHCP()
	srv := NewServer(w,
		WithDHCPManager(stub),
		WithWriteAuth(func(r *http.Request) bool { return false }),
	)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	body := []byte(`{"mac":"AA:BB:CC:DD:EE:FF","ip":"1.2.3.4"}`)
	resp, err := http.Post(ts.URL+"/api/dhcp", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
	if stub.setCalls != 0 {
		t.Errorf("Set was called despite 403: %d", stub.setCalls)
	}
}

func TestNewUCIDHCPManagerUnavailableOffOpenWrt(t *testing.T) {
	// On a dev machine without uci, NewUCIDHCPManager must cleanly
	// return ErrDHCPManagerUnavailable (wrapped) rather than panic.
	orig := uciBinary
	uciBinary = "argusweb-definitely-not-a-real-binary"
	defer func() { uciBinary = orig }()

	m, err := NewUCIDHCPManager()
	if m != nil {
		t.Errorf("manager should be nil, got %v", m)
	}
	if !errors.Is(err, ErrDHCPManagerUnavailable) {
		t.Errorf("err should unwrap to ErrDHCPManagerUnavailable, got %v", err)
	}
}

func TestDevicesCapabilitiesAdvertisesDHCP(t *testing.T) {
	w := argus.New(argus.WithFetcher(&argustest.FixedFetcher{}))
	srv := NewServer(w, WithDHCPManager(newStubDHCP()))
	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/devices")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Capabilities map[string]bool `json:"capabilities"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Capabilities["dhcp"] {
		t.Errorf("capabilities.dhcp = false, want true: %+v", body.Capabilities)
	}
	if body.Capabilities["aliases"] {
		t.Errorf("capabilities.aliases = true, want false (no alias store attached): %+v", body.Capabilities)
	}
}

// --- pruneLeaseFile tests ---------------------------------------------
// These guard the "immediate effect" behavior shipped in v0.15.2: the
// static-IP POST flow needs to remove a device's stale lease line so
// the daemon reissues the configured IP on the next DHCP packet,
// instead of letting the client keep the old IP until its 12h lease
// runs out.

func TestPruneLeaseFile_RemovesMatchingLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dhcp.leases")
	content := "1778460000 aa:bb:cc:dd:ee:ff 192.168.10.5 phoneA 01:aa:bb:cc:dd:ee:ff\n" +
		"1778460001 11:22:33:44:55:66 192.168.10.6 phoneB 01:11:22:33:44:55:66\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := pruneLeaseFile(path, "aa:bb:cc:dd:ee:ff"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if strings.Contains(string(got), "aa:bb:cc") {
		t.Errorf("matching line still present: %q", got)
	}
	if !strings.Contains(string(got), "11:22:33") {
		t.Errorf("unrelated line dropped: %q", got)
	}
}

func TestPruneLeaseFile_CaseInsensitiveMatch(t *testing.T) {
	// Real dhcp.leases uses lowercase; API callers might send
	// uppercase MACs. The pruner matches either way.
	dir := t.TempDir()
	path := filepath.Join(dir, "leases")
	if err := os.WriteFile(path, []byte("1 aa:bb:cc:dd:ee:ff 10.0.0.1 host\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := pruneLeaseFile(path, "AA:BB:CC:DD:EE:FF"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if strings.Contains(string(got), "aa:bb:cc") {
		t.Errorf("case-insensitive match failed: %q", got)
	}
}

func TestPruneLeaseFile_MissingFileIsNoError(t *testing.T) {
	err := pruneLeaseFile("/nonexistent/path/leases", "aa:bb:cc:dd:ee:ff")
	if err == nil {
		t.Error("expected error for missing file (caller treats as best-effort)")
	}
	// applyDHCPChanges swallows this error; the test documents the raw
	// behavior so we notice if it ever stops returning an error.
}

func TestPruneLeaseFile_NoMatchIsNoOp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "leases")
	original := "1 11:22:33:44:55:66 10.0.0.1 host\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(path)
	time.Sleep(10 * time.Millisecond)

	if err := pruneLeaseFile(path, "aa:bb:cc:dd:ee:ff"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(path)
	// When no line matches, we must not rewrite the file at all (the
	// rename would bump mtime and churn flash on routers).
	if !before.ModTime().Equal(after.ModTime()) {
		t.Errorf("file was rewritten when no match; mtimes before=%v after=%v",
			before.ModTime(), after.ModTime())
	}
}

// --- v0.15.3: IP conflict detection + FNV hash suffix ----------------
//
// These tests enforce the invariants the pre-v0.15.3 code lacked. The
// live router outage that prompted the fix was: two different MACs
// both had a static reservation for 192.168.10.2. odhcpd rejected the
// entire /etc/config/dhcp on next reload, breaking DHCP for every
// device on the LAN. The conflict check in Set now rejects the second
// write with ErrIPAlreadyReserved.

func TestSetRejectsIPConflict(t *testing.T) {
	stub := newStubDHCP()
	stub.state["aa:bb:cc:dd:ee:01"] = StaticLease{
		MAC: "aa:bb:cc:dd:ee:01", IP: "192.168.10.2", Name: "existing",
	}
	err := stub.Set(context.Background(), StaticLease{
		MAC: "ff:ee:dd:cc:bb:02", IP: "192.168.10.2", Name: "colliding",
	})
	var conflict *ErrIPAlreadyReserved
	if !errors.As(err, &conflict) {
		t.Fatalf("expected *ErrIPAlreadyReserved, got %T: %v", err, err)
	}
	if conflict.IP != "192.168.10.2" {
		t.Errorf("IP = %q, want 192.168.10.2", conflict.IP)
	}
	if conflict.OwnerMAC != "aa:bb:cc:dd:ee:01" {
		t.Errorf("OwnerMAC = %q, want aa:bb:cc:dd:ee:01", conflict.OwnerMAC)
	}
	// The conflicting entry must NOT be in state.
	if _, exists := stub.state["ff:ee:dd:cc:bb:02"]; exists {
		t.Error("colliding MAC was written despite conflict")
	}
}

func TestSetAllowsUpdateOfOwnReservation(t *testing.T) {
	// Updating an existing reservation (same MAC) with the same IP
	// is NOT a conflict — it's a no-op update.
	stub := newStubDHCP()
	mac := "aa:bb:cc:dd:ee:01"
	stub.state[mac] = StaticLease{MAC: mac, IP: "192.168.10.2", Name: "old"}
	err := stub.Set(context.Background(), StaticLease{
		MAC: mac, IP: "192.168.10.2", Name: "renamed",
	})
	if err != nil {
		t.Errorf("own-MAC same-IP update should succeed, got %v", err)
	}
	if stub.state[mac].Name != "renamed" {
		t.Errorf("update didn't take effect: %+v", stub.state[mac])
	}
}

func TestDHCPPostReturns409OnConflict(t *testing.T) {
	w := argus.New(argus.WithFetcher(&argustest.FixedFetcher{}))
	stub := newStubDHCP()
	stub.state["aa:bb:cc:dd:ee:01"] = StaticLease{
		MAC: "aa:bb:cc:dd:ee:01", IP: "192.168.10.2",
	}
	srv := NewServer(w,
		WithDHCPManager(stub),
		WithWriteAuth(func(r *http.Request) bool { return true }),
	)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	body := []byte(`{"mac":"FF:EE:DD:CC:BB:02","ip":"192.168.10.2"}`)
	resp, err := http.Post(ts.URL+"/api/dhcp", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
	var r struct {
		Error    string `json:"error"`
		IP       string `json:"ip"`
		OwnerMAC string `json:"owner_mac"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		t.Fatal(err)
	}
	if r.IP != "192.168.10.2" || r.OwnerMAC != "AA:BB:CC:DD:EE:01" {
		t.Errorf("conflict body = %+v", r)
	}
}

func TestMACHashSuffixIsStable(t *testing.T) {
	// Deterministic: same MAC -> same suffix across runs.
	a := macHashSuffix("aa:bb:cc:dd:ee:ff")
	b := macHashSuffix("aa:bb:cc:dd:ee:ff")
	if a != b {
		t.Errorf("macHashSuffix not stable: %q vs %q", a, b)
	}
	if len(a) != 6 {
		t.Errorf("suffix length = %d, want 6", len(a))
	}
}

func TestMACHashSuffixAvoidsLastByteCollision(t *testing.T) {
	// The pre-v0.15.3 code used only the last 2 bytes of the MAC to
	// derive the uci section name, causing aa:bb:cc:dd:ee:97 and
	// ff:ee:dd:cc:bb:97 to collide on argus_ee97. The FNV-32 suffix
	// must disambiguate these.
	a := macHashSuffix("aa:bb:cc:dd:ee:97")
	b := macHashSuffix("ff:ee:dd:cc:bb:97")
	if a == b {
		t.Errorf("suffix collision: both -> %q (regression of the v0.15.3 fix)", a)
	}
}

// --- v0.15.3: ErrIPAlreadyReserved error surface ---------------------

func TestErrIPAlreadyReservedMessage(t *testing.T) {
	e := &ErrIPAlreadyReserved{IP: "10.0.0.5", OwnerMAC: "aa:bb:cc:dd:ee:ff"}
	msg := e.Error()
	if !strings.Contains(msg, "10.0.0.5") {
		t.Errorf("error message missing IP: %q", msg)
	}
	if !strings.Contains(strings.ToUpper(msg), "AA:BB:CC:DD:EE:FF") {
		t.Errorf("error message missing MAC: %q", msg)
	}
}
