# Changelog

All notable changes to **Argus** will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

**EN** — Each release section records new features, behavior changes, and bug fixes under the labels **Added / Changed / Deprecated / Removed / Fixed / Security**. The topmost `[Unreleased]` section accumulates changes landed on `main` since the last tag.

**中文** — 每个版本节按 **Added(新增) / Changed(变更) / Deprecated(废弃) / Removed(移除) / Fixed(修复) / Security(安全)** 分类记录。顶部 `[Unreleased]` 节收集自上一个 tag 以来合入 `main` 的变更,发版时移动到对应版本节。

---

## [Unreleased]

<!-- 新特性 / Bug 修复请在这里追加. 发版时由 maintainer 剪到下面对应版本节. -->

---

## [0.6.0] - 2026-05-10

Focus on **config ergonomics**: make the library trivial to drop into a
daemon that reads config from a file and publishes events to Kafka /
HTTP webhooks. No breaking change.

### Added · 新增

- **`argustest` subpackage** — public test helpers for downstream:
  - `FixedFetcher{Devices, Err}` — deterministic `Fetcher` with injectable error
    and call counter
  - `FakeProber{Reach, AllReachable}` — IP-to-reachability map with concurrent
    `Set` method
  Consumers writing unit tests for business logic on top of Argus can
  `import "github.com/xxl6097/argus/argustest"` instead of forking internal
  fixtures. (`argustest/argustest.go`)

- **JSON serialization is now part of the Stable public surface** (see
  [`STABILITY.md`](./STABILITY.md)):
  - `Event` fields: `time` / `kind` / `device` / `changes`
  - `Device` fields: `mac` / `ip` / `hostname` / `vendor` / `type` / `radio` /
    `ssid` / `channel` / `rssi` / `uptime_ns` / `access_time` / `last_seen`
  - `Change` fields: `field` / `old` / `new`
  - `Decision` fields: `time` / `kind` / `mac` / `detail`
  - `Config` fields: snake_case mirrors of the Go field names
  - `EventKind` / `DecisionKind` marshal to English `String()`
    (`"ONLINE"` / `"CONNECT_EMIT"` / …), not the underlying integer. The
    integer values remain `Evolving` per STABILITY.md so renumbering stays
    safe in future versions.
  - `EventKind.UnmarshalJSON` accepts both the string form and the legacy
    integer form for backward compatibility with data serialized by older
    versions.
  All fields use `omitempty` so sparse config files / compact events stay
  small on the wire.

- **`ExampleConfig_jsonReload`** — godoc example showing `/etc/argusd.json`
  style load via `json.Unmarshal(..., &cfg)` + `argus.WithConfig(cfg)`.
  (`example_test.go`)

- **`ExampleFixedFetcher`** — godoc example in the `argustest` subpackage.
  (`argustest/example_test.go`)

- **JSON round-trip tests** — `TestEventJSONRoundTrip`,
  `TestEventKindUnmarshalFromInt`, `TestChangeJSONFields`,
  `TestConfigJSONRoundTrip`, `TestDecisionJSONFields`.
  (`json_test.go`)

### Changed · 变更

- Struct tags added to `Device` / `Event` / `Change` / `Decision` / `Config`.
  No Go-level breaking change (existing consumers unaffected); new JSON
  field names are the public contract going forward.

### Documentation

- `STABILITY.md` expanded with an explicit "JSON serialization" section
  documenting every stable field name and adds `argustest` subpackage to
  the Stable surface.

---

## [0.5.0] - 2026-05-09

Lifecycle: add graceful stop and restart on the same `*Watcher`. Closes the
last Level-5 API gap — long-running services can now hot-reload config on
SIGHUP without re-emitting Online for every known device.

### Added · 新增

- **`(*Watcher).Stop(ctx) error`** — graceful shutdown that cancels the
  internal Run ctx and waits for all spawned goroutines (syslog listener,
  hint consumer, hint workers) to exit via an internal `sync.WaitGroup`.
  - Idempotent: no-op when no Run is active.
  - Returns `context.DeadlineExceeded` on stop-ctx timeout; workers continue
    to exit in the background.
  - After Stop returns `nil`, `Run` can be called again on the same Watcher.
  (`watcher.go`)

