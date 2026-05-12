# Argus

[中文文档 →](./README_zh.md)

> **Real-time OpenWrt device presence & static-IP dashboard — multi-source fusion, sub-second events, zero-dep Web UI**

[![Go Reference](https://pkg.go.dev/badge/github.com/xxl6097/argusd.svg)](https://pkg.go.dev/github.com/xxl6097/argusd)
[![Go Report Card](https://goreportcard.com/badge/github.com/xxl6097/argusd)](https://goreportcard.com/report/github.com/xxl6097/argusd)
[![Go version](https://img.shields.io/github/go-mod/go-version/xxl6097/argusd)](go.mod)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Tests](https://img.shields.io/badge/tests-passing-brightgreen)](.)
[![Release](https://img.shields.io/github/v/release/xxl6097/argusd?sort=semver)](https://github.com/xxl6097/argusd/releases)

![Dashboard](./docs/images/dashboard-desktop.png)

Argus is a Go library + CLI for **real-time WiFi/wired device presence** on OpenWrt routers. It fuses six data sources (ahsapd · hostapd · `logread` · DHCP leases · ARP · ICMP) into a single sub-second event stream — `Online` / `Offline` / `Change` — and ships an **opt-in Web UI** with static-IP reservations, device aliases, and one-click recovery tools. Zero-dep, works on stock OpenWrt + MediaTek vendor firmwares (C-Life and similar). Named after the hundred-eyed giant of Greek myth — whose eyes never all slept.

**Quick start**:

```bash
# Releases page: https://github.com/xxl6097/argusd/releases
scp argusd root@192.168.1.1:/tmp/ && ssh root@192.168.1.1 \
  '/tmp/argusd -listen :8080 -aliases /etc/argusd/aliases.json'
# Open http://<router-ip>:8080/
```

---

## Table of Contents

1. [Features](#features)
2. [Quick Start](#quick-start)
3. [Web UI · built-in dashboard](#web-ui--built-in-dashboard-v0130)
4. [Architecture](#architecture)
5. [API Overview](#api-overview)
6. [Configuration](#configuration)
7. [Observability](#observability)
8. [Roadmap](#roadmap)
9. [Compatibility](#compatibility)
10. [Contributing](#contributing)

---

## Features

- 🔀 **Multi-source fusion** — ahsapd + hostapd + `logread -f` + DHCP leases + ARP states + ICMP probe, all merged into one stream.
- 🏭 **Vendor-agnostic zero-config** — auto-detects `ahsapd` (vendor firmware) or `hostapd.*` (stock OpenWrt) at startup.
- ⚡ **Sub-second events** — kernel log streaming (`New Sta`, `Del Sta`, `Deauth`, `DHCPACK`…) delivers online/offline in 1–2 s.
- 🛡️ **Multi-dimensional offline detection** — three-layer decision: ICMP ping filter + AP association table with RSSI tiers + ARP `FAILED/INCOMPLETE` shortcut.
- 🌊 **Flap suppression** — 90 s cooldown plus 30 s same-kind suppression window eliminates weak-signal thrashing. Both independently toggleable via `Config.DisableCooldown` / `DisableFlapSuppression`.
- 🧩 **Pure stdlib, single static binary** — ~2.6 MB static binary (`CGO_ENABLED=0`, GOARCH=arm64). Drop into `/tmp` and run.
- 🔬 **Observability** — four hook surfaces, all opt-in and zero-cost when unused: `DecisionHandler` (1.7 ns/op, 0 allocs) surfaces 17 internal branch decisions; `WithLogger` emits structured logs (slog/zap/zerolog adapter in ~5 lines); `WithSpanRecorder` emits distributed-tracing spans (OTel adapter ~15 lines); the `argusmetrics` subpackage ships zero-dependency counters (`Counters` / `LabeledCounters`) ready to bridge to Prometheus / OTLP.
- 🔒 **Security hardened** — IP regex + `net.ParseIP` double validation, interface whitelist — no command injection.
- 🧵 **Concurrency-safe** — `sync.Mutex` protects shared state; events emitted outside the lock; `go test -race` clean across 60+ tests and 9 lifecycle tests.
- 🛟 **Panic-safe callbacks** — user callbacks (`EventHandler` / `ErrorHandler` / `DecisionHandler`) are wrapped with `defer recover`. An `EventHandler` panic is reported via `onError` and does not kill any Watcher goroutine.
- 🔄 **Hot-reload lifecycle (v0.5.0+)** — `Watcher.Stop(ctx)` + re-run preserves `known` / cooldown / flap state across config reload (SIGHUP pattern). Real-router validated: 10 restarts on MT7981 show zero goroutine leak (Threads: 15 → 15). See [`docs/SIGHUP-real-device-test.md`](./docs/SIGHUP-real-device-test.md).
- 🎯 **Sentinel errors + structured validation** — `ErrHandlerRequired` / `ErrInvalidConfig` / `ErrNoFetcher` / `ErrFetchFailed` / `ErrAlreadyRunning`, all `errors.Is`-compatible. `Config.Validate` returns `*ConfigError` with field-level detail reachable via `errors.As` — ideal for web config UIs.

---

## Quick Start

### Use as a library

```go
import (
    "context"
    "fmt"
    "log"
    "os/signal"
    "syscall"

    argus "github.com/xxl6097/argusd"
)

func main() {
    ctx, stop := signal.NotifyContext(context.Background(),
        syscall.SIGINT, syscall.SIGTERM)
    defer stop()

    w := argus.New(
        argus.OnFetcherDetected(func(k argus.FetcherKind) {
            log.Printf("data source: %s", k)
        }),
    )

    err := w.Run(ctx, func(e argus.Event) {
        switch e.Kind {
        case argus.EventOnline:
            fmt.Printf("[+] %s joined %s\n", e.Device.MAC, e.Device.IP)
        case argus.EventOffline:
            fmt.Printf("[-] %s left\n", e.Device.MAC)
        case argus.EventChange:
            for _, c := range e.Changes {
                fmt.Printf("[~] %s %s: %q → %q\n",
                    e.Device.MAC, c.Field, c.Old, c.New)
            }
        }
    }, nil)
    if err != nil {
        log.Fatal(err)
    }
}
```

### Use as a CLI

Prebuilt binaries for common OpenWrt CPU architectures are published on the [Releases page](https://github.com/xxl6097/argusd/releases) (amd64 / arm64 / armv5 / armv7 / mips / mipsle / mips64 / mips64le / riscv64 / 386, all static).

```bash
# Download the matching archive, verify, and deploy.
VER=v1.0.1
TARGET=linux-mipsle-softfloat   # replace with your arch
curl -LO "https://github.com/xxl6097/argusd/releases/download/${VER}/argusd_${VER}_${TARGET}.tar.gz"
curl -LO "https://github.com/xxl6097/argusd/releases/download/${VER}/SHA256SUMS"
sha256sum -c SHA256SUMS --ignore-missing
tar -xzf argusd_${VER}_${TARGET}.tar.gz
scp argusd_${VER}_${TARGET}/argusd root@192.168.1.1:/tmp/argusd
ssh root@192.168.1.1 '/tmp/argusd'
```

Or build from source:

```bash
# Cross-compile for OpenWrt (aarch64 example).
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
    go build -trimpath -ldflags="-s -w" \
    -o argusd ./cmd/argusd

scp argusd root@192.168.1.1:/tmp/
ssh root@192.168.1.1 '/tmp/argusd'
```

Sample output:

```
2026/05/09 18:40:21 data source: ahsapd
MAC                  IP              Hostname         Vendor   Type    Signal        Link
──────────────────────────────────────────────────────────────────────────────────────────
2C:CF:67:1D:27:AC    192.168.1.11    raspberrypi      rasp..   PC      -             wired
B0:FC:36:32:94:61    192.168.1.5     lenovo           DESK..   Phone   -38(strong)   5G/avgb-5G
BA:79:97:73:89:8D    192.168.1.213   BA799773898D     -        Phone   -44(strong)   5G/avgb-5G
──────────────────────────────────────────────────────────────────────────────────────────
4 devices online (WiFi: 3, Wired: 1)

[2026-05-09 18:42:03] [syslog] WIFI_CONNECT  BA:79:...
[2026-05-09 18:42:03] [syslog] DHCP_ACK      BA:79:... IP=192.168.1.213
[2026-05-09 18:42:03] [event]  ONLINE        BA:79:... 192.168.1.213 iPhone -44(strong) 5G/avgb-5G
```

---

## Web UI · built-in dashboard (v0.13.0+)

Argus ships an opt-in, zero-dependency HTTP + Server-Sent-Events dashboard in the `argusweb` subpackage. Single embedded HTML file, vanilla JS, mobile-responsive. Pass `-listen :8080` to `argusd`, or wire `argusweb.NewServer` into your own `http.Handler` tree.

### Screens

**Desktop main view** — device table on the left (seven columns: status / MAC / IP / hostname / vendor / signal / link-type), SSE-driven live event stream on the right, and system buttons (restart-network / reboot) in the top-right corner.

![Desktop](./docs/images/dashboard-desktop.png)

**Static-IP modal** — opens via the 📌 pin button. Enter an IP and optional name; tick "take effect now (restart WiFi)" to run `wifi reload` / `ahsapd restart` so every client reconnects within ~3–5 seconds and the new IP applies immediately. If unticked, the server only writes the UCI config and tries a per-station kick — suitable for firmwares where single-station disconnect actually works.

![Static IP modal](./docs/images/dashboard-static-ip.png)

**Alias rename** — click ✎ to inline-rename; UTF-8 (Chinese / spaces / dots / dashes) is accepted; an empty string clears the alias. Persisted to `aliases.json` with atomic writes.

![Rename](./docs/images/dashboard-rename.png)

**IP-conflict one-click replace** — when the target IP is already bound to a different MAC, the server returns `409 Conflict`. The frontend prompts with the owner MAC; clicking "OK" auto-runs `DELETE /api/dhcp?mac=<owner>` then retries the POST. "Cancel" leaves both reservations unchanged.

![IP conflict](./docs/images/dashboard-ip-conflict.png)

**Mobile (viewport ≤ 640px)** — the table switches to a card layout: MAC / status badge / hostname / vendor / link / signal stacked vertically, one card per device.

![Mobile](./docs/images/dashborad-mobile.png)

### Features

| Feature | Description | Since |
|---|---|---|
| Live device table | SSE-driven; MAC / IP / hostname / vendor / type / signal / link-type / status columns | v0.13.0 |
| Online/Offline column | Offline rows retained per `WithOfflineRetention` (default 7d / 512 entries); shown with relative time such as "2m ago" | v0.13.3 |
| Mobile responsive | Card layout below 640 px breakpoint | v0.13.1 |
| Adaptive columns | `table-layout:auto` with per-column min-widths; columns expand to full content when the screen is wide, truncate with ellipsis + hover tooltip only when cramped | v0.15.5 |
| Reconnect coalescing | OFFLINE→ONLINE bursts within 10 s collapse into one RECONNECT row | v0.13.2 |
| Vendor column | OUI lookup, ellipsis + tooltip for long names | v0.15.0 |
| Aliases (renamable) | ✎ inline-rename button, UTF-8 names (Chinese / spaces / dots accepted), file-backed JSON, atomic writes | v0.14.0 / v0.15.4 |
| Static DHCP reservations | 📌 button → modal; UCI-backed; optional immediate-apply (reload + lease prune + ARP flush + station kick) | v0.15.0 / v0.15.2 / v0.15.7 |
| IP conflict guard | 409 Conflict if the target IP is already bound to a different MAC; UI offers a 1-click "replace" (delete old, retry) | v0.15.3 / v0.15.5 |
| Recovery endpoint | `POST /api/dhcp?purge_argus=1` removes every `dhcp.argus_*` section | v0.15.3 |
| Opt-in WiFi restart | Save-dialog checkbox runs `wifi reload` / `ahsapd restart` so every client re-associates within seconds — nuclear option for firmwares where per-station kick is a no-op | v0.15.8 |
| System actions | Header buttons: "restart network" (soft, 5–15 s LAN blip, config preserved) and "reboot router" (hard, 30–60 s full reboot), each with confirmation prompts | v0.15.9 |
| Write auth | `WithWriteAuth(predicate)` gates every POST/DELETE (aliases / dhcp / system); default allows loopback + RFC1918 | v0.14.0 |

### UI details

- **Status badges** — connection state (`connected` / `reconnecting…`) + online/offline counters stay in the top-right.
- **🔒 icon** — devices with a static reservation show a lock before the IP; hover reveals "static reservation active".
- **📌 button** — opens the static-IP modal. If the MAC already has a reservation, a red "Remove" button appears in the modal footer.
- **✎ button** — opens an inline rename form; Enter saves, Esc cancels, empty string clears the alias.
- **Event badge colors** — `ONLINE` / `RECONNECT` are green, `OFFLINE` / `FLAP` red, `CHANGE` amber.
- **Long-text hover** — any cell truncated by ellipsis shows full content on hover.
- **Toast feedback** — saving a static IP surfaces a multi-line status toast: `reloaded` / `old lease pruned` / `ARP cache flushed` / `station kicked` / `WiFi restarted`, so you know exactly what the server did.
- **Offline devices stay manageable** — offline rows are dimmed but the ✎ / 📌 buttons still work, letting you pre-assign aliases and static IPs for devices that aren't online yet.

### Running

```bash
# CLI: bind on all interfaces, port 8080
./argusd -listen :8080 \
         -aliases /etc/argusd/aliases.json   # optional: enable alias store
# Open http://<router-ip>:8080/
```

Or mount in your own server:

```go
w := argus.New(argus.WithFetcher(...))

aliases := argusweb.NewAliasStore("/etc/argusd/aliases.json")
dhcp, _ := argusweb.NewUCIDHCPManager() // returns ErrDHCPManagerUnavailable off-OpenWrt

srv := argusweb.NewServer(w,
    argusweb.WithAliases(aliases),
    argusweb.WithDHCPManager(dhcp),
    argusweb.WithOfflineRetention(7*24*time.Hour),
    argusweb.WithOfflineMax(512),
    argusweb.WithWriteAuth(func(r *http.Request) bool {
        return r.Header.Get("X-Token") == os.Getenv("ARGUS_TOKEN")
    }),
)
w.RegisterEventHandler(srv.OnEvent) // feed events into the SSE stream
go http.ListenAndServe(":8080", srv)
```

### HTTP API

All responses are JSON. Writes are gated by `WithWriteAuth` (default: loopback + RFC1918 allowed; everything else returns `403`).

| Route | Methods | Description |
|---|---|---|
| `/` | GET | Dashboard HTML (embedded single file) |
| `/api/devices` | GET | `{count, online, offline, capabilities:{aliases,dhcp}, devices:[...]}`; each row carries `status` / `offline_at_ms` / `alias` |
| `/api/events` | GET | SSE stream; event name = `EventKind.String()` (`ONLINE` / `OFFLINE` / `CHANGE`) |
| `/api/aliases` | GET / POST / DELETE | MAC ↔ friendly-name CRUD; `503` when `WithAliases` is not set |
| `/api/dhcp` | GET / POST / DELETE | Static DHCP reservation CRUD; `503` when `WithDHCPManager` is not set; POST/DELETE accept `?restart_wifi=1` to trigger immediate-apply (v0.15.8+) |
| `/api/dhcp?purge_argus=1` | POST | Removes every `dhcp.argus_*` section (recovery tool, v0.15.3+) |
| `/api/system/restart-network` | POST | `/etc/init.d/network restart` (soft network restart, v0.15.9+) |
| `/api/system/reboot` | POST | `/sbin/reboot` (full router reboot, v0.15.9+) |

POST `/api/dhcp` error codes:

- `400` — invalid MAC / IP / name
- `403` — blocked by `WithWriteAuth`
- `409` — target IP already reserved for a different MAC; body `{error, ip, owner_mac}` names the current owner (v0.15.3+)
- `503` — no `DHCPManager` attached

`applyReport` (the `apply` field in every DHCP write response) contains: `reloaded[]` · `pruned[]` · `arp_flushed` · `kicked` · `wifi_restarted`. The dashboard renders its toast from these fields.

Full wire-shape contract lives in [`STABILITY.md`](./STABILITY.md) — stable public surface since v0.13.0.

### DHCP backend compatibility

`NewUCIDHCPManager()` works on any OpenWrt-like system with the `uci` CLI; other platforms get `ErrDHCPManagerUnavailable`. Verified on MediaTek MT7981 / C-Life vendor firmware (odhcpd) and stock OpenWrt (dnsmasq).

> **Beware of dual DHCP servers** — if your LAN has a secondary router (iStoreOS / OpenClash and similar) with DHCP enabled by default, it will race the main router's offers and some devices will end up with the secondary router as their gateway (static reservations then seem to misbehave randomly). Diagnose with `ip neigh` on the main router (check each device's gateway); fix by disabling DHCP on the secondary: `uci set dhcp.lan.ignore=1 && uci commit dhcp && /etc/init.d/dnsmasq restart`.

---

## Architecture

Six feeds enter the Event Fusion Engine; the Watcher emits events (business), decisions (observability), and errors (failures).

```
                       ┌──────────────┐
                       │   logread    │ ← realtime kernel events
                       │      -f      │   (Connect/Disconnect/Deauth/DHCPACK)
                       └──────┬───────┘
                              │
 ┌─ ubus call ────┐    ┌──────┼──────┐     ┌─ ARP state ──┐
 │ ahsapd.sta or  │ →  │  Event      │  ←  │ ip neigh     │
 │ hostapd.<iface>│    │  Fusion     │     │ FAILED/OK    │
 └────────────────┘    │  Engine     │     └──────────────┘
                       │             │
 ┌─ DHCP leases ──┐    │             │     ┌─ ICMP probe ─┐
 │ /tmp/dhcp.     │ →  │             │  ←  │ ping -c 1    │
 │   leases       │    │             │     │ -W 1         │
 └────────────────┘    └──────┬──────┘     └──────────────┘
                              │
                        ┌─────▼──────┐
                        │  Watcher   │  ← diff + cooldown + flap-suppress
                        └─────┬──────┘
                              │
                 ┌────────────┼────────────┐
                 ▼            ▼            ▼
            EventHandler  DecisionHandler  ErrorHandler
            (business)    (observability)  (failures)
```

See [`ONLINE.md`](./ONLINE.md) and [`OFFLINE.md`](./OFFLINE.md) for detailed decision flows.

---

## API Overview

| Type | Purpose |
|------|---------|
| `argus.Watcher` | Main entry: `New(opts...) *Watcher`, `Run`, `Stop`, `List`, `Known`, `EnsureFetcher`, `FetcherKind` |
| `argus.Event` / `EventKind` | Business events (Online / Offline / Change) |
| `argus.Decision` / `DecisionKind` | Internal decision trace (17 branches) |
| `argus.Config` / `argus.ConfigError` | Tunable thresholds + structured validation errors (v0.9.0+) |
| `argus.Fetcher` | Data source interface, auto-detected |
| `argus.Prober` | Liveness probe; default `ICMPProber{Timeout: 1s}` |
| `argus.Hint` / `argus.HintSource` / `argus.DefaultHintSource` | Injectable enrichment (v0.7.0+) — DHCP/ARP on non-OpenWrt targets |
| `argus.LoggerHandler` / `LogLevel` / `LogAttr` | Structured logging hook (v0.9.0+) |
| `argus.SpanRecorder` / `SpanRecorderFunc` | Distributed-tracing hook (v0.12.0+) |
| `argus.SyslogEvent` | Raw syslog parse result |
| `argus.DetectLocalLocation()` | Parse `/etc/TZ` → `*time.Location` (no global mutation) |
| `argus.SetupLocalTimezone()` | *Deprecated.* Mutates `time.Local` |
| Sentinel errors | `ErrHandlerRequired` / `ErrInvalidConfig` / `ErrNoFetcher` / `ErrFetchFailed` / `ErrAlreadyRunning` (all `errors.Is`-compatible) |
| `github.com/xxl6097/argusd/argusmetrics` | Zero-dep `Counters` + `LabeledCounters` (v0.7.0 / v0.10.0+) |
| `github.com/xxl6097/argusd/argustest` | `FixedFetcher` / `FakeProber` for downstream tests (v0.6.0+) |

Functional options:

```go
argus.WithConfig(cfg)                      // override defaults
argus.WithFetcher(custom)                  // custom data source
argus.WithProber(nil)                      // disable liveness probe
argus.WithBaseline(old.Known())            // seed known-set on restart
argus.WithHintSource(custom)               // custom DHCP/ARP enrichment (v0.7.0+)
argus.WithLogger(h)                        // structured logging (v0.9.0+)
argus.WithSpanRecorder(r)                  // distributed tracing (v0.12.0+)
argus.OnFetcherDetected(func(k) {...})     // detection callback
argus.WithDecisionHandler(func(d) {...})   // decision trace
```

---

## Configuration

All thresholds live in `argus.Config`. Zero values preserve defaults.

```go
w := argus.New(argus.WithConfig(argus.Config{
    // Polling cadence
    PollInterval:  1 * time.Second,   // default 1s
    OfflineMisses: 5,                 // default 5
    FetchTimeout:  3 * time.Second,   // default 3s

    // Anti-flap
    OfflineCooldown:            90 * time.Second,
    CooldownReleaseRSSI:        -65,
    WeakRSSI:                   -80,
    ExtremelyWeakRSSI:          -88,
    WeakMissThreshold:          5,
    ExtremelyWeakMissThreshold: 2,
    FlapSuppressionWindow:      30 * time.Second,
}))
```

Guidelines:

| Scenario | Suggested change |
|----------|------------------|
| Aggressive IoT gateway (tolerate noise) | `FlapSuppressionWindow: 0`, `OfflineCooldown: time.Nanosecond` |
| Home/away automation | keep defaults |
| Crowded WiFi environment | `WeakRSSI: -75`, `WeakMissThreshold: 10` |
| Trust AP table only | `WithProber(nil)` |

---

## Observability

Argus exposes five opt-in observability channels; pick the right one for the right audience.

| Channel | Type | Frequency | Use case |
|---------|------|-----------|----------|
| `EventHandler` (arg to `Run`) | `Event` | Sparse | Business logic (home/away automation) |
| `ErrorHandler` (arg to `Run`) | `error` | Rare | Non-fatal failures |
| `WithDecisionHandler` | `Decision` | Dense | Tuning / debugging |
| `WithLogger` (v0.9.0+) | `LogLevel` + attrs | Lifecycle + anomaly | slog/zap/zerolog bridge |
| `WithSpanRecorder` (v0.12.0+) | span start/finish | Per `Run` + per disconnect | OTel / Datadog tracing |

Plus the `argusmetrics` subpackage for in-process counter aggregation (bridgeable to Prometheus / OTLP in ~10 lines; see godoc).

For raw syslog mirroring, call `WatchSyslog(ctx, func(SyslogEvent), onError)` directly — it's a standalone helper, not a Watcher option.

Sample decision trace:

```
[decision] CONNECT_HINT     BA:79:... (IP=192.168.1.213)
[decision] CONNECT_EMIT     BA:79:... (IP=192.168.1.213)
[event]    ONLINE           BA:79:... 192.168.1.213 iPhone -44(strong) 5G/avgb-5G
[decision] POLL_WEAK_MISS   BA:79:... (RSSI=-82 misses=3/5)
[decision] POLL_WEAK_MISS   BA:79:... (RSSI=-85 misses=5/5)
[decision] OFFLINE_EMIT     BA:79:... (via=poll RSSI=-85)
[event]    OFFLINE          BA:79:...
```

`DecisionHandler` is zero-cost when not registered — no allocations, no `time.Now()` calls.

---

## Roadmap

- [x] ahsapd / hostapd dual fetcher with auto-detection
- [x] syslog `logread -f` real-time stream
- [x] ICMP liveness probe with parallel semaphore
- [x] Cooldown + flap suppression
- [x] Decision handler observability
- [x] `go test -race` clean (multi-Go-version matrix, 1.21–1.25)
- [x] Lifecycle: `Stop` + restart (v0.5.0)
- [x] Portability: `HintSource` abstraction (v0.7.0)
- [x] Metrics: `argusmetrics.Counters` + `LabeledCounters` (v0.7.0 / v0.10.0)
- [x] Structured logging hook `WithLogger` (v0.9.0)
- [x] Structured validation errors `ConfigError` (v0.9.0)
- [x] Distributed tracing hook `SpanRecorder` (v0.12.0)
- [x] Fuzz targets for syslog / DHCP lease parsers (v0.12.0)
- [x] Built-in Web UI (HTTP + SSE, v0.13.0)
- [x] Device aliases with UTF-8 names (v0.14.0 / v0.15.4)
- [x] Static DHCP reservations via UCI + immediate-apply (v0.15.0 / v0.15.2 / v0.15.7 / v0.15.8)
- [x] IP-conflict 409 + one-click replace + PurgeArgusOwned recovery (v0.15.3 / v0.15.5)
- [x] System endpoints: reboot + restart-network (v0.15.9)
- [x] **v1.0 tagged** — Stable surface locked under SemVer v1 rules
- [ ] Direct `ubus` socket integration (skip CLI)
- [ ] IPv6-only device support
- [ ] Home Assistant `device_tracker` bridge
- [ ] Prometheus `/metrics` endpoint (argusweb bridge)

---

## Compatibility

| Platform | Data source | Status |
|----------|-------------|--------|
| MediaTek MT7981 vendor firmware | ahsapd | ✅ Reference target |
| OpenWrt 23.05+ stock | hostapd.* | 🧪 Theoretical, awaiting real-device validation |
| Any Linux with `logread` + `ubus` | syslog-only | ⚠️ Events only, no device table |

Go 1.21+ (N-2 policy: current + two preceding minor versions). No cgo. Cross-compiles to any GOOS/GOARCH that runs OpenWrt.

---

## Contributing

PRs welcome. See [`CONTRIBUTING.md`](./CONTRIBUTING.md). Before submitting, make sure the following pass locally:

```bash
go vet ./...
go test -race ./...
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./cmd/argusd
```

---

## More Docs

- [`CHANGELOG.md`](./CHANGELOG.md) — version history (features & fixes)
- [`STABILITY.md`](./STABILITY.md) — API stability guarantees & v1.0 criteria
- [`ONLINE.md`](./ONLINE.md) — online decision deep-dive
- [`OFFLINE.md`](./OFFLINE.md) — offline + cooldown analysis
- [`docs/SIGHUP-real-device-test.md`](./docs/SIGHUP-real-device-test.md) — v0.5.0 Stop+Restart real-router validation report
- [`docs/blog/ios-static-ip.md`](./docs/blog/ios-static-ip.md) — debugging story: the 3 ways "set static IP" silently fails on iOS + OpenWrt
- [GoDoc](https://pkg.go.dev/github.com/xxl6097/argusd) — API reference

---

## License

MIT © 2026 — see [`LICENSE`](./LICENSE)

---

*"Every station. Every event. Every eye open."*
