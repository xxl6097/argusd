// Package argusweb exposes a built-in HTTP + Server-Sent Events (SSE)
// dashboard on top of an argus.Watcher.
//
// It is intentionally zero-dependency: the rendering is a single
// embedded HTML file with vanilla JS + EventSource; no build step, no
// framework, no external CDN. The handler set is three endpoints:
//
//   - GET /             — embedded dashboard page
//   - GET /api/devices  — JSON snapshot (online devices from Watcher.Known
//     merged with recently-offline devices cached here)
//   - GET /api/events   — SSE stream of Online/Offline/Change events
//
// The package is opt-in (no code in the core library changes); typical
// wiring in argusd:
//
//	srv := argusweb.NewServer(w)
//	http.ListenAndServe("127.0.0.1:9099", srv)
//
// Scope (intentional non-goals for v0.13.0):
//   - No authentication. Bind to 127.0.0.1 or put a reverse proxy in front.
//   - No write API. The dashboard is read-only; Argus's Config is
//     reloaded via SIGHUP on the host process, not via HTTP.
//   - No HTTPS. Terminate TLS at the reverse proxy if needed.
//
// # Chinese · 中文
//
// argusweb 子包提供基于 HTTP + SSE 的只读仪表板, 零依赖, 单文件 HTML,
// 直接嵌入二进制。默认监听 127.0.0.1:9099, 不带鉴权 (如需外网访问请
// 在反向代理层加认证)。
package argusweb

import (
	"context"
	_ "embed"
	"encoding/json"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	argus "github.com/xxl6097/argusd"
)

//go:embed assets/dashboard.html
var dashboardHTML []byte

// Defaults for the offline device cache. Override via Option at
// construction time (see NewServer).
const (
	defaultOfflineRetention = 7 * 24 * time.Hour
	defaultOfflineMax       = 512
)

// Option configures a Server at construction time.
type Option func(*Server)

// WithOfflineRetention sets how long a device remains in the /api/devices
// response after it goes offline. Zero or negative disables retention
// entirely (offline devices drop out of the list immediately on
// EventOffline). Default: 7 days.
func WithOfflineRetention(d time.Duration) Option {
	return func(s *Server) { s.offlineTTL = d }
}

// WithOfflineMax caps the number of offline devices retained in the
// dashboard cache. When the cap is reached, the oldest entry (by offline
// timestamp) is evicted to make room. Zero or negative disables the cap.
// Default: 512.
func WithOfflineMax(n int) Option {
	return func(s *Server) { s.offlineMax = n }
}

// WithAliases attaches a persistent alias store. When set, `/api/devices`
// rows carry an `alias` field (when present) and the dashboard prefers
// the alias over hostname for display. Writes go through
// `POST /api/aliases`, which is gated by the auth predicate (see
// [WithWriteAuth]).
//
// Passing nil is a no-op (equivalent to the default). The store itself
// is safe for concurrent use.
func WithAliases(store *AliasStore) Option {
	return func(s *Server) { s.aliases = store }
}

// AuthCheck decides whether an incoming HTTP request may mutate state
// (currently: the alias write APIs). Return true to allow, false to
// reject with 403. See [WithWriteAuth] for the default policy.
type AuthCheck func(r *http.Request) bool

// WithWriteAuth replaces the default write-API auth predicate. The
// default allows requests whose remote address is loopback or an
// RFC1918 private-network address — appropriate for a Web UI exposed
// on a home LAN via `-listen=0.0.0.0:9099`. For more restrictive
// deployments (reverse proxy with shared-secret header, Basic Auth,
// etc.), provide a custom AuthCheck.
//
// The predicate is NOT applied to read endpoints (`GET /`, `GET
// /api/devices`, `GET /api/events`, `GET /api/aliases`).
func WithWriteAuth(check AuthCheck) Option {
	return func(s *Server) { s.writeAuth = check }
}