- **`ErrAlreadyRunning` sentinel** — concurrent `Run` calls on the same
  Watcher fail-fast with this error (matchable via `errors.Is`), instead
  of silently corrupting shared state.
  (`errors.go`)

- **Restart semantics** — on second `Run`:
  - **Preserved**: `known`, `offlineCooldown`, `lastEventAt`, detected
    `Fetcher` / `detectKind` (`sync.Once` caches)
  - **Reset**: `misses`, `disconnectInFlight`, `syslogHints` channel
    (recreated), `droppedHints` counter
  Rationale: timeless state should survive config reload; transient state
  from the previous run would poison new decisions.
  (`watcher.go:Run`)

- **`ExampleWatcher_Stop`** — SIGHUP hot-reload pattern runnable on
  pkg.go.dev. (`example_test.go`)

- **9 regression tests** in `lifecycle_test.go`:
  - `TestRunConcurrentReturnsAlreadyRunning`
  - `TestStopIdempotent` / `TestStopBeforeRun`
  - `TestRunAfterStopSucceeds`
  - `TestRestartPreservesKnownAndCooldown`
  - `TestRestartResetsTransients`
  - `TestStopWaitsForDisconnectWorker` — uses a slow prober to force a
    real worker wait, verifies Stop blocks ≥ 300ms
  - `TestStopWithTimeout` — verifies `context.DeadlineExceeded` surface
  - `TestGoroutineLeakOnRestart` — 30-cycle Run/Stop loop, asserts
    goroutine count stable within ±5

### Changed · 变更

- `Run` docstring no longer claims "不支持多次 Run"; now documents the
  CAS guard, restart semantics, and state-preservation contract.
- New `Watcher` fields (unexported): `running atomic.Bool`, `runWG sync.WaitGroup`,
  `runCancel context.CancelFunc`.
- Hint worker goroutines in `runSyslogConsumer` now register with `runWG`
  so `Stop` waits for them to complete.
- `runSyslog` and `runSyslogConsumer` now capture the current Run's
  `syslogHints` channel by value at entry, ensuring no race with the
  channel's recreation on restart.

### STABILITY

`STABILITY.md` v1.0 checklist item for multi-Run support removed (now
implemented). `Stop` and `ErrAlreadyRunning` added to the Stable public
surface.

---

## [0.4.0] - 2026-05-09

Focus on **production-grade robustness**: user callbacks can no longer kill
Watcher goroutines, the zero-cost `DecisionHandler` claim is now CI-backed,
pkg.go.dev shows runnable examples, and the API stability contract is explicit.

### Added · 新增

- **Panic isolation for all user callbacks**
  `EventHandler` / `ErrorHandler` / `DecisionHandler` / `OnFetcherDetected`
  are now wrapped in `defer recover()`. A panic in user code:
  - `EventHandler` — caught, reported to `onError` as
    `"argus: EventHandler panicked: <value>"`, and does NOT kill the diff
    goroutine. Subsequent events continue to flow.
  - `ErrorHandler` — caught and silently swallowed (no recursion).
  - `DecisionHandler` — caught and silently swallowed (hot path).
  - `OnFetcherDetected` — caught and silently swallowed.
  (`watcher.go`)

- **`diff()` emits events after releasing `stateMu`**
  Internally refactored to collect events into a `pending []Event` slice.
  `Run` dispatches them via `safeInvokeEvent` AFTER unlocking the mutex.
  Prior to this, a slow or panicking user callback would hold `stateMu`,
  blocking `Known()`, `List()`, and the next poll tick.
  (`watcher.go`)

- **`example_test.go` — 6 runnable godoc examples**
  Covers `New`, `Watcher.List`, `WithDecisionHandler`, `WithBaseline`,
  `Config` tuning, and typed-error handling. pkg.go.dev now renders them
  as "Try it" code blocks at the top of the package page.
  (`example_test.go`)

