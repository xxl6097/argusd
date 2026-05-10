// Package argusweb exposes a built-in HTTP + Server-Sent Events (SSE)
// dashboard on top of an argus.Watcher.
//
// It is intentionally zero-dependency: the rendering is a single
// embedded HTML file with vanilla JS + EventSource; no build step, no
// framework, no external CDN. The handler set is three endpoints:
//
//   - GET /             — embedded dashboard page
//   - GET /api/devices  — JSON snapshot of the current Known() set
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
	"net/http"
	"sort"
	"strings"
	"sync"

	argus "github.com/xxl6097/argus"
)

//go:embed assets/dashboard.html
var dashboardHTML []byte

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
func NewServer(w *argus.Watcher) *Server {
	s := &Server{
		watcher: w,
		mux:     http.NewServeMux(),
		subs:    make(map[chan argus.Event]struct{}),
	}
	s.mux.HandleFunc("/", s.handleIndex)
	s.mux.HandleFunc("/api/devices", s.handleDevices)
	s.mux.HandleFunc("/api/events", s.handleEvents)
	return s
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// OnEvent is a DecisionHandler-style entry point that fans an Event
// out to all currently connected SSE subscribers. Safe to call from
// any goroutine.
//
// Returns immediately; if a subscriber's channel is full the event is
// dropped for that subscriber only (other subscribers unaffected). The
// channel buffer is deliberately small (8) so a slow client does not
// pin memory for the whole server.
func (s *Server) OnEvent(e argus.Event) {
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
type deviceRow struct {
	MAC        string `json:"mac"`
	IP         string `json:"ip,omitempty"`
	Hostname   string `json:"hostname,omitempty"`
	Vendor     string `json:"vendor,omitempty"`
	Type       string `json:"type,omitempty"`
	Radio      string `json:"radio,omitempty"`
	SSID       string `json:"ssid,omitempty"`
	Channel    int    `json:"channel,omitempty"`
	RSSI       int    `json:"rssi,omitempty"`
	Wired      bool   `json:"wired"`
	LastSeenMs int64  `json:"last_seen_ms,omitempty"`
}

func (s *Server) handleDevices(w http.ResponseWriter, r *http.Request) {
	known := s.watcher.Known()
	rows := make([]deviceRow, 0, len(known))
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
		}
		if !d.LastSeen.IsZero() {
			row.LastSeenMs = d.LastSeen.UnixMilli()
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].MAC < rows[j].MAC })

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(map[string]any{
		"devices": rows,
		"count":   len(rows),
	})
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
