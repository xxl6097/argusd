// Package argus provides real-time device presence detection for OpenWrt routers.
//
// Argus — named after the hundred-eyed giant of Greek myth whose eyes never all slept —
// fuses six data sources into a single millisecond-grade event stream
// (Online / Offline / Change): ahsapd (vendor) or hostapd.* (stock) via ubus,
// logread -f syslog, /tmp/dhcp.leases, ip neigh, and ICMP liveness probes.
//
// # Quick start
//
//	w := argus.New()                          // auto-detect fetcher
//	devices, _ := w.List(ctx)                 // one-shot snapshot
//	err := w.Run(ctx, func(e argus.Event) {   // real-time stream
//	    // handle EventOnline / EventOffline / EventChange
//	}, nil)                                   // nil ErrorHandler = discard errors
//
// # Architecture
//
//	                  ┌───────────────────────────────┐
//	                  │   HintSource (enrichment)     │
//	                  │   /tmp/dhcp.leases + ip neigh │
//	                  └────────────────┬──────────────┘
//	                                   │ {IP, Hostname}
//	                                   ▼
//	┌──────────┐    ┌───────────┐    ┌──────┐    ┌────────────────┐    ┌────────────────┐
//	│ Fetcher  │───▶│ baseline  │───▶│ diff │───▶│ DecisionHandler│───▶│ EventHandler   │
//	│ (ahsapd/ │    │ (known)   │    │      │    │  (optional,    │    │  Online/       │
//	│  hostapd)│    └───────────┘    └───┬──┘    │   zero-cost    │    │  Offline/      │
//	└──────────┘                         │       │   when nil)    │    │  Change        │
//	      ▲                              ▼       └────────────────┘    └────────────────┘
//	      │                       ┌──────────┐
//	      │                       │  Prober  │ (ICMP liveness; filters out fake-online)
//	      │                       └──────────┘
//	      │
//	┌────┴───────────┐
//	│ logread -f     │ (syslog hint stream; disconnect / deauth / assoc / DHCP)
//	└────────────────┘
//
// The diff stage also consults an offlineCooldown map (90 s default) to suppress
// weak-signal flaps at the edge, and a FlapSuppressionWindow (30 s default) to
// collapse rapid-fire reconnects.
//
// # Extension points
//
//   - [WithFetcher]: plug in a custom data source (tested with [argustest.FixedFetcher]).
//   - [WithProber]: replace ICMP liveness check (e.g. a faked in-memory map for tests).
//   - [WithHintSource]: inject a custom enrichment source for non-OpenWrt firmwares
//     with /var/lib/misc/dnsmasq.leases or equivalent.
//   - [WithDecisionHandler]: receive internal decision traces (zero-cost when unset).
//   - [WithLogger]: structured logging adapter (slog / zap / zerolog in ~5 lines).
//
// # Lifecycle
//
// A single *Watcher may be [Watcher.Run]-[Watcher.Stop]-[Watcher.Run]ed repeatedly
// (SIGHUP hot-reload pattern). State preserved across restart: known set,
// offlineCooldown, lastEventAt, detected Fetcher. State reset: in-flight misses,
// disconnect dedup map, syslog hint channel, drop counter. Concurrent Run calls on
// the same Watcher fast-fail with [ErrAlreadyRunning].
//
// # Observability
//
//   - Logs via [WithLogger] (library never logs from the hot path; only lifecycle
//     and recoverable anomalies).
//   - Metrics via the [github.com/xxl6097/argus/argusmetrics] subpackage
//     ([argusmetrics.Counters] for totals, [argusmetrics.LabeledCounters] for
//     per-SSID / per-MAC / per-band bucketing), both zero-dependency.
//   - Decision traces via [WithDecisionHandler] — every internal choice the diff
//     engine makes surfaces as a [Decision] record.
//
// # Error handling
//
// Library errors are matchable with [errors.Is]:
//
//	errors.Is(err, argus.ErrHandlerRequired)  // nil callback
//	errors.Is(err, argus.ErrInvalidConfig)    // Config.Validate rejected
//	errors.Is(err, argus.ErrNoFetcher)        // ubus detection found nothing
//	errors.Is(err, argus.ErrFetchFailed)      // baseline fetch failed
//	errors.Is(err, argus.ErrAlreadyRunning)   // concurrent Run on same Watcher
//
// [Config.Validate] returns a *[ConfigError] exposing the offending field, reachable
// via [errors.As] for form-level UI feedback.
//
// # Stability
//
// See the STABILITY.md document in the repository. The 0.x line is "minor-zero
// stable" (no breaking change to listed Stable surface within a 0.x.y window);
// v1.0 will lock that surface under SemVer v1 rules.
//
// # Supported Go versions
//
// The current Go release and the two preceding minors (N-2 policy). CI matrix
// tests every commit on Go 1.21 – 1.25.
//
// # Chinese summary · 中文摘要
//
// Argus 是一个针对 OpenWrt 路由器的实时设备感知库, 融合 ahsapd / hostapd / syslog /
// DHCP / ARP / ICMP 六路数据源形成毫秒级事件流 (Online / Offline / Change)。
// 名字取自希腊神话中的百眼巨人 —— 他的眼睛永远不会同时闭上。零第三方依赖,
// 内置 argusmetrics 零分配计数器, 通过 WithDecisionHandler 暴露决策 trace,
// 生命周期 Stop / Restart 支持热重载。
package argus