- **Benchmarks backing the zero-cost `DecisionHandler` claim**
  - `BenchmarkEmitDecisionNil`: **1.0 ns/op, 0 allocs/op** on M4
  - `BenchmarkEmitDecisionActive`: 33 ns/op, 0 allocs/op (defer+recover overhead)
  - `BenchmarkSafeInvokeEventOK`: monitors panic-safe wrapper cost in normal path
  Makes the docstring promise a CI-enforceable guarantee.
  (`panic_test.go`)

- **`STABILITY.md` — explicit API compatibility contract**
  Lists "Stable" / "Evolving" / "Unstable" surface, documents the
  **minor-zero-stable** policy for the 0.x line, and defines the 7-point
  checklist required before tagging v1.0.
  (`STABILITY.md`)

### Changed · 变更

- `diff()` signature: dropped `onEvent EventHandler`, now returns
  `[]Event` of pending events. The `Run` caller dispatches via the new
  panic-safe path. **This is an internal function**; no public API impact.
- `handleDisconnectHint()` / `emitConnectEvent()` now take `onError` so
  their direct `onEvent` calls can report callback panics. Internal-only.
- `ScheduleOnFetcherDetected` callback invocation now also recovers from
  panics (detector runs once under `sync.Once`).

### Tests · 测试

- `TestEventHandlerPanicDoesNotKillWatcher` — verifies panic capture and
  error reporting.
- `TestErrorHandlerPanicDoesNotRecurse` — verifies 1-second max duration
  when `ErrorHandler` itself panics (no recursion).
- `TestDecisionHandlerPanicSwallowed`
- `TestDiffEventPanicContained` — verifies event-N panic does not block
  event-N+1 delivery.

All pass under `go test -race`.

---

## [0.3.0] - 2026-05-09

Focus on **API ergonomics & robustness** — no behavior change for existing users
on default config, new opt-in knobs for lifecycle handoff and feature toggling,
and typed errors for programmatic error handling.

### Added · 新增

- **`Config.DisableCooldown` / `Config.DisableFlapSuppression`**
  Explicit boolean switches to turn off cooldown / flap-suppression. Previously
  required the magic value `time.Nanosecond` or `FlapSuppressionWindow=0`
  (which the `WithConfig` zero-value convention treated as "preserve default").
  Default `false` preserves existing behavior. (`watcher.go`)

- **`Watcher.Known() map[string]Device`**
  Thread-safe deep-copy snapshot of the currently-known device set, for use
  with the new `WithBaseline` option. (`watcher.go`)

- **`WithBaseline(map[string]Device) Option`**
  Seeds a new `Watcher`'s `known` set at construction time. Intended for hot
  reload / process restart: take `old.Known()`, pass to `New(WithBaseline(snap))`
  to avoid the entire device table re-emitting as "new online" events on boot.
  (`watcher.go`)

- **Sentinel errors** (`errors.go`)
  - `ErrHandlerRequired` — `Run` called with `nil` `onEvent`
  - `ErrInvalidConfig` — `Config.Validate()` rejected the config
  - `ErrNoFetcher` — ubus auto-detect found no `ahsapd` / `hostapd`
  - `ErrFetchFailed` — initial baseline fetch failed

  All reachable via `errors.Is`. Existing `fmt.Errorf` wrappers are preserved
  for their human-readable context.

### Changed · 变更

- **`Run` now calls `Config.Validate()` at entry.** Previously `Config` validation
  was exported but only invoked by user code. Invalid configs now fail fast before
  any goroutine starts, returning `ErrInvalidConfig`. No behavior change for
  users on `DefaultConfig()` / sane configs. (`watcher.go`)

### Deprecated · 废弃

- **`SetupLocalTimezone()`** is marked `Deprecated` in its docstring. It mutates
  global `time.Local`, which is a library anti-pattern. Consumers should use
  `DetectLocalLocation()` to get a `*time.Location` and format with
  `t.In(loc)` (or set `time.Local` in their own `main`). The function itself
  is retained for backward compatibility and will not be removed.
  (`timezone.go`)

