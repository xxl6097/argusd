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
- Sentinel errors: `ErrHandlerRequired`, `ErrInvalidConfig`, `ErrNoFetcher`, `ErrFetchFailed`

### Constructors / constructor-like

- `New(opts ...Option) *Watcher`
- `DefaultConfig() Config`
- `Config.Validate() error`
- All `WithXxx` / `OnXxx` options

### Watcher methods

- `(*Watcher).Run(ctx, onEvent, onError) error`
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

### Behavioral guarantees

- `EventHandler` / `ErrorHandler` / `DecisionHandler` are called synchronously but with **panic isolation**: a panic in user code is caught, reported via `onError` (for `EventHandler`) or swallowed (for `ErrorHandler`/`DecisionHandler`), and never kills any goroutine.
- When `DecisionHandler` is not registered, `emitDecision` is **zero-cost**: no allocations, no `time.Now()` call. Backed by `BenchmarkEmitDecisionNil` (≤ 2 ns/op, 0 allocs).
- `Run` validates `Config` at entry (`ErrInvalidConfig`) and surfaces baseline-fetch failures via `ErrFetchFailed`.

---

## Evolving surface (演进中 · 可能微调)

These are exported but still shape-shifting. Consumers should only depend on them loosely:

- `DecisionKind` const **values** (1, 2, 3, … 42) may be renumbered between 0.x minor releases. The `String()` / `Label()` outputs are the stable identifiers — use those for logging/serialization.
- `SyslogKind` — same caveat.
- Internal branch coverage (e.g. adding new `DecisionKind` constants) is a **minor** bump, not breaking.
- `Fetcher` / `Prober` interface methods are unlikely to change, but concrete struct field additions in `AhsapdFetcher` / `HostapdFetcher` / `ICMPProber` are allowed.

---

## Unstable / internal (不稳定 · 不要依赖)

- Any package-unexported identifier (lowercase).
- Any function in `cmd/argusd` — it's a reference CLI, not part of the library API.
- Test fixtures (`staticFetcher`, etc.).
- Decision log string formats.

---

## Deprecated

Functions marked `// Deprecated:` will emit an IDE warning and pkg.go.dev banner, but **will not be removed** in the 0.x line. Current deprecations:

- `SetupLocalTimezone()` — mutates `time.Local` (library anti-pattern). Use `DetectLocalLocation()` and format with `t.In(loc)` instead. Deprecated in 0.3.0.

---

## Path to v1.0 (进阶为 v1.0 的条件)

Argus will ship `v1.0.0` once **all** of the following hold for **3 consecutive months** on the `main` branch:

1. ✅ No breaking change to any item in the "Stable public surface" list
2. ✅ `go test -race ./...` passing on every commit
3. ✅ `go vet ./...` clean
4. ✅ No unresolved `Deprecated` entries with removal intent
5. ✅ Runtime `panic` caught in tests (library never propagates panic to user goroutines)
6. ✅ `pkg.go.dev` godoc page renders all types and at least one runnable `Example` per high-traffic entry point
7. ✅ All exported symbols have godoc comments

After v1.0, any breaking change requires a `v2` module path. Before v1.0, breaking changes are permitted but each `0.x.0` release must ship a migration note.

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