// WithDHCPManager attaches a router-specific DHCP manager (typically
// a *UCIDHCPManager produced by NewUCIDHCPManager). When set,
// `/api/dhcp` is enabled and the dashboard surfaces a "set static IP"
// button on every device row. Passing nil is a no-op (equivalent to
// the default — the endpoint returns 503 and the dashboard hides
// the button).
func WithDHCPManager(m DHCPManager) Option {
	return func(s *Server) { s.dhcp = m }
}

// Server is an http.Handler that serves the argus dashboard + API.
// Embed it in your own http.ServeMux or pass it directly to
// http.ListenAndServe.
//
// Server is safe for concurrent use by multiple HTTP clients.
type Server struct {
	watcher *argus.Watcher
	mux     *http.ServeMux

	// SSE fan-out: set of subscriber channels. Each /api/events connection
	// registers a channel on connect and un-registers on disconnect.
	subsMu sync.RWMutex
	subs   map[chan argus.Event]struct{}

	// Offline cache: devices that have gone Offline but we still want to
	// surface in /api/devices. Entries are added by OnEvent on EventOffline
	// and removed on EventOnline for the same MAC; also evicted by TTL
	// (offlineTTL) and soft cap (offlineMax).
	offlineMu  sync.Mutex
	offline    map[string]offlineEntry
	offlineTTL time.Duration
	offlineMax int

	// aliases is an optional user-managed MAC -> friendly-name store.
	// When non-nil, /api/devices rows carry an `alias` field and the
	// dashboard prefers it for display. nil means "no alias feature".
	aliases *AliasStore

	// dhcp is an optional router-specific manager for static DHCP
	// reservations. Exposes /api/dhcp when non-nil; the dashboard
	// hides the "set static IP" UI when nil.
	dhcp DHCPManager

	// writeAuth gates mutating APIs (POST/DELETE /api/aliases). nil
	// means the default LAN policy (loopback + RFC1918).
	writeAuth AuthCheck
}

// offlineEntry stores the last-known Device shape at the moment it went
// offline, plus the time we observed the offline event. LastSeen on the
// Device itself is preserved from the library's point of view (wire
// format reports both as separate fields).
type offlineEntry struct {
	dev       argus.Device
	offlineAt time.Time
}

