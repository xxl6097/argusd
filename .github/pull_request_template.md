<!--
Thanks for contributing to Argus! Please fill out the sections below.
A PR that changes the Stable public surface (see STABILITY.md) requires
prior discussion in an issue or Discussion thread — do NOT rename or
remove symbols listed there without a v2 plan.
-->

## Summary

<!-- One or two sentences: what and why. -->

## Type of change

- [ ] Bug fix (non-breaking change that fixes an issue)
- [ ] New feature (non-breaking change that adds functionality)
- [ ] Performance improvement (no externally visible behavior change)
- [ ] Documentation / example only
- [ ] CI / build / release tooling
- [ ] Breaking change (requires `v2` module path — see STABILITY.md)

## Test plan

- [ ] `go test -race ./...` passes
- [ ] `go vet ./...` clean
- [ ] Added / updated tests for the change
- [ ] If touching the hot decision path: `go test -bench=BenchmarkEmitDecisionNil` still reports 0 allocs
- [ ] If changing JSON / Config fields: `STABILITY.md` JSON serialization section updated

## Stability impact

- [ ] No change to Stable public surface
- [ ] Additive only (new symbol, new field with omitempty, new `DecisionKind` / `EventKind` constant)
- [ ] Touches Stable surface (**requires** `MIGRATION.md` note + discussion link)

## Checklist

- [ ] I read `CONTRIBUTING.md`
- [ ] Commit message follows the convention used in recent commits (`feat:` / `fix:` / `docs:` / `perf:` / `ci:` / `refactor:`)
- [ ] Updated `CHANGELOG.md` under `[Unreleased]` if the change is user-visible
