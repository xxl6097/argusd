# Migration Guide · 升级指南

This document collects per-release migration notes. Argus follows **Semantic
Versioning** + the stability contract in [`STABILITY.md`](./STABILITY.md).
The 0.x line was **minor-zero stable** — no breaking change to Stable public
surface within any `0.x.y` window. v1.0.0 crystallizes that contract.

**中文** — 本文档汇总每个版本的升级注意事项。Argus 遵循 [`STABILITY.md`](./STABILITY.md)
中定义的稳定性契约。0.x 线承诺 **minor-zero 稳定** — 同一 `0.x.y` 线内稳定公开
表面不会破坏。v1.0.0 将该契约固化。

---

## Upgrading to v1.0.0

**TL;DR** — if you were on **any v0.7.0+ release**, this is a no-op upgrade:
`go get -u github.com/xxl6097/argusd@v1.0.0`, rebuild, done. No code
change needed.

**中文** — 从 v0.7.0 之后的任意版本升级 v1.0.0 无需修改代码,拉取新版本
重新构建即可。

### What v1.0.0 locks in

From this release onward, the "Stable public surface" list in
[`STABILITY.md`](./STABILITY.md) is frozen under SemVer v1 rules:

- **Breaking changes** to Stable surface require a `v2` module path
  (`github.com/xxl6097/argusd/v2`). No breaking change will ship as a
  minor or patch bump.
- **Additions** (new exported types, functions, methods, options, errors,
  fields; new `DecisionKind` / `EventKind` constants; new `Config`
  fields with zero-value-preserves-default semantics) remain
  minor/patch bumps and are non-breaking.
- **JSON field names** (`mac`, `ip`, `hostname`, … per STABILITY.md)
  are part of the contract. New fields use `omitempty` so sparse
  documents round-trip cleanly.
- **`argusweb` wire shapes** (`/api/devices`, `/api/events`,
  `/api/aliases`, `/api/dhcp` including `applyReport` fields
  `reloaded` / `pruned` / `arp_flushed` / `kicked` / `wifi_restarted`,
  plus `/api/system/reboot` and `/api/system/restart-network` since
  v0.15.9) are part of the Stable surface.
- **Zero-cost `DecisionHandler` when unset** (≤ 2 ns/op, 0 allocs)
  stays a CI-enforced guarantee via `BenchmarkEmitDecisionNil`.

### Recommended hygiene for v1.0.0 consumers

1. **Pin the module version** in `go.mod`: `require github.com/xxl6097/argusd v1.0.0`
2. **Prefer typed errors over string match**:
   ```go
   if errors.Is(err, argus.ErrInvalidConfig) { /* … */ }
   ```
3. **Wire `argusmetrics` for observability** instead of parsing
   `DecisionHandler` trace strings:
   ```go
   m := argusmetrics.New()
   w := argus.New(argus.WithDecisionHandler(m.OnDecision))
   // later: prom.Add(float64(m.Snapshot()["CONNECT_EMIT"]))
   ```
4. **Use `argustest.FixedFetcher` / `FakeProber`** in downstream tests
   instead of reimplementing fixtures.
5. **If you embed `argusweb`**, use the `applyReport` JSON fields
   (all `omitempty`) to render status instead of pattern-matching the
   `message` string, which is informational.

### Nothing changed at the code level vs v0.15.9

v1.0.0 points at the same commit as v0.15.9 + the v0.15.4 → v0.15.9
changes already landed on `main`. No symbol was added, removed, or
renamed between v0.15.9 and v1.0.0; only docs and the tag annotation
changed.

### Cumulative deltas since v0.14.0 (non-breaking additions)

| Release | New additions |
|---|---|
| v0.14.0 | `WithAliases`, `AliasStore`, `/api/aliases` |
| v0.15.0 | `WithDHCPManager`, `DHCPManager`, `StaticLease`, `NewUCIDHCPManager`, `/api/dhcp` |
| v0.15.3 | `ErrIPAlreadyReserved`, `(*UCIDHCPManager).PurgeArgusOwned`, 409 response shape, `?purge_argus=1` query |
| v0.15.4 | `validateName` UTF-8 whitelist (names now accept Chinese / spaces / dots) |
| v0.15.5 | 409 one-click replace in dashboard (no API change; UI only) |
| v0.15.7 | `applyReport.ARPFlushed` field; `dismissTime` bumped to 30s |
| v0.15.8 | `?restart_wifi=1` query on `POST`/`DELETE /api/dhcp`; `applyReport.WiFiRestarted` field |
| v0.15.9 | `POST /api/system/reboot`, `POST /api/system/restart-network` |

All items above are additions only — old clients remain compatible.

---

## 0.6.0 → 0.7.0 (2026-05-10)

### Non-breaking

- `hint` (unexported) renamed to `Hint` (exported). External consumers
  couldn't reference the old name, so this is cosmetic.
- New `HintSource` interface; default behavior unchanged — the package
  still reads `/tmp/dhcp.leases` + `ip neigh show` with a 5 s cache.
