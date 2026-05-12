# Security Policy

## Supported versions

| Version | Status | Security fixes |
|---|---|---|
| 1.0.x | ✅ active (current) | yes |
| 0.15.x | ✅ active (prior line, supported through 2026-11-12) | yes |
| ≤ 0.14.x | 🔴 end-of-life — upgrade to 1.0.x or 0.15.x | no |

The 1.x line is the stable support target going forward. The preceding
0.15.x line continues to receive security fixes for 6 months after v1.0
(through 2026-11-12) so consumers who can't re-vendor immediately have
a runway. Pre-v1.0 releases outside 0.15.x are end-of-life.

## Reporting a vulnerability

**Do not** open a public GitHub issue for security reports.

Please email the maintainer at **xxl6097@gmail.com** (or open a private
[security advisory](https://github.com/xxl6097/argusd/security/advisories/new)
on GitHub) with:

- Affected version(s) / commit hash
- Minimal reproducer
- Your disclosure timeline expectation

Expected response:

- **Acknowledgement**: within 72 hours
- **Triaged severity + initial assessment**: within 7 days
- **Fix or mitigation published**: within 30 days for high/critical, 90 days for low/medium

Credit will be attributed in `CHANGELOG.md` under the affected release
unless you request otherwise.

## Scope

In scope:
- Library code: everything outside `cmd/argusd`
- `argusd` CLI binary as shipped in GitHub Releases

Out of scope (please report upstream):
- OpenWrt / `ubus` / `logread` / `dnsmasq` vulnerabilities
- Go standard library or toolchain issues
- Issues in downstream consumers

## Threat model

Argus is a **local-network read-only observer** — it:

- Reads `ubus`, `/tmp/dhcp.leases`, `ip neigh`, syslog, and ICMP
- Writes nothing to the router (apart from stdout log lines)
- Makes no outbound network calls
- Does not touch authentication, ACLs, or firewall state

The primary risk surface is therefore **parsing untrusted router output**
(malformed syslog lines, DHCP lease entries, hostapd replies). Panics in
parsing paths are caught by the library's `safeInvoke*` wrappers and
surfaced via `ErrorHandler` — they never crash the host process.

Dependencies: Argus ships zero third-party dependencies beyond the Go
standard library. `go mod tidy` must not introduce any.
