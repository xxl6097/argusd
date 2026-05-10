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

[Unreleased]: https://github.com/xxl6097/argusd/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/xxl6097/argusd/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/xxl6097/argusd/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/xxl6097/argusd/releases/tag/v0.1.0