// NewServer constructs a dashboard server around the given Watcher.
// The Watcher must already be running (or about to run) via its Run
// method elsewhere in the process — Server does NOT call Run for you.
//
// To plumb events into the /api/events SSE stream, wrap your existing
// EventHandler with (*Server).OnEvent before passing it to Watcher.Run:
//
//	srv := argusweb.NewServer(w)
//	w.Run(ctx, srv.OnEvent, nil)
//
// If you already have a business EventHandler, call srv.OnEvent
// alongside it:
//
//	w.Run(ctx, func(e argus.Event) {
//	    srv.OnEvent(e)
//	    myBusinessHandler(e)
//	}, nil)
func NewServer(w *argus.Watcher, opts ...Option) *Server {
	s := &Server{
		watcher:    w,
		mux:        http.NewServeMux(),
		subs:       make(map[chan argus.Event]struct{}),
		offline:    make(map[string]offlineEntry),
		offlineTTL: defaultOfflineRetention,
		offlineMax: defaultOfflineMax,
	}
	for _, opt := range opts {
		opt(s)
	}
	if s.writeAuth == nil {
		s.writeAuth = defaultLANAuth
	}
	s.mux.HandleFunc("/", s.handleIndex)
	s.mux.HandleFunc("/api/devices", s.handleDevices)
	s.mux.HandleFunc("/api/events", s.handleEvents)
	s.mux.HandleFunc("/api/aliases", s.handleAliases)
	s.mux.HandleFunc("/api/dhcp", s.handleDHCP)
	s.mux.HandleFunc("/api/system/reboot", s.handleReboot)
	s.mux.HandleFunc("/api/system/restart-network", s.handleRestartNetwork)
	return s
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// OnEvent fans an Event out to SSE subscribers AND maintains the offline
// cache used by /api/devices. Safe to call from any goroutine.
//
// Offline cache behavior:
//   - EventOffline adds the Device to the cache (keyed by MAC).
//   - EventOnline removes the MAC from the cache (the Watcher's Known()
//     will surface it as online).
//   - EventChange updates the cached entry IF the MAC is currently in
//     the cache (i.e. the device is in its offline retention window
//     and picked up a field change anyway).
//
// SSE fan-out: returns immediately; if a subscriber's channel is full
// the event is dropped for that subscriber only (others unaffected).
// The channel buffer is deliberately small (8) so a slow client does
// not pin memory for the whole server.
func (s *Server) OnEvent(e argus.Event) {
	s.updateOfflineCache(e)

	s.subsMu.RLock()
	defer s.subsMu.RUnlock()
	for ch := range s.subs {
		select {
		case ch <- e:
		default:
			// Slow subscriber — drop. Clients should reconnect if they
			// miss events; the dashboard /api/devices endpoint lets
			// them re-sync on (re)load.
		}
	}
}

func (s *Server) updateOfflineCache(e argus.Event) {
	switch e.Kind {
	case argus.EventOffline:
		s.offlineMu.Lock()
		defer s.offlineMu.Unlock()
		if s.offlineMax > 0 && len(s.offline) >= s.offlineMax {
			// Evict the oldest entry by offlineAt. This is O(n) but n is
			// bounded by offlineMax and only triggers when at capacity,
			// so overall cost is amortized and the map stays bounded.
			var oldestMAC string
			var oldestTime time.Time
			for m, entry := range s.offline {
				if oldestMAC == "" || entry.offlineAt.Before(oldestTime) {
					oldestMAC = m
					oldestTime = entry.offlineAt
				}
			}
			delete(s.offline, oldestMAC)
		}
		s.offline[normalizeMAC(e.Device.MAC)] = offlineEntry{
			dev:       e.Device,
			offlineAt: nonZeroTime(e.Time),
		}

	case argus.EventOnline:
		s.offlineMu.Lock()
		defer s.offlineMu.Unlock()
		delete(s.offline, normalizeMAC(e.Device.MAC))

	case argus.EventChange:
		// Only relevant if the MAC is currently in our offline cache —
		// an offline device that happened to get an enrichment update.
		s.offlineMu.Lock()
		defer s.offlineMu.Unlock()
		mac := normalizeMAC(e.Device.MAC)
		if existing, ok := s.offline[mac]; ok {
			existing.dev = e.Device
			s.offline[mac] = existing
		}
	}
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Conservative cache: the dashboard HTML is embedded in the binary,
	// so a redeploy is required to change it. 5-min cache is fine.
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(dashboardHTML)
}

// deviceRow is the wire format for /api/devices. Fields mirror the
// stable JSON field names in STABILITY.md so consumers can script
// against them.
//
// Status, OfflineAtMs, and Alias are argusweb additions and are
// documented in STABILITY.md under the argusweb subpackage.
type deviceRow struct {
	MAC         string `json:"mac"`
	IP          string `json:"ip,omitempty"`
	Hostname    string `json:"hostname,omitempty"`
	Vendor      string `json:"vendor,omitempty"`
	Type        string `json:"type,omitempty"`
	Radio       string `json:"radio,omitempty"`
	SSID        string `json:"ssid,omitempty"`
	Channel     int    `json:"channel,omitempty"`
	RSSI        int    `json:"rssi,omitempty"`
	Wired       bool   `json:"wired"`
	LastSeenMs  int64  `json:"last_seen_ms,omitempty"`
	Status      string `json:"status"`                  // "online" | "offline" (since v0.13.3)
	OfflineAtMs int64  `json:"offline_at_ms,omitempty"` // set when status=="offline"
	Alias       string `json:"alias,omitempty"`         // user-defined name (since v0.14.0)
}

// applyAlias annotates the row with a user-defined friendly name when
// one is configured. Returns the row unchanged if no alias store is
// attached or the MAC has no entry.
func (s *Server) applyAlias(row deviceRow) deviceRow {
	if s.aliases == nil {
		return row
	}
	if name := s.aliases.Lookup(row.MAC); name != "" {
		row.Alias = name
	}
	return row
}

func (s *Server) handleDevices(w http.ResponseWriter, r *http.Request) {
	known := s.watcher.Known()

	// Prune offline cache: drop entries older than offlineTTL, and any
	// MAC that reappeared in known (defensive — OnEvent should have
	// already removed it, but a lost EventOnline shouldn't pin an
	// incorrect offline row forever).
	s.offlineMu.Lock()
	now := time.Now()
	for mac, entry := range s.offline {
		if s.offlineTTL > 0 && now.Sub(entry.offlineAt) > s.offlineTTL {
			delete(s.offline, mac)
			continue
		}
		if _, onlineNow := known[mac]; onlineNow {
			delete(s.offline, mac)
		}
	}
	// Copy offline entries while holding the lock so we can release it
	// before the JSON encoding.
	offlineSnapshot := make(map[string]offlineEntry, len(s.offline))
	for k, v := range s.offline {
		offlineSnapshot[k] = v
	}
	s.offlineMu.Unlock()

	rows := make([]deviceRow, 0, len(known)+len(offlineSnapshot))
	onlineCount := 0
	offlineCount := 0

	for _, d := range known {
		row := deviceRow{
			MAC:      strings.ToUpper(d.MAC),
			IP:       d.IP,
			Hostname: d.Hostname,
			Vendor:   d.Vendor,
			Type:     d.Type,
			Radio:    d.Radio,
			SSID:     d.SSID,
			Channel:  d.Channel,
			RSSI:     d.RSSI,
			Wired:    d.Wired(),
			Status:   "online",
		}
		if !d.LastSeen.IsZero() {
			row.LastSeenMs = d.LastSeen.UnixMilli()
		}
		rows = append(rows, s.applyAlias(row))
		onlineCount++
	}

	for mac, entry := range offlineSnapshot {
		if _, ok := known[mac]; ok {
			continue // defensive: already online, don't double-count
		}
		d := entry.dev
		row := deviceRow{
			MAC:         strings.ToUpper(d.MAC),
			IP:          d.IP,
			Hostname:    d.Hostname,
			Vendor:      d.Vendor,
			Type:        d.Type,
			Radio:       d.Radio,
			SSID:        d.SSID,
			Channel:     d.Channel,
			RSSI:        d.RSSI,
			Wired:       d.Wired(),
			Status:      "offline",
			OfflineAtMs: entry.offlineAt.UnixMilli(),
		}
		if !d.LastSeen.IsZero() {
			row.LastSeenMs = d.LastSeen.UnixMilli()
		}
		rows = append(rows, s.applyAlias(row))
		offlineCount++
	}

	// Sort: online first, then offline, each alphabetical by MAC. Keeps
	// the active fleet visually on top; offline history trails below.
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Status != rows[j].Status {
			return rows[i].Status == "online"
		}
		return rows[i].MAC < rows[j].MAC
	})

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(map[string]any{
		"devices":      rows,
		"count":        len(rows),
		"online":       onlineCount,
		"offline":      offlineCount,
		"capabilities": s.capabilities(),
	})
}