### Tests · 测试

- `TestRunReturnsSentinelErrHandlerRequired` / `TestRunReturnsSentinelErrInvalidConfig`
- `TestConfigDisableCooldownStopsSuppression`
- `TestConfigDisableFlapSuppression`
- `TestWithBaselineSeedsKnown`
- `TestKnownReturnsIndependentCopy`

All pass under `go test -race`.

---

## [0.2.0] - 2026-05-09

### Changed · 变更

- **Disconnect hint dedup** · 断开提示去重
  `handleDisconnectHint` now tracks an in-flight MAC set and short-circuits
  duplicate hints. A typical disconnect emits 3 syslog lines (disconnect /
  deauth / Del Sta) within milliseconds, spawning 3 workers. Previously all
  three entered the 500 ms wait + ping path; the second/third only no-op'd
  after the first deleted the MAC from `known`. Now only the first worker
  runs the full path; the rest emit `DISCONNECT_SKIP_INFLIGHT` and return
  immediately. Saves ≈ 2 × (500 ms sleep + ping cost) and avoids redundant
  ping of an already-known-offline IP under burst.
  No behavior change to event emissions — still exactly one `EventOffline`
  per logical disconnect.
  Observed on a real MT7981 router: 3 `DISCONNECT_HINT` traces previously
  all entered the slow path; now 1 runs and 2 are skipped. (`watcher.go`,
  `decision.go`)

### Added · 新增

- New `DecisionKind`: `DecisionDisconnectSkippedInflight` (string
  `DISCONNECT_SKIP_INFLIGHT`, label "跳过(已在处理)"). Surfaces the
  dedup decision in `DecisionHandler` traces. (`decision.go`)
- Test `TestHandleDisconnectHintDedupesInFlight` covers the dedup path
  under `-race`. (`watcher_test.go`)

---

## [0.1.0] - 2026-05-09

Initial public release · 首次公开发布。

### Added · 新增

- **Multi-source fusion engine** · 多源融合引擎
  Fuse six data sources into one event stream: `ahsapd` / `hostapd.*` (via `ubus`),
  `logread -f` syslog stream, `/tmp/dhcp.leases`, `ip neigh` ARP states, ICMP
  liveness probe. Emits `EventOnline` / `EventOffline` / `EventChange`.
  (`watcher.go`, `fetcher.go`, `hostapd.go`, `logwatch.go`, `enrich.go`, `prober.go`)

- **Zero-config vendor detection** · 零配置多厂商兼容
  `DetectFetcher` auto-selects `AhsapdFetcher` when `ahsapd.sta` is on `ubus`,
  falls back to `HostapdFetcher` scanning all `hostapd.*` interfaces.
  (`detect.go`)

- **Sub-second event pipeline** · 毫秒级事件管线
  Channel A (`runSyslog` → `runSyslogConsumer`, 16 concurrent workers) produces
  online/offline hints in ~0–1.5 s via kernel logs (`New Sta`, `AP SETKEYS DONE`,
  `DHCPACK`, `Del Sta`, `DE-AUTH`, `wifi_sys_disconn_act`).
  Channel B polls every `PollInterval` (default 1 s) as fallback.
  (`watcher.go:runSyslog`, `runSyslogConsumer`, `handleConnectHint`,
  `handleDisconnectHint`)

- **Three-layer offline filter** · 三层离线筛选
  (1) `ICMPProber` ping filter; (2) AP association table + RSSI tiers
  (`WeakRSSI` / `ExtremelyWeakRSSI`); (3) ARP `FAILED`/`INCOMPLETE` state.
  (`prober.go:filterAlive`, `watcher.go:diff`)

- **Flap suppression: cooldown + window** · 抗抖动: 冷却期 + 抖动窗口
  `OfflineCooldown` (default 90 s) with `CooldownReleaseRSSI` (default -65 dBm)
  covers long-duration weak-signal thrashing; `FlapSuppressionWindow` (default
  30 s) covers short-time same-kind flapping. Cooldown is refreshed on every
  suppress so devices stay hidden until signal recovers.
  (`watcher.go:emitConnectEvent`, `shouldSuppressFlap`, `diff`)