- New `argusmetrics` subpackage — zero-dependency, opt-in.

### Optional adoption

- On non-OpenWrt systems (stock Linux, macOS dev loop, custom firmware):
  ```go
  w := argus.New(
      argus.WithHintSource(&argus.DefaultHintSource{
          LeasesPath: "/var/lib/misc/dnsmasq.leases",
          ARPCommand: []string{"ip", "neigh", "show"},
          CacheTTL:   10 * time.Second,
      }),
  )
  ```
- To expose counters to Prometheus / OTel / StatsD, wire
  `argusmetrics.Counters` as the `DecisionHandler` and snapshot
  periodically.

---

## 0.5.0 → 0.6.0 (2026-05-10)

### Non-breaking

- Struct tags added to `Event` / `Device` / `Change` / `Decision` /
  `Config`. Go-level shape unchanged; JSON output now uses snake_case
  field names.
- `EventKind` / `DecisionKind` marshal to English `String()` instead
  of the integer. `EventKind.UnmarshalJSON` still accepts the legacy
  integer form for backward compatibility.
- New `argustest` subpackage (`FixedFetcher`, `FakeProber`).

### Action for JSON consumers

If you previously parsed `EventKind` from JSON as an integer, you can
keep existing logic — `UnmarshalJSON` accepts both forms. For new
pipelines, match on the string name (`"ONLINE"` / `"OFFLINE"` / `"CHANGE"`).

---

## 0.4.0 → 0.5.0 (2026-05-09)

### Non-breaking

- New `(*Watcher).Stop(ctx) error` — graceful shutdown.
- New `ErrAlreadyRunning` sentinel — concurrent `Run` calls on the
  same Watcher now fail-fast (previously undefined behavior).
- `Run` can be called again on the same Watcher after `Stop` returns
  `nil`. Preserved state: `known` / `offlineCooldown` / `lastEventAt` /
  detected `Fetcher`. Reset state: `misses` / `disconnectInFlight` /
  `syslogHints` / `droppedHints`.

### Action for long-running daemons

Switch from "create a new Watcher on SIGHUP" to `Stop` + `Run`:

```go
// Before (v0.4.0) — re-emits Online for every device on reload
w = argus.New(cfg)
go w.Run(ctx, onEvent, onError)
// on SIGHUP:
cancel()
w = argus.New(newCfg)
go w.Run(newCtx, onEvent, onError)

// After (v0.5.0+) — preserves known set, no false Online events
w := argus.New(cfg)
go w.Run(ctx, onEvent, onError)
// on SIGHUP:
stopCtx, _ := context.WithTimeout(context.Background(), 5*time.Second)
w.Stop(stopCtx)
// apply newCfg via a fresh Option slice if needed
go w.Run(ctx, onEvent, onError)
```

---

## 0.3.0 → 0.4.0 (2026-05-09)

### Non-breaking

- Panic isolation wraps `EventHandler` / `ErrorHandler` /
  `DecisionHandler` / `OnFetcherDetected`. Previously a panic in user
  code killed the Watcher goroutine; now it's reported via `onError`
  (for `EventHandler`) or swallowed (others).
- `diff()` now releases `stateMu` before invoking event callbacks.
  A slow or panicking user callback no longer blocks `Known()`,
  `List()`, or the next poll tick.

No action required.

---

## 0.2.0 → 0.3.0 (2026-05-09)

### Non-breaking

- New `Config.DisableCooldown` / `Config.DisableFlapSuppression`
  explicit boolean switches. Previous magic values (`time.Nanosecond`
  for cooldown; `FlapSuppressionWindow=0`) still disable the feature,
  but prefer the booleans for clarity.
- New `(*Watcher).Known()` + `WithBaseline(map[string]Device) Option`
  for hot-reload seeding.
- Sentinel errors: `ErrHandlerRequired`, `ErrInvalidConfig`,
  `ErrNoFetcher`, `ErrFetchFailed`. Existing `fmt.Errorf` messages are
  preserved; `errors.Is` now works alongside them.
- `Run` now calls `Config.Validate()` at entry. Fails fast on invalid
  configs with `ErrInvalidConfig`. No behavior change for
  `DefaultConfig()` users.

### Deprecated

- `SetupLocalTimezone()` — mutates `time.Local` (library anti-pattern).
  Use `DetectLocalLocation()` and `t.In(loc)` instead. Kept for
  backward compatibility; will not be removed in the 0.x or 1.x line.

---

## 0.1.0 → 0.2.0 (2026-05-09)

### Non-breaking

- Disconnect hint dedup — duplicate syslog lines within milliseconds
  (disconnect / deauth / Del Sta) no longer spawn 3 workers; only the
  first runs the 500 ms wait + ping path. Others emit the new
  `DecisionDisconnectSkippedInflight` trace.
- No public API change.

---

## Questions?

Open an issue with the `api-stability` or `migration` label.
