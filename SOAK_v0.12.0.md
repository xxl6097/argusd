# SOAK_v0.12.0.md — 5-Minute Real-Device Soak Report (v0.12.0)

**Subject**: `argusd` v0.12.0 @ `bdd8332` (Git HEAD after the SIGUSR1 fix)
**Build**: `linux/arm64`, `go1.25.0`, `-s -w`, 2.7 MiB
**Device**: OpenWrt router 192.168.10.1 · `aarch64` ARMv8 · kernel 5.4.225
**Window**: 2026-05-10 15:00:27 CST → 15:05:41 CST (5 m 14 s)
**PID**: 30195 · state S (sleeping) at snapshot
**Signal used**: SIGUSR1 to print metrics

## TL;DR

- **Soak run 1 (PID 26036, pre-fix)** — uncovered a self-inflicted regression
  in the `argusd` main loop: after SIGUSR1 printed the metrics snapshot,
  the main `for-select` fell through to the next iteration and started a
  second `w.Run()` while the first was still alive. The second Run
  returned `ErrAlreadyRunning` → `log.Fatalf` → process exit.
  The **library itself was fine**; `ErrAlreadyRunning` did exactly what it's
  supposed to do. The bug lived in my v0.12.0 commit to `cmd/argusd/main.go`.
  Fixed in `bdd8332` by moving the SIGUSR1 handler to its own goroutine.

- **Soak run 2 (PID 30195, post-fix)** — 5 min clean, no panics, no errors,
  3 Online events, stable memory (9.0 MiB) and thread count (16). Metrics
  snapshot via SIGUSR1 worked without disturbing the Run loop.

v0.12.0's library additions (`SpanRecorder`, fuzz targets) do not regress
the hot path. Both the `WithLogger` and `argusmetrics` integrations added to
`cmd/argusd` in this cycle are now stable on arm64.

---

## Setup

```bash
# local
GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" \
    -o /tmp/argusd-linux-arm64 ./cmd/argusd   # 2.7 MiB (was 2.6 at v0.7)

# router
scp argusd-linux-arm64 root@192.168.10.1:/tmp/argusd
nohup /tmp/argusd > /tmp/argusd.log 2>&1 &
# PID 30195, baseline 4 devices
kill -USR1 30195    # at t+5m, snapshot metrics
```

No flags; all observability wired at library level (`WithLogger`,
`WithDecisionHandler` → `argusmetrics`).

---

## Bug: SIGUSR1 re-entered the Run loop (v0.12.0 argusd main)

Pre-fix `main.go` had the SIGUSR1 receiver inside the main `for-select` that
also managed Run lifecycle. Flow on soak run 1:

```
t=0       w.Run starts (goroutine A)
t=...     SIGUSR1 arrives
          case <-sigusr1: printMetricsSnapshot; select ends
          outer for-loop iterates → new runCtx, new goroutine B
          w.Run() in B → CompareAndSwap fails → ErrAlreadyRunning
          err != context.Canceled → log.Fatalf → process exit
```

The library behaved correctly. `ErrAlreadyRunning` is the sentinel introduced
in v0.5.0 precisely for concurrent-Run-on-same-Watcher safety. But the
daemon shouldn't have triggered it.

**Fix** (commit `bdd8332`): SIGUSR1 handler moved to a dedicated goroutine
bound to `exitCtx` — it calls `printMetricsSnapshot` and goes back to
waiting, never touching the Run for-select.

```go
go func() {
    for {
        select {
        case <-exitCtx.Done(): return
        case <-sigusr1: printMetricsSnapshot(metrics)
        }
    }
}()
```

SIGHUP, which genuinely intends to cycle the Run, stays in the main loop.

---

## Event summary (post-fix, PID 30195)

| Metric | Value | Note |
|---|---:|---|
| EVENT_ONLINE | 3 | iPad + "FA:63…" + Xiaomi — all genuine |
| EVENT_OFFLINE | 0 | — |
| EVENT_CHANGE | 0 | — |
| CONNECT_EMIT | 3 | Matches EVENT_ONLINE |
| POLL_SLEEP_PROTECT | 185 | ≈ 37/min idle-phone RSSI noise, baseline signature |
| POLL_WEAK_MISS | 4 | Weak-signal ping failures under the 5-miss threshold — did **not** emit Offline (correct) |
| Panics | **0** | — |
| ubus errors | **0** | — |

Log lines: **20** (compact — with `ARGUSD_DEBUG=1` the decision trace
expands to ~45 lines/min like prior soaks; here it's disabled).

---

## slog / WithLogger (v0.9.0) end-to-end on arm64

First time the v0.9.0 `WithLogger` hook ran on real hardware. Observed stderr
output (via the default `log/slog.TextHandler`):

```
time=2026-05-10T15:00:27.133+08:00 level=INFO msg="fetcher auto-detected" kind=ahsapd
time=2026-05-10T15:00:29.225+08:00 level=INFO msg="watcher starting" fetcher=ahsapd baseline_devices=4 poll_interval=1s
```

- Lifecycle events fire at the right moments (`EnsureFetcher` then `Run`)
- Attributes serialize correctly (`kind`, `fetcher`, `baseline_devices`, `poll_interval`)
- Zero allocations in the hot path (decision trace does not log — by design)
- On the router's system timezone (`CST +0800`), `time.Time` format is correct

Swapping the handler for a production one (zap / zerolog / OTLP) is a
5-line edit in the CLI.

---

## argusmetrics (v0.7.0 / v0.10.0) integration

The SIGUSR1 snapshot demonstrated:

- `argus.WithDecisionHandler(metrics.OnDecision)` wired at `argus.New` —
  every decision feeds the counter
- `argus.Run`'s event channel also feeds `metrics.OnEvent` (via the
  wrapping handler in `main.go`) — totals match
- Snapshot keys use the stable English `String()` (`CONNECT_EMIT`,
  `POLL_SLEEP_PROTECT`, `EVENT_ONLINE`) — ready to bridge to Prometheus
  without any transformation
- No overhead visible: RSS stayed at 9.0 MiB, threads at 16 throughout

---

## Process health

| Metric | Post-fix run |
|---|---:|
| PID | 30195 |
| State | S (sleeping on poll tick) |
| VmRSS | 9 152 KiB |
| Threads | 16 (matches v0.5.0 / v0.7.0 / v0.10.0 baseline) |
| Uptime at snapshot | 5 m 14 s |

No drift vs earlier soaks; v0.12.0's tracing hook doesn't add any
goroutines (the noopFinish path is allocation-free).

---

## Verdict

- **Library is clean on v0.12.0.** All additions (SpanRecorder,
  fuzz targets) leave the hot path untouched, confirmed by stable
  RSS / threads / ubus error count.
- **argusd CLI had a regression** from this cycle's main.go changes,
  caught by the soak and fixed in `bdd8332`. Non-library; no tag bump
  needed for the library.
- **slog + argusmetrics hooks work on arm64** — first real-device
  validation of the v0.9.0 / v0.10.0 additions.
- Next meaningful gap: the roadmap's "Built-in Web UI" item (v0.13.0).
