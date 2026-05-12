# Contributing to Argus

Thanks for your interest! Argus is a focused library — contributions should match its scope: real-time device presence detection on OpenWrt.

## Quick Checks

Before opening a PR, make sure the following pass locally:

```bash
go vet ./...
go test -race ./...
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./cmd/argusd
```

## What makes a good PR

- **One concern per PR.** Refactors, bug fixes, and new features get separate reviews.
- **Add tests.** If you fix a bug, write a test that fails before your fix. If you add a feature, cover its happy path and edge cases.
- **Keep code comments in Chinese.** The project uses Chinese for internal design notes; public API documentation is bilingual (Chinese + English).
- **Match existing style.** Run `go fmt` and keep line widths sensible.
- **Update docs.** If you change user-visible behavior, update `README.md`, `ONLINE.md`, or `OFFLINE.md` as appropriate.
- **Update `CHANGELOG.md`.** Every user-visible change must add an entry under the `[Unreleased]` section, classified as `Added` / `Changed` / `Deprecated` / `Removed` / `Fixed` / `Security`. Purely internal refactors that don't change behavior may be omitted.

## Release process (maintainers)

Argus follows [Keep a Changelog](https://keepachangelog.com/) + [Semantic Versioning](https://semver.org/). To cut a release:

1. Edit `CHANGELOG.md`:
   - Move the accumulated `[Unreleased]` bullets into a new `[X.Y.Z] - YYYY-MM-DD` section.
   - Keep the empty `[Unreleased]` header at the top.
   - Update the link references at the bottom of the file.
2. Commit the CHANGELOG edit: `docs: release vX.Y.Z`.
3. Tag and push:
   ```bash
   git tag -a vX.Y.Z -m "vX.Y.Z"
   git push origin vX.Y.Z
   ```
4. The `release.yml` workflow cross-compiles 10 OpenWrt-relevant targets, uploads tarballs + `SHA256SUMS`, and creates the GitHub Release with auto-generated notes. The CHANGELOG entry is the human-authored summary; the auto-generated notes serve as the commit-level detail.

Version bump guidance:
- **Major (X)** — breaking changes to `Event` / `Device` / `Config` / `Option` public API
- **Minor (Y)** — backwards-compatible new features, new config fields (zero-value = old behavior)
- **Patch (Z)** — bug fixes, doc updates, internal refactors

## Release cadence & LTS policy

- **Cadence**: minor releases ship **ad hoc** when a cohesive theme is ready (e.g. v0.5.0 "lifecycle", v0.7.0 "portability + observability"). There is no fixed monthly cycle. Patch releases ship whenever a bug fix is ready.
- **Supported Go versions**: the current Go release plus the **two preceding minor versions** (N-2). Argus 0.8.x supports Go 1.21 – 1.25. This is enforced by a CI matrix.
- **LTS for Argus itself** (after v1.0):
  - The current minor line receives bug fixes and security patches.
  - The previous minor line receives **security-only** fixes for 6 months after the next minor ships.
  - Pre-v1.0 (the 0.x line) has no LTS — upgrade to the latest 0.x for fixes.
- **Breaking changes** after v1.0 ship only in a `v2` module path (`github.com/xxl6097/argusd/v2`). See `STABILITY.md`.
- **Deprecations** give at least one full minor cycle (e.g. if deprecated in 1.3, earliest removal candidate is 2.0) and appear in `MIGRATION.md`.

## Security

Report vulnerabilities privately per [`SECURITY.md`](./SECURITY.md) — do not open a public issue. Acknowledgement within 72 hours, triage within 7 days, fix published within 30 days for high/critical severity.

## Testing on a real router

Argus is designed for OpenWrt; many features can't be fully exercised in unit tests alone. If you have a router handy:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
    go build -trimpath -ldflags="-s -w" -o argusd ./cmd/argusd
scp argusd root@<router-ip>:/tmp/
ssh root@<router-ip> '/tmp/argusd'
```

Report in your PR description:
- router model + firmware
- which `Fetcher` was auto-detected
- sample event log over 5+ minutes

## What we won't accept

- Breaking changes to `Event` / `Device` / `EventKind` without a deprecation path
- Adding `cgo` dependencies (kills cross-compilation simplicity)
- Adding third-party dependencies without strong justification (we stay pure stdlib)
- New features without tests

## Issues vs. PRs

If you're unsure whether a change will be accepted, open an issue first to discuss. For bug reports, please include:

- Router model + OpenWrt / vendor firmware version
- `ubus list` output (trimmed)
- A few relevant lines from `logread` when the issue occurred
- Argus log output with `--verbose` decision trace if possible

## Code of Conduct

All contributors are expected to follow the [Code of Conduct](./CODE_OF_CONDUCT.md).
Be kind. Be concise. Assume good faith.

---

Questions? Open a discussion or ping [@xxl6097](https://github.com/xxl6097).
