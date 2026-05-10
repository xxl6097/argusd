// DHCPManager abstracts router-specific static-lease (DHCP reservation)
// operations so the Web UI can offer a "set static IP" affordance
// without the core library having a hard dependency on OpenWrt /
// dnsmasq / uci. See the UCIDHCPManager implementation for the
// reference OpenWrt binding.
//
// A DHCPManager is considered available when NewUCIDHCPManager finds
// the uci binary in PATH and the current process can read
// /etc/config/dhcp. If you're not on OpenWrt, leave the Server
// without a manager — the /api/dhcp endpoints will return 503 and
// the dashboard will hide the static-IP button.

package argusweb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// DHCPManager is the Web-UI-facing interface for static-lease
// operations. All methods are safe for concurrent use.
type DHCPManager interface {
	// List returns every current static reservation, keyed by
	// lowercase MAC. Implementations should normalize MAC casing.
	List(ctx context.Context) (map[string]StaticLease, error)

	// Set creates or updates a reservation. An existing entry for
	// the MAC is replaced in-place. Implementations must validate
	// inputs defensively (MAC and IP syntax, IP-within-subnet when
	// applicable) before mutating state.
	Set(ctx context.Context, l StaticLease) error

	// Delete removes the reservation for a MAC. No-op when the MAC
	// has no reservation; returns nil in that case.
	Delete(ctx context.Context, mac string) error
}

// StaticLease describes a single DHCP reservation as the dashboard
// sees it. Name is optional; when empty the implementation may
// synthesize one (e.g. "argus-<mac-suffix>").
type StaticLease struct {
	MAC  string `json:"mac"`
	IP   string `json:"ip"`
	Name string `json:"name,omitempty"`
}

// ErrDHCPManagerUnavailable is returned by NewUCIDHCPManager when the
// host doesn't look like an OpenWrt system (uci missing or
// /etc/config/dhcp unreadable). The value surfaces via errors.Is so
// callers can fall back cleanly.
var ErrDHCPManagerUnavailable = errors.New("argusweb: no DHCP manager available on this host")

// uciBinary is overridable for tests; argusd always uses the default.
var uciBinary = "uci"

// dnsmasqReloadCmd is the command argusweb runs after uci commit to
// make reservations take effect. Overridable for tests.
var dnsmasqReloadCmd = []string{"/etc/init.d/dnsmasq", "reload"}

// UCIDHCPManager writes static DHCP reservations to OpenWrt's uci-
// backed /etc/config/dhcp and reloads dnsmasq. Constructed via
// NewUCIDHCPManager (which refuses to build on non-OpenWrt hosts).
//
// Thread model: all uci + dnsmasq calls are serialized through an
// internal mutex. Concurrent Set/Delete calls from multiple HTTP
// requests are therefore strictly ordered, which matches dnsmasq's
// expectations anyway.
type UCIDHCPManager struct {
	mu sync.Mutex
}

// NewUCIDHCPManager returns a manager for the local OpenWrt host, or
// ErrDHCPManagerUnavailable wrapped with the detection failure cause
// on non-OpenWrt systems.
func NewUCIDHCPManager() (*UCIDHCPManager, error) {
	if _, err := exec.LookPath(uciBinary); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDHCPManagerUnavailable, err)
	}
	// Probe /etc/config/dhcp by attempting a no-op 'uci show'.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := runUCI(ctx, "show", "dhcp"); err != nil {
		return nil, fmt.Errorf("%w: uci show dhcp: %v", ErrDHCPManagerUnavailable, err)
	}
	return &UCIDHCPManager{}, nil
}

// --- Input validation (defense against shell / uci injection) ----------

var (
	reMAC  = regexp.MustCompile(`^[0-9a-fA-F]{2}(:[0-9a-fA-F]{2}){5}$`)
	reName = regexp.MustCompile(`^[A-Za-z0-9_-]{0,63}$`)
)

func validateMAC(mac string) (string, error) {
	mac = strings.TrimSpace(mac)
	if !reMAC.MatchString(mac) {
		return "", fmt.Errorf("invalid MAC %q (want aa:bb:cc:dd:ee:ff)", mac)
	}
	return strings.ToLower(mac), nil
}

func validateIPv4(s string) (string, error) {
	s = strings.TrimSpace(s)
	ip := net.ParseIP(s)
	if ip == nil || ip.To4() == nil {
		return "", fmt.Errorf("invalid IPv4 %q", s)
	}
	return ip.String(), nil
}

func validateName(n string) (string, error) {
	n = strings.TrimSpace(n)
	if !reName.MatchString(n) {
		return "", fmt.Errorf("invalid name %q (allowed: A-Z a-z 0-9 _ -, up to 63 chars)", n)
	}
	return n, nil
}

// --- uci wrappers ------------------------------------------------------