- **`DecisionHandler` observability** · 决策回调可观测性
  16 `DecisionKind` branches expose the full internal decision chain
  (`CONNECT_HINT`, `CONNECT_EMIT`, `COOLDOWN_SUPPRESS_*`, `FLAP_SUPPRESS_*`,
  `POLL_SLEEP_PROTECT`, `POLL_WEAK_MISS`, `POLL_ARP_FAILED`, `POLL_MISSES_EXHAUSTED`,
  `DISCONNECT_PING_OK`, `OFFLINE_EMIT`, …). Zero-cost when no handler registered
  (no allocations, no `time.Now()` call).
  (`decision.go`)

- **Syslog consumer concurrency cap** · 系统日志消费者并发上限
  Semaphore of 16 bounds goroutines spawned from `syslogHints`; 256-element
  buffered channel with atomic `droppedHints` counter and 30 s aggregated
  `onError` reporting under burst.
  (`watcher.go:runSyslogConsumer`, `runSyslog`)

- **Hint cache with 5 s TTL** · 5 秒 TTL 的 hints 缓存
  `loadHints` memoizes `/tmp/dhcp.leases` + `ip neigh show` output to avoid
  per-hint forks during WiFi handshake bursts.
  (`enrich.go`)

- **`RenderTable` formatter** · 表格输出
  Human-readable CLI table for `[]Device` with Chinese labels.
  (`format.go`)

- **`SetupLocalTimezone`** · 路由器本机时区解析
  Parses `/etc/TZ` (e.g. `CST-8`) into `time.Local` so syslog timestamps match
  the router's wall clock.
  (`timezone.go`)

- **Reference CLI `argusd`** · 参考命令行 `argusd`
  Prints device table on start, then streams live events + decisions.
  (`cmd/argusd/main.go`)

- **GitHub Actions CI/release pipeline** · GitHub Actions CI/发布流程
  `ci.yml` runs `go vet` + `go test -race` and cross-compiles 5 targets on
  every push/PR. `release.yml` triggers on `v*.*.*` tag push and publishes
  a GitHub Release with binaries for 10 OpenWrt-relevant targets (`amd64`,
  `386`, `arm64`, `armv5`, `armv7`, `mips/mipsle softfloat`,
  `mips64/mips64le softfloat`, `riscv64`) plus aggregated `SHA256SUMS`.
  (`.github/workflows/ci.yml`, `.github/workflows/release.yml`)

- **Bilingual documentation** · 双语文档
  `README.md` (overview + API), `ONLINE.md` (online decision deep-dive),
  `OFFLINE.md` (offline + cooldown analysis), `CONTRIBUTING.md`.

### Security · 安全

- **IP input validated twice** · IP 双重校验
  `ICMPProber.Reachable` validates IPs with regex `^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}$`
  AND `net.ParseIP`, blocking command injection into `ping`.
  (`prober.go`)
- **Hostapd interface whitelist** · hostapd 接口白名单
  `HostapdFetcher` only accepts interfaces discovered through `ubus list`
  (prefix `hostapd.`), preventing arbitrary service names in shell args.
  (`detect.go`, `hostapd.go`)

### Known limitations · 已知限制

- Stock OpenWrt 23.05+ `hostapd.*` path is implemented but not yet tested on
  real hardware; MediaTek MT7981 vendor firmware (`ahsapd`) is the reference
  target.
- IPv6-only devices are not yet tracked (ARP/DHCP-v4 sources only).
- `argusd --version` flag not yet wired (planned for a later release; the
  linker does set `main.version` via `-ldflags=-X`).

---

<!--
Link references (kept at the bottom for readability).
-->

[Unreleased]: https://github.com/xxl6097/argusd/compare/v0.6.0...HEAD
[0.6.0]: https://github.com/xxl6097/argusd/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/xxl6097/argusd/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/xxl6097/argusd/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/xxl6097/argusd/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/xxl6097/argusd/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/xxl6097/argusd/releases/tag/v0.1.0