// capabilities tells the dashboard which features it may expose. The
// fields are part of the Stable wire shape (see STABILITY.md).
func (s *Server) capabilities() map[string]bool {
	return map[string]bool{
		"aliases": s.aliases != nil,
		"dhcp":    s.dhcp != nil,
	}
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE requires a flushable ResponseWriter", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	// Prevent proxy buffering (e.g. nginx needs `proxy_buffering off`).
	w.Header().Set("X-Accel-Buffering", "no")

	ch := make(chan argus.Event, 8)
	s.subsMu.Lock()
	s.subs[ch] = struct{}{}
	s.subsMu.Unlock()
	defer func() {
		s.subsMu.Lock()
		delete(s.subs, ch)
		s.subsMu.Unlock()
	}()

	// Initial hello so clients see the connection is live.
	_, _ = w.Write([]byte("event: hello\ndata: {\"ok\":true}\n\n"))
	flusher.Flush()

	ctx := r.Context()
	enc := json.NewEncoder(w)
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-ch:
			// SSE envelope: `event: <kind>\ndata: <json>\n\n`
			_, _ = w.Write([]byte("event: " + e.Kind.String() + "\ndata: "))
			if err := enc.Encode(e); err != nil {
				return
			}
			_, _ = w.Write([]byte("\n"))
			flusher.Flush()
		}
	}
}

