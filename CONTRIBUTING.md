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

Be kind. Be concise. Assume good faith.

---

Questions? Open a discussion or ping [@uuxia](https://github.com/uuxia).