func runUCI(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, uciBinary, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("uci %s: %w: %s",
			strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// List implements DHCPManager.
func (m *UCIDHCPManager) List(ctx context.Context) (map[string]StaticLease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out, err := runUCI(ctx, "-q", "show", "dhcp")
	if err != nil {
		return nil, err
	}
	return parseUCIDHCPShow(out), nil
}

// Set implements DHCPManager.
func (m *UCIDHCPManager) Set(ctx context.Context, l StaticLease) error {
	mac, err := validateMAC(l.MAC)
	if err != nil {
		return err
	}
	ip, err := validateIPv4(l.IP)
	if err != nil {
		return err
	}
	name, err := validateName(l.Name)
	if err != nil {
		return err
	}
	if name == "" {
		name = "argus-" + strings.ReplaceAll(mac[len(mac)-5:], ":", "")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Always revert any stale pending state on the DHCP package before
	// we start, and ensure we revert if anything goes wrong after we've
	// begun mutating. Without this, a prior failed Set can leave
	// "dhcp.@host[N]=host" floating in the pending changeset, which
	// breaks index arithmetic on the next attempt.
	_, _ = runUCI(ctx, "revert", "dhcp")
	committed := false
	defer func() {
		if !committed {
			_, _ = runUCI(ctx, "revert", "dhcp")
		}
	}()

	idx, err := m.findHostSectionLocked(ctx, mac)
	if err != nil {
		return err
	}
	// uci addresses differ for anonymous vs named sections:
	//   anonymous: dhcp.@host[N].field=...
	//   named:     dhcp.name.field=...
	// We preserve the anonymous form when updating an existing entry
	// that was already anonymous (typically created by LuCI);
	// otherwise we create/update a named "argus_<suffix>" section so
	// the index doesn't shift when other entries are added/removed.
	var sectionRef string
	if idx == "" {
		sectionName := "argus_" + strings.ReplaceAll(mac[len(mac)-5:], ":", "")
		if _, err := runUCI(ctx, "set", "dhcp."+sectionName+"=host"); err != nil {
			return err
		}
		sectionRef = "dhcp." + sectionName
	} else if strings.HasPrefix(idx, "@host[") {
		sectionRef = "dhcp." + idx
	} else {
		sectionRef = "dhcp." + idx
	}
	for _, kv := range [][]string{
		{sectionRef + ".name=" + name},
		{sectionRef + ".mac=" + mac},
		{sectionRef + ".ip=" + ip},
		{sectionRef + ".leasetime=infinite"},
	} {
		if _, err := runUCI(ctx, append([]string{"set"}, kv...)...); err != nil {
			return err
		}
	}
	if _, err := runUCI(ctx, "commit", "dhcp"); err != nil {
		return err
	}
	committed = true
	return reloadDnsmasq(ctx)
}

// Delete implements DHCPManager.
func (m *UCIDHCPManager) Delete(ctx context.Context, mac string) error {
	normalized, err := validateMAC(mac)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	_, _ = runUCI(ctx, "revert", "dhcp")
	committed := false
	defer func() {
		if !committed {
			_, _ = runUCI(ctx, "revert", "dhcp")
		}
	}()

	idx, err := m.findHostSectionLocked(ctx, normalized)
	if err != nil {
		return err
	}
	if idx == "" {
		committed = true // nothing to commit; avoid the defer's revert
		return nil       // already absent
	}
	if _, err := runUCI(ctx, "delete", "dhcp."+idx); err != nil {
		return err
	}
	if _, err := runUCI(ctx, "commit", "dhcp"); err != nil {
		return err
	}
	committed = true
	return reloadDnsmasq(ctx)
}

// findHostSectionLocked returns the uci section key (e.g. "@host[2]"
// or "argus_8e97") for a MAC, or "" if no existing reservation has
// that MAC. Caller must hold m.mu.
func (m *UCIDHCPManager) findHostSectionLocked(ctx context.Context, mac string) (string, error) {
	out, err := runUCI(ctx, "-q", "show", "dhcp")
	if err != nil {
		return "", err
	}
	leases := parseUCIDHCPShow(out)
	for _, l := range leases {
		if l.MAC == mac {
			// Name is "#<key>:<original>"; extract the key.
			if !strings.HasPrefix(l.Name, "#") {
				return "", fmt.Errorf("could not extract section key from %q", l.Name)
			}
			rest := l.Name[1:]
			colon := strings.Index(rest, ":")
			if colon < 0 {
				return "", fmt.Errorf("malformed section-key prefix %q", l.Name)
			}
			return rest[:colon], nil
		}
	}
	return "", nil
}

// parseUCIDHCPShow extracts static leases from 'uci show dhcp' output.
// Each lease's Name is prefixed with "#<key>:" so callers can find the
// underlying uci section by that key without re-running uci. The key
// is either an index ("0", "1", ...) for anonymous @host[N] entries
// or a named section ("argus_8e97") for entries created by us.
//
// Exported solely for tests inside this package; not a stability
// guarantee.
func parseUCIDHCPShow(out string) map[string]StaticLease {
	// Output looks like either:
	//   dhcp.@host[2]=host
	//   dhcp.@host[2].name='xiaomi-pc'
	//   dhcp.@host[2].mac='a0:29:42:00:7a:fd'
	//   dhcp.@host[2].ip='192.168.10.2'
	// OR (named sections we create):
	//   dhcp.argus_8e97=host
	//   dhcp.argus_8e97.name='xiaomi_speaker'
	//   dhcp.argus_8e97.mac='ec:41:18:79:8e:97'
	//   dhcp.argus_8e97.ip='192.168.10.209'
	reAnon  := regexp.MustCompile(`^dhcp\.@host\[(\d+)\]\.(name|mac|ip)=(.*)$`)
	reNamed := regexp.MustCompile(`^dhcp\.([A-Za-z_][A-Za-z0-9_]*)\.(name|mac|ip)=(.*)$`)
	// Also track which sections are declared as =host so we don't
	// misinterpret other named sections (dhcp.lan=dhcp, etc.).
	reSection := regexp.MustCompile(`^dhcp\.([A-Za-z_][A-Za-z0-9_]*|\@host\[\d+\])=host$`)

	isHost := map[string]bool{}
	partial := map[string]*StaticLease{} // key = normalized section ident
	setField := func(key, field, val string) {
		val = strings.Trim(val, "'\"")
		l, ok := partial[key]
		if !ok {
			l = &StaticLease{}
			partial[key] = l
		}
		switch field {
		case "name":
			l.Name = val
		case "mac":
			l.MAC = strings.ToLower(val)
		case "ip":
			l.IP = val
		}
	}

	for _, line := range strings.Split(out, "\n") {
		if m := reSection.FindStringSubmatch(line); m != nil {
			isHost[m[1]] = true
			continue
		}
		if m := reAnon.FindStringSubmatch(line); m != nil {
			key := "@host[" + m[1] + "]"
			setField(key, m[2], m[3])
			continue
		}
		if m := reNamed.FindStringSubmatch(line); m != nil {
			// Skip keys we know belong to non-host sections.
			setField(m[1], m[2], m[3])
		}
	}
	result := make(map[string]StaticLease, len(partial))
	for key, l := range partial {
		if !isHost[key] {
			continue // drop non-host entries (dhcp.lan.*, dhcp.@dnsmasq[0].*)
		}
		if l.MAC == "" || l.IP == "" {
			continue
		}
		// Prefix name with "#<key>:" so findHostIndexLocked can recover it.
		display := l.Name
		l.Name = "#" + key + ":" + display
		result[l.MAC] = *l
	}
	return result
}

func reloadDnsmasq(ctx context.Context) error {
	if len(dnsmasqReloadCmd) == 0 {
		return nil
	}
	cmd := exec.CommandContext(ctx, dnsmasqReloadCmd[0], dnsmasqReloadCmd[1:]...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", strings.Join(dnsmasqReloadCmd, " "),
			err, strings.TrimSpace(string(out)))
	}
	return nil
}

// --- HTTP handlers (wired from Server.handleDHCP) ----------------------

func (s *Server) handleDHCP(w http.ResponseWriter, r *http.Request) {
	if s.dhcp == nil {
		http.Error(w, `{"error":"dhcp manager not configured"}`, http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.handleDHCPGet(w, r)
	case http.MethodPost:
		if !s.writeAuth(r) {
			writeJSONErr(w, http.StatusForbidden, "write denied by auth policy")
			return
		}
		s.handleDHCPSet(w, r)
	case http.MethodDelete:
		if !s.writeAuth(r) {
			writeJSONErr(w, http.StatusForbidden, "write denied by auth policy")
			return
		}
		s.handleDHCPDelete(w, r)
	default:
		w.Header().Set("Allow", "GET, POST, DELETE")
		writeJSONErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleDHCPGet(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	leases, err := s.dhcp.List(ctx)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Strip the internal "#<idx>:" prefix before sending to clients.
	clean := make(map[string]StaticLease, len(leases))
	for mac, l := range leases {
		l.MAC = strings.ToUpper(mac)
		if i := strings.Index(l.Name, ":"); i >= 0 && strings.HasPrefix(l.Name, "#") {
			l.Name = l.Name[i+1:]
		}
		clean[strings.ToUpper(mac)] = l
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(map[string]any{"leases": clean})
}

func (s *Server) handleDHCPSet(w http.ResponseWriter, r *http.Request) {
	var in StaticLease
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := s.dhcp.Set(ctx, in); err != nil {
		writeJSONErr(w, http.StatusBadRequest, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":  true,
		"mac": strings.ToUpper(in.MAC),
		"ip":  in.IP,
	})
}

func (s *Server) handleDHCPDelete(w http.ResponseWriter, r *http.Request) {
	mac := r.URL.Query().Get("mac")
	if mac == "" {
		writeJSONErr(w, http.StatusBadRequest, "mac query parameter required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := s.dhcp.Delete(ctx, mac); err != nil {
		writeJSONErr(w, http.StatusBadRequest, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}