// Shutdown disconnects all SSE subscribers. Callers typically wrap
// Server in an http.Server and call that server's Shutdown; this
// method is exposed so embedders without http.Server can drain
// subscribers explicitly.
func (s *Server) Shutdown(_ context.Context) {
	s.subsMu.Lock()
	defer s.subsMu.Unlock()
	for ch := range s.subs {
		delete(s.subs, ch)
		close(ch)
	}
}

func normalizeMAC(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func nonZeroTime(t time.Time) time.Time {
	if t.IsZero() {
		return time.Now()
	}
	return t
}

// handleAliases multiplexes GET / POST / DELETE on /api/aliases.
//
//	GET    /api/aliases                -> {"aliases": {MAC: name, ...}}
//	POST   /api/aliases  {mac, name}   -> {"ok": true, "mac": MAC, "name": N}
//	                                      empty name deletes the alias
//	DELETE /api/aliases?mac=AA:BB:...  -> {"ok": true}
//
// All paths return JSON. Mutating methods are gated by s.writeAuth.
func (s *Server) handleAliases(w http.ResponseWriter, r *http.Request) {
	if s.aliases == nil {
		http.Error(w, `{"error":"alias store not configured"}`, http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.handleAliasesGet(w, r)
	case http.MethodPost:
		if !s.writeAuth(r) {
			writeJSONErr(w, http.StatusForbidden, "write denied by auth policy")
			return
		}
		s.handleAliasesSet(w, r)
	case http.MethodDelete:
		if !s.writeAuth(r) {
			writeJSONErr(w, http.StatusForbidden, "write denied by auth policy")
			return
		}
		s.handleAliasesDelete(w, r)
	default:
		w.Header().Set("Allow", "GET, POST, DELETE")
		writeJSONErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleAliasesGet(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(map[string]any{"aliases": s.aliases.All()})
}

func (s *Server) handleAliasesSet(w http.ResponseWriter, r *http.Request) {
	var in struct {
		MAC  string `json:"mac"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if err := s.aliases.Set(in.MAC, in.Name); err != nil {
		writeJSONErr(w, http.StatusBadRequest, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":   true,
		"mac":  strings.ToUpper(normalizeMAC(in.MAC)),
		"name": strings.TrimSpace(in.Name),
	})
}

func (s *Server) handleAliasesDelete(w http.ResponseWriter, r *http.Request) {
	mac := r.URL.Query().Get("mac")
	if mac == "" {
		writeJSONErr(w, http.StatusBadRequest, "mac query parameter required")
		return
	}
	if err := s.aliases.Set(mac, ""); err != nil {
		writeJSONErr(w, http.StatusBadRequest, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func writeJSONErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// defaultLANAuth is the default predicate used when the user doesn't
// override WithWriteAuth. It allows requests whose remote address is
// loopback or an RFC1918 private network — appropriate for a dashboard
// bound to a home LAN. X-Forwarded-For is NOT consulted: if you front
// Argus with a reverse proxy, supply your own AuthCheck.
func defaultLANAuth(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	return isRFC1918(ip)
}

func isRFC1918(ip net.IP) bool {
	for _, cidr := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"} {
		_, subnet, _ := net.ParseCIDR(cidr)
		if subnet.Contains(ip) {
			return true
		}
	}
	return false
}
