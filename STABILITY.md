# Stability & Compatibility

This document defines Argus's API stability guarantees. It's the answer to "can I safely pin `v0.x` in production?"

**EN** — Argus follows [Semantic Versioning](https://semver.org/). 0.x releases are **minor-zero stable**: no breaking change to listed "Stable" surface within a `0.x.y` line. Breaking changes to Stable surface ship only in `0.(x+1).0` with a clear migration note in [`CHANGELOG.md`](./CHANGELOG.md).

**中文** — Argus 遵循 [语义化版本](https://semver.org/lang/zh-CN/)。0.x 版本承诺 **minor-zero 稳定**:同一 `0.x.y` 线内不会对下文列出的"Stable surface"做破坏性变更。破坏性变更仅在 `0.(x+1).0` 发布, 并在 [`CHANGELOG.md`](./CHANGELOG.md) 的对应版本节给出迁移说明。

---

## Stable public surface (稳定 API · 不会破坏)

Types and functions used by **library consumers** — these must be preserved across patch/minor releases in the same 0.x line:

### Types

- `Event` / `EventKind` / `Change`
- `Device`
- `Config` — *new fields may be added with zero-value-preserves-default semantics*
- `Watcher` — method signatures
- `Option`, `EventHandler`, `ErrorHandler`, `DecisionHandler`
- `Hint` — single MAC's `{IP, Hostname}` enrichment payload
- `HintSource` interface — injectable enrichment source (see `WithHintSource`)
- `DefaultHintSource` — struct with configurable `LeasesPath` / `ARPCommand` / `CacheTTL`
- Sentinel errors: `ErrHandlerRequired`, `ErrInvalidConfig`, `ErrNoFetcher`, `ErrFetchFailed`, `ErrAlreadyRunning`

### Constructors / constructor-like

- `New(opts ...Option) *Watcher`
- `DefaultConfig() Config`
- `Config.Validate() error`
- All `WithXxx` / `OnXxx` options (including `WithHintSource` since v0.7.0)

### Watcher methods

- `(*Watcher).Run(ctx, onEvent, onError) error`
- `(*Watcher).Stop(stopCtx) error`
- `(*Watcher).List(ctx) ([]Device, error)`
- `(*Watcher).EnsureFetcher(ctx) error`
- `(*Watcher).FetcherKind() FetcherKind`
- `(*Watcher).Known() map[string]Device`

### Utilities

- `RenderTable([]Device) string`
- `TableHeader() (header, separator string)`
- `(Device).Row() / .String() / .Wired()`
- `DetectLocalLocation() *time.Location`
- `EventKind.String() / .Label()`
- Subpackage `argustest` — all exported names: `FixedFetcher{Devices, Err}` / `FakeProber{Reach, AllReachable}` and their methods. Intended for consumer unit tests.
- Subpackage `argusmetrics` (stable from v0.7.0) — zero-dependency in-process counters:
  - `argusmetrics.New() *Counters`
  - `(*Counters).OnDecision(Decision)` — satisfies `argus.DecisionHandler`; hot path is 1.7 ns/op, 0 allocs
  - `(*Counters).OnEvent(Event)`
  - `(*Counters).Snapshot() map[string]uint64` — stable keys: `DecisionKind.String()` values + `EVENT_` prefix for event counts
  - `(*Counters).Reset()`

### JSON serialization (stable from v0.6.0)

The following JSON field names are part of the Stable public surface — downstream consumers can safely use them as Kafka / webhook / database column names:

- `Event`: `time` / `kind` / `device` / `changes`
- `EventKind`: marshaled as the English `String()` (`"ONLINE"` / `"OFFLINE"` / `"CHANGE"`). `UnmarshalJSON` also accepts the legacy integer form for backward compatibility.
- `Device`: `mac` / `ip` / `hostname` / `vendor` / `type` / `radio` / `ssid` / `channel` / `rssi` / `uptime_ns` / `access_time` / `last_seen`
- `Change`: `field` / `old` / `new`
- `Decision`: `time` / `kind` / `mac` / `detail`. `DecisionKind` marshals to the English `String()` too.
- `Config`: `poll_interval` / `offline_misses` / `fetch_timeout` / `offline_cooldown` / `cooldown_release_rssi` / `weak_rssi` / `extremely_weak_rssi` / `weak_miss_threshold` / `extremely_weak_miss_threshold` / `flap_suppression_window` / `disable_cooldown` / `disable_flap_suppression`. Durations in nanoseconds (Go's default). `omitempty` preserved on all fields so sparse config files work.

New fields on `Event` / `Device` / `Decision` / `Config` may be added in future minor releases (with `omitempty` so old consumers don't break). Existing fields will not be renamed or removed in the 0.x line.

### Behavioral guarantees

- `EventHandler` / `ErrorHandler` / `DecisionHandler` are called synchronously but with **panic isolation**: a panic in user code is caught, reported via `onError` (for `EventHandler`) or swallowed (for `ErrorHandler`/`DecisionHandler`), and never kills any goroutine.
- When `DecisionHandler` is not registered, `emitDecision` is **zero-cost**: no allocations, no `time.Now()` call. Backed by `BenchmarkEmitDecisionNil` (≤ 2 ns/op, 0 allocs).
- `Run` validates `Config` at entry (`ErrInvalidConfig`) and surfaces baseline-fetch failures via `ErrFetchFailed`.
- `Run` can be called multiple times on the same `Watcher` (subject to `ErrAlreadyRunning` when one is already active). After `Stop` returns, a new `Run` reuses preserved state (`known` / `offlineCooldown` / `lastEventAt` / detected `Fetcher`) but resets transient state (`misses` / `disconnectInFlight` / `syslogHints` / `droppedHints`).

### Context cancellation contract (stable from v0.8.0)

Every entry point that takes a `context.Context` follows these rules — this table is a formal part of the Stable surface:

| Entry point | `ctx.Done()` fires mid-call | `ctx` already cancelled at call time | On return |
|---|---|---|---|
| `(*Watcher).List(ctx)` | returns the wrapped ctx error from the underlying `Fetcher.Fetch` (in-tree fetchers propagate via `fmt.Errorf("...: %w", err)`); no background goroutine spawned | same — `exec.CommandContext` / `ubus` call aborts immediately | caller observes error, no Watcher state changed |
| `(*Watcher).EnsureFetcher(ctx)` | returns `ErrNoFetcher` wrapping `ctx.Err()` **if detection was in progress**; a subsequent call *does* retry (the `sync.Once` only records success) | same as mid-call | on success, detected `Fetcher` / `detectKind` cached for the Watcher's lifetime |
| `(*Watcher).Run(ctx, onEvent, onError)` | returns **`nil`** (not `ctx.Err()`) after in-flight decisions flush and all spawned goroutines exit via `runWG.Wait()`; matches `http.Server.Shutdown` convention | returns `nil` immediately after baseline fetch completes or is aborted | `running` flag cleared; `runCancel` nulled; restart is safe |
| `(*Watcher).Stop(stopCtx)` | — (Stop cancels the Run's internal ctx; it only observes `stopCtx` for the wait deadline) | returns `stopCtx.Err()` immediately; Run goroutines still exit in the background | `running` stays `false`; a follow-up `Stop` returns `nil` (idempotent) |
| `HintSource.Hints(ctx)` | `DefaultHintSource`: aborts partial read, returns whatever was assembled up to that point (can be empty) | `DefaultHintSource`: returns empty map without reading | no side effects beyond the 5 s cache refresh |
| `Fetcher.Fetch(ctx)` | all in-tree impls (`AhsapdFetcher`, `HostapdFetcher`) propagate `ctx.Err()` via `fmt.Errorf("...: %w", err)` | same | `exec.CommandContext` kills subprocess with SIGKILL |

**Key invariants** (tested in `context_contract_test.go`):

- **Run exit on `ctx.Done()` is not an error.** `Run` returns `nil`, not `ctx.Err()` — this lets consumers match `err != nil` as "terminal failure" without also matching graceful shutdown. Only `ErrHandlerRequired` / `ErrInvalidConfig` / `ErrNoFetcher` / `ErrFetchFailed` / `ErrAlreadyRunning` from the **validation / baseline** phase are returned as non-nil.
- **Stop always waits for in-flight decisions to flush** before returning `nil`. If `stopCtx` expires first, Stop returns `stopCtx.Err()` but the workers still exit in the background (the `runWG.Wait` goroutine runs to completion inside a fresh goroutine) — they never leak.
- **Run + Stop concurrency is safe.** Calling `Stop` on one goroutine while another is blocked in `Run` cancels Run's internal ctx; Run returns `nil`; Stop's `runWG.Wait` completes.
- **Nil ctx is a programming error, not a supported input.** Entry points do NOT silently fall back to `context.Background()`; `exec.CommandContext(nil, ...)` / `time.NewTicker` inside `Run` will panic. This matches the stdlib convention.

---

## Evolving surface (演进中 · 可能微调)

These are exported but still shape-shifting. Consumers should only depend on them loosely:

- `DecisionKind` / `SyslogKind` / `EventKind` **integer const values** (1, 2, 3, … 42) may be renumbered between 0.x minor releases. The `String()` / `Label()` / JSON outputs are the stable identifiers — use those for logging/serialization.
- Internal branch coverage (e.g. adding new `DecisionKind` constants) is a **minor** bump, not breaking.
- `Fetcher` / `Prober` interface methods are unlikely to change, but concrete struct field additions in `AhsapdFetcher` / `HostapdFetcher` / `ICMPProber` are allowed.

---

## Unstable / internal (不稳定 · 不要依赖)

- Any package-unexported identifier (lowercase).
- Any function in `cmd/argusd` — it's a reference CLI, not part of the library API.
- In-package test fixtures (`staticFetcher`, etc.). Use `argustest` subpackage instead.
- Decision log string formats.

---

## Deprecated

Functions marked `// Deprecated:` will emit an IDE warning and pkg.go.dev banner, but **will not be removed** in the 0.x line. Current deprecations:

- `SetupLocalTimezone()` — mutates `time.Local` (library anti-pattern). Use `DetectLocalLocation()` and format with `t.In(loc)` instead. Deprecated in 0.3.0.

---

## Path to v1.0 — criteria met (soak pending)

As of v0.8.0, all criteria for cutting v1.0 are satisfied. The tag is
held until the maintainer decides the soak window has been long
enough — v1.0 locks the Stable public surface under SemVer v1 rules,
so breaking changes afterward require a `v2` module path.

1. ✅ No breaking change to any item in the "Stable public surface" list across 0.3 → 0.8 (six releases)
2. ✅ `go test -race ./...` passing on every tagged release; multi-version matrix (Go 1.21 – 1.25) since v0.8.0
3. ✅ `go vet ./...` clean
4. ✅ No unresolved `Deprecated` entries with removal intent
5. ✅ Runtime `panic` caught in tests (library never propagates panic to user goroutines) — see `panic_test.go`
6. ✅ `pkg.go.dev` godoc page renders all types; 10+ runnable `Example` functions cover the high-traffic entry points
7. ✅ All exported symbols have godoc comments
8. ✅ Lifecycle: `Stop` + restart supported (v0.5.0)
9. ✅ Portability: `HintSource` abstraction (v0.7.0)
10. ✅ Observability: zero-dependency `argusmetrics` subpackage (v0.7.0)
11. ✅ JSON serialization contract (v0.6.0)
12. ✅ Consumer test fixtures: `argustest` subpackage (v0.6.0)
13. ✅ Context cancellation contract documented + tested (v0.8.0)
14. ✅ Multi-Go-version CI matrix (Go 1.21 – 1.25, N-2 policy) (v0.8.0)
15. ✅ Security policy + maintenance signals ([`SECURITY.md`](./SECURITY.md), [`CODE_OF_CONDUCT.md`](./CODE_OF_CONDUCT.md), issue/PR templates) (v0.8.0)

**Post-v1.0 policy**: any breaking change to Stable surface requires a
`v2` module path (`github.com/xxl6097/argus/v2`). Additions (new
symbols, new `Config` fields with zero-value-preserves-default, new
`DecisionKind` / `EventKind` constants) continue to ship as minor
bumps and are non-breaking.

See [`MIGRATION.md`](./MIGRATION.md) for per-release upgrade notes.

---

## What NOT a breaking change looks like

The following are **always** considered non-breaking and may ship in a patch (`0.x.y+1`):

- Adding a new exported type, function, method, option, error, field
- Adding a new `Config` field (with zero-value-preserves-default semantics)
- Adding a new `DecisionKind` constant (above the current max value)
- Adding new `DecisionKind` traces at existing decision points
- Adding a new `EventKind` constant (subject to the "three Kinds: Online/Offline/Change" principle — a 4th kind would be minor, not patch)
- Improving accuracy of online/offline detection (e.g. tightening a threshold's default)
- Performance improvements with identical externally visible behavior
- Documentation and example updates

---

## Questions?

Open an issue with the `api-stability` label. Discussion before a breaking change is strongly preferred over after.
