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

## [1.2.5] - 2026-05-19

**Fix: WiFi clients on mainline OpenWrt (kernel 6.6+) showed up as `wired` with empty Radio/SSID/RSSI.**

Live repro on 504 router (Linux 6.6.93 / OpenWrt mainline / hostapd
double-radio `phy0-ap0` + `phy1-ap0`): all 8 WiFi devices reported by
`iwinfo` and `ubus call hostapd.phy0-ap0 get_clients` were rendered as
有线 (wired) in argusd output, with no signal / radio / SSID.

Two independent bugs combined to defeat the hostapd path:

### Fixed · 修复

1. **`hostapdServiceRe` regex didn't accept dashes.** It was
   `^hostapd\.\w+$` (`\w` = `[A-Za-z0-9_]`), but mainline OpenWrt
   names interfaces like `hostapd.phy0-ap0` with a dash. Both
   `listHostapdInterfaces` and the per-iface guard inside
   `Fetch` would silently filter those out, returning an empty
   client list. Widened to `^hostapd\.[\w-]+$`.

2. **Strict `Authorized=true` filter rejected current OpenWrt clients.**
   Code was `if !(c.Assoc && c.Authorized) { continue }`. The
   `authorized` field is no longer present in mainline OpenWrt
   hostapd's `get_clients` JSON (only `assoc` + `signal` etc.);
   default zero value `false` skipped every client. `assoc=true`
   alone is sufficient evidence of an active association.

### Verified

After fix on 504:
```
共 9 台设备在线 (WiFi: 8, 有线: 1)
14:D8:81:62:27:C0 ... -32(极强) 2.4G/abala
28:6C:07:FA:31:4F ... -41(极强) 2.4G/abala
60:7E:A4:E8:14:88 ... -57(强)   5G/abala_5G
...
```

Before: `WiFi: 0, 有线: 9` with all-`-` signal.

### Compatibility

- ahsapd path unaffected.
- vendor-firmware hostapd builds that DO emit `authorized` are still
  accepted (no longer required, just ignored).
- No API changes.

---

## [1.2.4] - 2026-05-18

**Fix: stale `logread -f` orphans on Linux when argusd is killed hard.**

Real-world repro on a soak router: `ps -ef | grep logread` showed 7
orphan `logread -f` processes adopted by init (PPid=1) — leftovers
from previous argusd runs that were SIGKILL'd, OOM-killed, or
segfaulted before the `defer cmd.Process.Kill()` could run.

### Fixed · 修复

- `WatchSyslog` now sets `SysProcAttr.Pdeathsig = SIGTERM` and
  `Setpgid = true` on the spawned `logread` child. Linux kernel
  sends SIGTERM to the child the moment its parent dies — fires
  even on SIGKILL / OOM / segfault, where Go-side defer cleanup
  cannot run.
- Cleanup `defer` now uses `syscall.Kill(-pid, SIGKILL)` (negative
  PID = entire process group) when `Setpgid` was applied, so any
  shell-wrapped descendants get cleaned up too.

### Added · 新增

- `reapOrphanedLogreads()` (Linux only): pre-flight at every
  `WatchSyslog` start, scans `/proc/*/status` for processes whose
  cmdline is exactly `logread -f` / `/sbin/logread -f` /
  `/usr/sbin/logread -f` and PPid == 1, sends SIGTERM. Strictly
  matches Go-spawned form to avoid touching vendor logread daemons
  (those use `-F`/`-S`/`-p` flags).
- `safeInvokeErrorStandalone`: panic-isolated ErrorHandler call for
  callers outside the `*Watcher` instance scope.

### Verified

Live test on the soak router (192.168.1.1):
```
=== orphans BEFORE === (10 logread processes, 7 orphans)
=== orphans AFTER (argusd v1.2.4 started) ===
"reaped 7 orphaned logread process(es) from prior run"
(2 vendor argus-app children retained, 1 vendor logread daemon retained, 2 new from this argusd)
```

### Compatibility

- Linux only (uses `/proc` + Pdeathsig). On macOS / Windows the helpers
  are no-ops, so unit tests still build and pass.
- Pure infrastructure fix; no API changes.

---

## [1.2.3] - 2026-05-18

**Fix: dashboard / `Known()` no longer shows offline devices as online during cooldown.**

After v1.2.2 silenced duplicate Offline events, a follow-on bug surfaced
on the live dashboard:

- argusd correctly emit Offline at T=0 (RSSI=-97)
- application layer received the Offline ✓
- but `/api/devices` continued to report the same MAC as `online`
  for the entire 90-second cooldown window — the dashboard never
  transitioned to "offline"

Root cause: in cooldown, the diff and connect-hint paths "silently
re-add the device to `known`" (`known[mac] = d`) on every weak-signal
sighting to refresh the cooldown timestamp. But `Server.handleDevices`
sources its online list directly from `Watcher.Known()`, so the silent
re-add kept the device displayed as online forever.

### Fixed · 修复

- `diff` cooldown branch: refresh `cooldown[mac]` but **do not**
  re-add the device to `known`. The cooldown timestamp still gets
  updated each tick the device shows up in fetch results, so the
  v1.2.x dedup machinery still works correctly.
- `emitConnectEvent` (handleConnectHint): same change — the symmetric
  syslog-driven path also stops repopulating `known` while in
  cooldown.
- `TestDiffCooldownSuppressWeakOnline` updated to assert the new
  contract: in cooldown with weak RSSI, `known` should remain empty.

### Behavioral effect

- Dashboard view becomes consistent with the application layer:
  emit Offline → `/api/devices` reports offline within 1 poll tick.
- Subsequent weak-signal sightings during cooldown extend the cooldown
  but do not flicker the device back to online.
- A genuine signal recovery (`RSSI >= CooldownReleaseRSSI`, default
  -65 dBm) still clears cooldown and emits Online normally.

### Compatibility

- No new exported types or constants; pure internal behavior fix.
- The cooldown-refresh + dedup machinery (v1.2.1 + v1.2.2) is
  unchanged and still functional. This release fixes the third
  symptom of the same edge-signal-iPhone scenario:
  v1.2.1 stopped duplicate emit within the cooldown window;
  v1.2.2 stopped duplicate emit beyond the cooldown window;
  v1.2.3 stops the dashboard from lying about online state.

---

## [1.2.2] - 2026-05-18

**Fix: application layer never sees two Offline events for the same MAC without an Online in between.**

v1.2.1 only suppressed duplicate Offline within the 90s `OfflineCooldown`
window. Real-world repro showed the bug recurring at 8-minute intervals
(MT7981 / iPhone 17, weak-signal sleep cycle):

```
14:09:53 设备离线 RSSI=-97               ← diff path emit, cooldown starts
14:10:26 冷却期抑制上线 (last refresh)    ← cooldown timestamp updated
(7m55s passes; iPhone deep sleep, no fetcher hits, cooldown not refreshed)
                                         ← cooldown naturally expires after 90s
14:18:21 [系统日志] 无线断开 / Del Sta    ← kernel finally drops station
14:18:21 设备离线 RSSI=-96               ← BUG: emit Offline #2
                                         (cooldown 475s ago > 90s window,
                                          v1.2.1 check doesn't fire)
```

The application layer (Webhook / DingTalk / Prometheus) sees **two**
Offline events for the same MAC with **no Online in between** — that
violates the documented event contract. v1.2.2 makes that invariant
hold unconditionally regardless of timing.

### Fixed · 修复

- `handleDisconnectHint`: after the existing v1.2.1 cooldown check, also
  check `lastEventAt[mac]` — if the most recent event emitted to the
  application layer was `EventOffline` (and there's been no Online
  since), short-circuit with `DecisionOfflineDedupedAppLayer` and do
  **not** emit another Offline.
- `diff` (poll path): same check applied at the entry of
  `emitIfNotSuppressed` for `EventOffline`. Covers the symmetric
  scenario where the syslog path missed the disconnect but the
  poll-driven offline detection fires after a long gap.
- `TestDiffFlapSuppressionAllowsAfterWindow` /
  `TestDiffFlapSuppressionDisabled` updated to reflect the new contract
  (post-window reverse events still work; same-kind repeats blocked).

### Added · 新增

- `DecisionOfflineDedupedAppLayer` (kind `29` /
  `"OFFLINE_DEDUPED_APPLAYER"` / "已离线(应用层去重,跳过)") surfaces
  every dedup event for observability.
- `TestDiffNoDuplicateOffline` regression: Offline at T=0, then 8 minutes
  of `diffEmit` calls with the device missing — only one Offline ever
  emitted.

### Compatibility

- Pure addition + behavior fix. No breaking change to the Stable
  surface; one new `DecisionKind` value (29).
- A new Online event still re-arms the dedup state (i.e., after Online,
  the next Offline fires normally). This preserves the symmetric
  Online/Offline alternation contract.
- Affected users: anyone receiving repeated Offline alerts for sleeping
  devices on weak-signal links.

---

## [1.2.1] - 2026-05-18

**Fix: duplicate Offline emitted when an already-offline weak-signal device finally drops off the AP.**

Real-world repro on MT7981 (iPhone 17 in pocket / sleep, RSSI -97):

```
10:03:04 设备离线 82:F8:8D:FB:B6:A6 192.168.1.3 RSSI=-97   ← diff 路径正确报离线
                                                            (5 misses 累计, 进入 90s cooldown)

(8 分钟过去, iPhone 在睡眠中始终保持 -97 dBm 在 AP 关联表里;
 每秒 diff 看到 inCooldown && RSSI < -65 → 静默把设备加回 known)

10:11:34 [系统日志] 无线断开 / MAC表移除   ← 内核终于踢掉 station
10:11:34 设备离线 82:F8:8D:FB:B6:A6 RSSI=-97   ← BUG: 重复 Offline,
                                                cooldown 已过期 / 没被检查
```

`handleDisconnectHint` 没检查 `offlineCooldown`,只看 `known` 是否还在,
而 diff 路径在 cooldown 期间会**反复把弱信号设备加回 known**。结果是
"诉讼判离线"和"内核确认离线"产生两条 Offline 事件,Webhook 被打两次,
告警噪音。

### Fixed · 修复

- `handleDisconnectHint` 入口检测 `offlineCooldown[mac]`:若仍在窗口内,
  静默清理 `known` 但**不再 emit EventOffline**,只发出新的
  `DecisionDisconnectAlreadyOffline` 决策让上层观察到。
- 与现有 `disconnectInFlight` / 弱信号 cooldown 机制兼容;
  `DisableCooldown=true` 时短路检查直接跳过(保留原有"激进上下线"语义)。

### Added · 新增

- `DecisionDisconnectAlreadyOffline` (kind `28` / string
  `"DISCONNECT_ALREADY_OFFLINE"` / label `"断开提示(已离线,跳过)"`)。
- 两个回归测试:
  - `TestHandleDisconnectHintSkipsWhenInCooldown` —— 真实场景
  - `TestHandleDisconnectHintEmitsAfterCooldown` —— cooldown 过期边界

### Compatibility

- 纯加法 + 行为修复,无破坏性变更。
- 老 `DecisionHandler` 看到一个新 kind 可忽略。
- 受影响的用户:**任何启用 OfflineCooldown(默认开)且依赖 syslog hint
  路径触发离线的部署**。Webhook / Prometheus 告警的重复 Offline
  噪音应当大幅降低。

---

## [1.1.0] - 2026-05-14

**Fix: spurious `Offline` for edge-signal devices that bounce off and on the AP within the same second.**

Real-world repro (MT7981 / iPhone 17, RSSI ~ -95 dBm):

```
21:44:22 [系统日志] 无线断开 / MAC表移除 / MAC表新增 / 无线接入 / 认证完成 / DHCP分配
21:44:22 设备离线 (RSSI=-95)        ← spurious Offline emitted by handleDisconnectHint
21:44:24 状态变更 (IP ""→"192.168.1.3") ← poll catches up
21:53:07 设备离线 (RSSI=-98)        ← real disconnect 9 minutes later, but state is wrong
```

The `Disconnect` hint fires first and immediately calls `emitOffline`,
deleting the device from `known`. The follow-up `MAC-add / auth-done`
hints then take the "new device → cooldown weak-signal" path and get
silently absorbed, leaving the watcher in an inconsistent
"offline-but-still-using-IP" state until the next real lifecycle event.

### Added · 新增

- `Config.OfflineRevertWindow time.Duration` — when a connect hint
  arrives within this window after a freshly-emitted Offline, treat the
  Offline as a false positive: clear cooldown so the upcoming Online
  passes the weak-signal gate. **Default: 5 s. Set 0 to disable.**
  Backwards-compatible: zero value in user config preserves the default.
- `DecisionOfflineReverted` (kind `27` / string `"OFFLINE_REVERTED"` /
  label `"撤销离线(立即重连)"`) surfaces every revert event so consumers
  can collapse the Offline+Online pair into a single RECONNECT visually.
  All three encodings (integer, English string, Chinese label) follow
  the existing DecisionKind contract.
- `Config.Validate` rejects negative `OfflineRevertWindow` (returns
  `*ConfigError`).
- Three new regression tests in `watcher_test.go`:
  - `TestHandleConnectHintRevertsRecentOffline` — happy path
  - `TestHandleConnectHintNoRevertWhenCooldownOld` — beyond window
  - `TestHandleConnectHintRevertDisabled` — `OfflineRevertWindow=0`

### Compatibility

- Wire-level addition only. Existing JSON shapes unchanged. Old
  `DecisionHandler` consumers see one new kind they can ignore.
- No breaking change to v1.0 Stable surface; this is a v1.1.0 minor
  bump per the SemVer v1 rules in [`STABILITY.md`](./STABILITY.md).

---

## [1.0.0] - 2026-05-12

**Milestone release — Stable public surface locked under SemVer v1 rules.**

All v1.0 criteria in [`STABILITY.md`](./STABILITY.md) have been satisfied and
soak-tested across v0.3 → v0.15.9 (twelve+ releases, no breaking change to the
Stable surface). v1.0.0 bundles the whole v0.15.x arc into one milestone tag
and opens the official 1.x line.

**Post-v1.0 policy**: breaking changes to the Stable surface require a `v2`
module path (`github.com/xxl6097/argusd/v2`). Additions (new exported
symbols, new `Config` fields with zero-value-preserves-default, new
`EventKind` / `DecisionKind` constants) continue to ship as minor bumps.

### Cumulative highlights (v0.15.4 → v0.15.9)

- **UTF-8 device aliases** — Chinese / spaces / dots accepted, only shell
  metachars banned. Prior regex was whitelist-only and rejected
  "uuxia的iPhone" style names. (v0.15.4)
- **Responsive dashboard layout** — `table-layout:auto` + per-column
  min-widths; columns expand to full content when there's room, truncate
  with ellipsis + hover tooltip only when cramped. 📌 / ✎ buttons stay
  visible at all widths. (v0.15.5 / v0.15.6)
- **IP-conflict one-click replace** — When target IP already belongs to a
  different MAC, the 409 dialog offers a one-click "replace": DELETE old
  reservation, retry POST, atomic from the user's perspective. (v0.15.5)
- **iOS static-IP fix stack** — `dismissTime` 2→30s + ARP cache flush via
  `ip neigh del`. iOS no longer holds a cached lease across reassoc.
  applyReport gained `arp_flushed` field. See
  [`docs/blog/ios-static-ip.md`](./docs/blog/ios-static-ip.md). (v0.15.7)
- **Opt-in WiFi restart** — When per-station kick silently no-ops on vendor
  firmwares (MTK C-Life and similar), a save-dialog checkbox runs
  `wifi reload` / `/etc/init.d/ahsapd restart` so every client re-associates
  within seconds. applyReport gained `wifi_restarted` field. (v0.15.8)
- **System endpoints** — `POST /api/system/restart-network` (soft: 5-15s
  LAN blip, config preserved) and `POST /api/system/reboot` (hard: 30-60s
  full reboot). Both behind `WithWriteAuth`, both surfaced in the
  dashboard header with confirmation prompts. (v0.15.9)

### Added · 新增

- Support-question issue template (third form in `.github/ISSUE_TEMPLATE/`)
  with ubus-list / router-model / DHCP-daemon prompts; first-time
  contributor checkbox.
- `scripts/build-all.sh` — mirrors CI matrix for local multi-arch builds
  (amd64/386/arm64/armv5/armv7/mips/mipsle/mips64/mips64le/riscv64).
- `docs/blog/ios-static-ip.md` — debugging story covering the three
  independent ways "set static IP" can silently fail on iOS + OpenWrt,
  with real logread/tcpdump/ip-neigh excerpts and fix commands.
- README "界面概览 · Screens" section with 5 real dashboard screenshots.

### Changed · 变更

- README tagline rewritten to be SEO-friendly and single-line:
  "Real-time OpenWrt device presence & static-IP dashboard — multi-source
  fusion, sub-second events, zero-dep Web UI".
- README hero image (`docs/images/dashboard-desktop.png`) moved above the
  fold so first-screen visitors see the actual UI.
- Badges consolidated: Go Reference + Go Report Card + Go version +
  License + Tests + Release (sort=semver).

### Migration notes

No code changes required from v0.7.0 onward — the Stable surface has been
frozen since then. `go get -u github.com/xxl6097/argusd@v1.0.0` and
rebuild. See [`MIGRATION.md`](./MIGRATION.md) for per-release upgrade
notes.

---

## [0.15.9] - 2026-05-12

### Added · 新增

- `POST /api/system/reboot` — auth-gated handler that schedules
  `/sbin/reboot` in a detached goroutine after a 500 ms delay so the HTTP
  response has time to flush. Dashboard header button "重启路由器" (red,
  two-step confirmation). Toast: "已下发重启指令, 路由器将在约 30-60 秒
  后恢复, 恢复后手动刷新本页面"。
- `POST /api/system/restart-network` — auth-gated handler that runs
  `/etc/init.d/network restart` in a detached goroutine (20 s ceiling).
  Dashboard header button "重启网络" (neutral, single confirmation).
  Toast: "已下发重启网络指令, 约 5-15 秒后恢复, SSE 会自动重连"。
- `argusweb/system.go` — new file containing both handlers; binaries
  discovered via `exec.LookPath`, returns 503 if unavailable.
- `.hdr-btn` / `.hdr-btn.hdr-danger` CSS classes in `dashboard.html`,
  style matched to existing pill design.

### Fixed · 修复

- Gives users an explicit escape hatch when per-station kick and opt-in
  `wifi reload` are both insufficient (very rare, but observed): a single
  click on "重启网络" forces every client through full DHCP re-negotiation
  without waiting 12 h for lease expiry.

### Scope

Pure addition. Existing `/api/aliases`, `/api/dhcp` handlers and
`applyReport` JSON shape unchanged; old clients unaffected.

---

## [0.15.8] - 2026-05-12

### Added · 新增

- Opt-in WiFi restart in the static-IP save dialog. Checkbox "立即生效
  (重启 WiFi)" attaches `?restart_wifi=1` to `POST /api/dhcp` and
  `DELETE /api/dhcp`; server runs `wifi reload` or `/etc/init.d/ahsapd
  restart` (first that succeeds) after the UCI commit. All WiFi clients
  drop for 3-5 s and auto-reassociate, which forces every device through
  fresh DHCP so the new reservation takes effect immediately.
- `applyReport.WiFiRestarted` JSON field (`omitempty`) exposes the
  command that actually ran.
- Toast message "已重启 WiFi (..), 全部设备将在数秒内重连并拿到新 IP"
  when the checkbox was honored.

### Changed · 变更

- `applyDHCPChanges(ctx, mac)` → `applyDHCPChanges(ctx, mac, restartWiFi)`.
  Only called from two handlers inside the package, so not a breaking
  change to any exported surface.

### Rationale

On MediaTek C-Life vendor firmware, `ubus call ahsapd.roaming
staDisconnect` returns exit 0 but produces no `Del Sta` / `Deauth` kernel
event — the method appears to be a no-op in single-AP deployments. Users
needed a reliable fallback that didn't require them to manually toggle
WiFi on every affected device; the "nuclear" checkbox gives them one.

---

## [0.15.7] - 2026-05-12

### Added · 新增

- `applyDHCPChanges` step 3 (new): `ip neigh del <oldIP> dev <iface>` to
  flush stale ARP entries for the target MAC before kicking the station.
  Without this, iOS in particular re-advertises its cached IP via ARP
  after reassoc and the router happily confirms it, letting iOS ignore
  the DHCPNAK and keep using the old address.
- `applyReport.ARPFlushed` JSON field (`omitempty`) — old IP whose ARP
  entry we deleted. Surfaced in the dashboard toast.
- `flushARPForMAC` helper reads `/proc/net/arp`, matches lowercase MAC,
  deletes every matching entry. Best-effort; errors swallowed.

### Changed · 变更

- `ahsapd.roaming staDisconnect` `dismissTime` raised from 2 to 30
  seconds. Empirically, iOS's DHCP client doesn't tear down its cached
  lease state within 2 s — it reconnects still in RENEWING state and
  re-requests the old IP. 30 s reliably forces a full release + fresh
  DISCOVER on iOS and Android.

### Rationale

User reported "static IP doesn't take effect on iPhone". Reproducer
showed dnsmasq correctly NAK'd the old IP and offered the new one, but
iOS re-requested the old IP with a wrong-server-ID and kept it. Root
cause was iOS aggressive DHCP state caching + router ARP confirmation of
the old mapping. See [`docs/blog/ios-static-ip.md`](./docs/blog/ios-static-ip.md)
for the full debugging writeup.

---

## [0.15.6] - 2026-05-12

### Fixed · 修复

- 📌 static-IP button position: switched `.ip-text` from `flex: 1 1 auto`
  to `flex: 0 1 auto` and added `margin-right: auto` on `.staticip-btn`.
  Button now hugs the IP text instead of being pushed to the far edge of
  the cell; still preserves ellipsis truncation when IP text overflows.

Scope: CSS only, no behavior change.

---

## [0.15.5] - 2026-05-12

### Added · 新增

- **IP-conflict replace prompt.** When `POST /api/dhcp` returns 409, the
  frontend now shows a confirm dialog naming the conflicting owner MAC.
  User clicks "确定" → dashboard auto-runs
  `DELETE /api/dhcp?mac=<owner>` then retries the original POST; "取消"
  leaves both reservations untouched. Toast on success: "已替换"。
- **Responsive table layout.** `table-layout: fixed` → `auto`, with each
  `<col>` given a `min-width` and select columns (status / MAC / RSSI /
  link) additionally given `width: 1%` to collapse to content. Result:
  on a wide screen every column displays full content; on narrow
  screens only the least-important columns truncate with ellipsis +
  hover tooltip.
- Flex container for the IP cell so the trailing 📌 button can never be
  eaten by a long IP string.

### Rationale

Narrow-column fixed widths (150 px / 140 px) were a bad compromise: they
wasted space on 1920px desktops and still truncated on mid-tier phones.
`table-layout: auto` with min-widths matches the user's expectation
better and fixes the "long hostname hides the ✎ button" complaint.

---

## [0.15.4] - 2026-05-12

### Fixed · 修复

- `validateName` for static-DHCP host names rewritten from a whitelist
  regex (`^[A-Za-z0-9_-]{0,63}$`) to a UTF-8 blacklist (bans only shell /
  uci metacharacters: `'"\\` `` ` `` `$;|&<>` and control chars). Chinese
  names, spaces, dots, colons, middle-dot, and other Unicode punctuation
  are now accepted; length still capped at 63 bytes for dnsmasq.
- `TestValidateName` updated to cover the new contract: "uuxia的iPhone",
  "客厅·音箱", "has space", "192.168.x" are all accepted; every shell
  metacharacter individually rejected.

### Rationale

Users with Chinese-speaking households couldn't set meaningful names on
their devices ("iPhone of 小明" was rejected). The old regex was an
over-conservative whitelist; the new blacklist is strictly narrower than
what `uci set key=value` can safely accept, so injection is still blocked
while the legitimate character set expands dramatically.

---

## [0.15.3] - 2026-05-10

**CRITICAL BUG FIX.** User report: after setting a static IP via the
dashboard and rebooting the router, no device could get a DHCP lease
— the entire LAN lost connectivity.

Root cause: v0.15.2's `UCIDHCPManager.Set` had no IP conflict
detection. Two reservations pointing at the same IP (one original
LuCI entry + one argus-written entry) caused odhcpd to refuse the
entire `/etc/config/dhcp` on the next reload, bringing DHCP down
for every device.

This release adds three safeguards so the same mistake is impossible
from now on, plus a recovery endpoint for anyone already in a bad
state.

### Fixed · 修复

- **IP conflict detection in `UCIDHCPManager.Set`**. Before writing
  a new reservation, Set now scans every existing `dhcp.@host[N]`
  and `dhcp.argus_*` entry. If the target IP is already bound to a
  different MAC, Set returns the new typed error
  `*ErrIPAlreadyReserved{IP, OwnerMAC}` without mutating UCI.
  (`argusweb/dhcp.go`)

- **HTTP 409 Conflict** — the POST handler recognizes
  `*ErrIPAlreadyReserved` (via `errors.As`) and returns status
  `409` with body `{"error", "ip", "owner_mac"}`. The dashboard
  surfaces the message so users see exactly which existing
  reservation blocks the write:
  ```
  IP 冲突: 192.168.10.2 已被另一台设备使用
  占用者 MAC: A0:29:42:00:7A:FD

  请换一个 IP,或先移除该设备的静态分配。
  ```

- **FNV-32 hash for UCI section names**. The old `argus_<last-2-MAC-bytes>`
  scheme collided for MACs that happened to share the last two
  bytes (e.g. `aa:bb:cc:dd:ee:97` and `ff:ee:dd:cc:bb:97` both
  mapped to `argus_ee97`). v0.15.3 uses the FNV-32a hash of the
  full MAC, rendered as 6 hex digits — effectively zero collision
  probability at any realistic fleet size.

### Added · 新增

- **`POST /api/dhcp?purge_argus=1`** recovery endpoint. Removes every
  `dhcp.argus_*` section in a single commit without touching
  anonymous `dhcp.@host[N]` entries (which belong to LuCI / the
  user). Write-auth gated. Returns `{"ok": true, "removed": N}`.
  Gives a user who has fallen into an inconsistent state a one-call
  path back to a clean DHCP config, without SSH-ing to the router
  and hand-editing `/etc/config/dhcp`.

- **`(*UCIDHCPManager).PurgeArgusOwned(ctx)`** method backing the
  new endpoint. Direct consumers who embed the type can call it
  programmatically.

- **New exported sentinel / type** `ErrIPAlreadyReserved` struct with
  `IP` and `OwnerMAC` fields, per STABILITY.md.

### Tests

- 6 new regression tests:
  - `TestSetRejectsIPConflict` — different MAC to same IP fails with
    the typed error; offending entry not written
  - `TestSetAllowsUpdateOfOwnReservation` — same MAC + same IP is a
    no-op update, not a conflict
  - `TestDHCPPostReturns409OnConflict` — HTTP status + body shape
  - `TestMACHashSuffixIsStable` — determinism
  - `TestMACHashSuffixAvoidsLastByteCollision` — the specific
    regression the v0.15.3 fix is named after
  - `TestErrIPAlreadyReservedMessage` — error format

### End-to-end UAT (MT7981 / C-Life vendor firmware)

Verified exactly the scenario that caused the outage:
- POST attempting to write `192.168.10.2` for `DE:AD:BE:EF:00:02`
  (while `A0:29:42:00:7A:FD` already owns it) returns **HTTP 409**
  with `owner_mac: "A0:29:42:00:7A:FD"` — UCI is NOT mutated.
- POST with a non-conflicting IP succeeds; UCI section name is
  the new FNV-based `dhcp.argus_2a8f3c` (was `dhcp.argus_8e97`).
- `?purge_argus=1` removes every `argus_*` entry, returns `{ok, removed}`.

### Recovery for anyone affected

If your router was poisoned by a pre-v0.15.3 write:

```bash
# 1. SSH to the router (set a static IP on your laptop first if
#    DHCP is down):
ssh root@192.168.10.1

# 2. Remove all argus-written entries in one shot:
for sec in $(uci -q show dhcp | grep -oE 'dhcp\.argus_[a-z0-9]+' | sort -u); do
    uci -q delete $sec
done
uci commit dhcp

# 3. Restart the DHCP daemon:
/etc/init.d/odhcpd restart   # or dnsmasq restart on mainline OpenWrt
```

After installing v0.15.3 the same cleanup is available via
`curl -X POST 'http://router:9099/api/dhcp?purge_argus=1'`.

---

## [0.15.2] - 2026-05-10

User report: after setting a static IP via the dashboard, the device
kept using its old dynamically-assigned IP for up to 12 hours (the
default DHCP lease time).

Root cause: v0.15.0's post-commit hook was hardcoded to
`/etc/init.d/dnsmasq reload`, which (a) silently no-ops on vendor
firmwares like MTK C-Life that run **odhcpd instead of dnsmasq**,
and (b) on dnsmasq hosts only re-reads config, it does NOT
invalidate existing leases in `/tmp/dhcp.leases`. The reservation
only took effect on the client's next voluntary DHCP renewal.

This release replaces the single reload call with a three-step
"immediate-apply" flow and surfaces what actually happened in the
API response + dashboard toast.

### Changed · 变更

- **`argusweb.UCIDHCPManager` POST/DELETE now performs three
  best-effort steps** after the `uci commit`:

  1. **Reload all known DHCP daemons**. Tries `/etc/init.d/dnsmasq
     reload` and `/etc/init.d/odhcpd reload` in order; skips
     whichever isn't installed (previously only the first was tried).
  2. **Prune the client's lease line** from every known lease file
     (`/tmp/dhcp.leases`, `/tmp/hosts/odhcpd`). Without this, the
     daemon keeps handing out the OLD IP until the client's lease
     expires naturally.
  3. **Kick the WiFi station** via a vendor-specific ubus call
     (`ahsapd.roaming staDisconnect` for MTK C-Life firmware),
     forcing the client to reassociate and send a fresh DHCP
     DISCOVER. Wired clients and devices on firmware without
     staDisconnect keep their old IP until they renew on their own
     schedule (still the old default behavior).

  All three steps are best-effort: any failure is silently
  skipped, and the POST continues to return 200 (the UCI commit
  has already persisted the reservation; immediate-apply is a
  courtesy, not a correctness requirement).

- **`/api/dhcp` response gains an `apply` block**. Both POST and
  DELETE now include:
  ```json
  "apply": {
      "reloaded": ["/etc/init.d/odhcpd"],
      "pruned":   ["/tmp/dhcp.leases"],
      "kicked":   "ubus call"
  }
  ```
  Fields are omitted when empty. Consumers (including the dashboard)
  can show a precise "已生效" vs "等待设备续租" hint instead of
  guessing. Additive; existing `ok`/`mac`/`ip` fields unchanged.

- **Dashboard toast** — after saving or removing a static IP, a
  5-second bottom-anchored toast summarizes what the server did:
  - "已重载: /etc/init.d/odhcpd"
  - "已清除旧租约 (1 个)"
  - "已踢出该设备,正在重连并重新申请 IP"
  - 或提示 "设备需要下次续约后才会拿到新 IP(最长 12 小时)。手动关开 WiFi 可立即生效"

### Added · 新增

- 4 new regression tests covering `pruneLeaseFile`: matching line
  removal, case-insensitive MAC match, missing-file handling,
  no-op when no line matches (preserves mtime so flash doesn't
  churn on routers).

### End-to-end UAT (MT7981 / C-Life vendor firmware)

Verified the exact fix for the reported issue:
- POST `/api/dhcp` → response shows `reloaded=["/etc/init.d/odhcpd"]`
  (dnsmasq script returned "Command failed: Not found" and was
  correctly skipped)
- `pruned=["/tmp/dhcp.leases"]` even when the file is empty (just
  a stat pass, no rewrite)
- `kicked="ubus call"` — `ahsapd.roaming staDisconnect` succeeded
- DELETE also surfaces the same three-step report

### Caveats

- **Not all firmwares support station kick.** Mainline OpenWrt's
  `hostapd.<iface>` ubus methods are a different shape and aren't
  included in v0.15.2's kick list (the device's WiFi driver here
  doesn't expose nl80211 so `iw station del` wouldn't work
  either). When the kick fails, the UI hints at "手动关开 WiFi 可立即生效".
- **Lease pruning requires root write access** to the lease files.
  When argusd runs unprivileged this step silently skips and the
  user sees a longer wait.
- **iOS low-power mode / some IoT devices ignore disconnect events**
  and cache their lease. In those cases the physical WiFi
  toggle is the reliable path.

---

## [0.15.1] - 2026-05-10

Dashboard UX patch: remove the dual-language EN/中文 labels (column
headers, status pills, button text, prompts) and keep Chinese only.
The bilingual headers were eating horizontal space and, on narrow
desktop windows, pushing the Vendor column to wrap. Also fixes
content-squeeze by truncating long cells with ellipsis and a
`title=` tooltip showing the full string on hover.

Library API / `/api/*` wire shape unchanged.

### Changed · 变更

- **Dashboard labels** now Chinese-only:
  - 表头: `状态`, `MAC`, `IP`, `主机名`, `厂商`, `信号`, `类型`
    (was `状态 · Status`, `厂商 · Vendor`, …)
  - 状态 pill: `在线`, `离线 N分钟前`, `重连`, `抖动`, `变更`
  - 连接状态: `已连接`, `重连中…`, `等待事件…`
  - 按钮: `保存 / 取消 / 移除 / 清除`
  - 模态框标题: `静态 IP` (was `静态 IP · Static IP`)
  - 事件行/链路: `有线`, `重命名`, `别名`, `已静态分配`, `设为静态 IP`
  - 顶部标题: `设备监控` (was `设备监控 · Device Monitor`)
  - Footer removed the "local dashboard" subtitle; kept endpoint hints
  (`MAC` / `IP` / `SSE` stay as acronyms — universal; not EN.)

- **Table layout now fixed-width** (`table-layout: fixed`) with a
  `<colgroup>` declaring explicit widths per column (status 90 px,
  MAC 150 px, IP 150 px, host auto-stretch, vendor 140 px, RSSI
  90 px, link 130 px). Long text no longer shoves adjacent columns.

- **Long-cell truncation**: every table `<td>` and mobile
  `.card-row` cell truncates with `text-overflow: ellipsis` on
  a single line. Every cell carries a `title="<full text>"`
  attribute so hovering (desktop) or long-pressing (iOS/Android)
  reveals the full value. Applies to:
  - Device rows: MAC / IP / hostname / vendor / RSSI / link type
  - Event stream: detail column (previously wrapped to two lines
    with `word-break: break-all`; now one-line + tooltip)
  - Mobile cards: MAC, host, link, vendor (4 truncation points)

- **Status pills** on offline rows now also carry the full
  "离线于 <relative time>" in the `title`, so long durations
  aren't cut off.

### Removed · 移除

- The `td.host { word-break: break-all }` rule (superseded by the
  global truncation rule).
- Footer's English subtitle `local dashboard`.

---

## [0.15.0] - 2026-05-10

User request: dashboard should show device vendor and let me set a
static IP from the UI. The first is a column addition; the second is
a new mutating API that touches the router's `/etc/config/dhcp` and
reloads dnsmasq, gated behind the same auth predicate as the alias
write API.

Library API and semantics unchanged. All new code lives in
`argusweb`; the static-IP feature auto-disables when not running on
OpenWrt (no `uci` in `$PATH` or `/etc/config/dhcp` unreadable).

### Added · 新增

- **Vendor column** in the Known Devices list. Desktop table gains
  a "厂商 · Vendor" column populated from `Device.Vendor` (already
  in the library/JSON since v0.6.0). Mobile cards add a third row
  showing "厂商 <vendor>". Wired devices and rows without vendor
  data show "—".

- **`DHCPManager` interface** for static DHCP reservations:
  ```go
  type DHCPManager interface {
      List(ctx) (map[string]StaticLease, error)
      Set(ctx, StaticLease) error
      Delete(ctx, mac string) error
  }
  ```
  Plus `StaticLease{MAC, IP, Name}` struct, `WithDHCPManager(m)`
  Option, and `ErrDHCPManagerUnavailable` sentinel for graceful
  fallback on non-OpenWrt hosts.

- **`UCIDHCPManager`** — OpenWrt implementation:
  - Constructor `NewUCIDHCPManager()` probes `uci show dhcp` and
    returns `ErrDHCPManagerUnavailable` when not on an OpenWrt box
  - Set/Delete are serialized (single internal mutex), apply on
    `/etc/config/dhcp` via uci, then reload dnsmasq
  - New entries created as named sections (`dhcp.argus_<mac-suffix>`)
    so writes are idempotent and don't shift indices when other
    entries are added/removed by LuCI or the user
  - Updates of existing anonymous `dhcp.@host[N]` entries (typically
    created by LuCI before argusd was installed) update in place
    without renaming
  - Defense-in-depth: every mutation is preceded by `uci revert dhcp`
    and reverted on error, so failed POSTs leave no pending state
  - Strict input validation against shell/uci injection: MAC matches
    `aa:bb:cc:dd:ee:ff`, IP via `net.ParseIP` + IPv4 check, name
    `[A-Za-z0-9_-]{0,63}`. Names with spaces or shell metachars are
    rejected with 400.

- **`/api/dhcp` HTTP routes**:
  - `GET /api/dhcp` — list current reservations as
    `{"leases": {MAC(upper): {mac, ip, name}, ...}}`
  - `POST /api/dhcp` `{"mac": "...", "ip": "...", "name": "..."}`
    — create/update a reservation. Empty name auto-generates
    `argus-<mac-suffix>`. Gated by write-auth.
  - `DELETE /api/dhcp?mac=...` — remove a reservation. Gated.
  - `503` when the server was built without `WithDHCPManager`.

- **`/api/devices` capabilities block** — top-level body now
  includes `"capabilities": {"aliases": bool, "dhcp": bool}` so the
  dashboard knows which features to surface (e.g. hide the static-IP
  button on hosts without a DHCP manager).

- **Dashboard static-IP UI**:
  - A 📌 button next to each device's IP opens a modal:
    "静态 IP · Static IP" prefilled with current IP and existing
    reservation name (if any). Save / Remove (when a reservation
    exists) / Cancel buttons. Enter saves; Esc cancels.
  - When a device has a static reservation, its IP cell shows
    🔒 prefix in accent color so you can tell at a glance which
    devices are pinned.
  - The pencil ✎ rename button (v0.14.0) is unaffected — both
    affordances coexist.

- **`argusd` auto-detect** — `argusd -listen=...` now probes for
  `uci` at startup and silently wires `UCIDHCPManager` when
  available; logs `DHCP 静态租约管理已启用 (uci)` on success or
  the detection failure on stderr. Dev laptops see the latter and
  the dashboard hides the 📌 button.

### Tests

- 14 new test cases in `argusweb/dhcp_test.go`:
  - Parser: 4 cases covering multi-host output, incomplete entries,
    unrelated sections, and named sections
  - Validators: MAC / IPv4 / name with explicit bad inputs (shell
    injection, oversized names) — confirms refusal
  - HTTP routes: GET returns leases, POST writes, POST rejects bad
    IP, DELETE removes, 503 without manager, 403 with denying auth
  - `NewUCIDHCPManager` returns wrapped `ErrDHCPManagerUnavailable`
    when `uci` is missing from PATH
  - Capabilities block correctly advertises feature availability

### End-to-end UAT (MT7981 router)

Verified:
- `argusd` auto-enables DHCP management at startup ✅
- POST creates `dhcp.argus_<suffix>=host` entry, commits, reloads
  dnsmasq ✅
- POST same MAC with new name updates in place (no duplicate
  section) ✅
- DELETE removes the section cleanly ✅
- Final `uci show dhcp` matches pre-test state — no leftovers ✅
- `/api/devices` carries `capabilities.dhcp=true` and `vendor`
  column data ✅

### Caveats

- **OpenWrt-specific.** The dashboard's 📌 button is hidden and
  `/api/dhcp` returns 503 on hosts without `uci` (Debian routers,
  pfSense, dev laptops). A user implementing `DHCPManager` against
  another platform's CLI/socket can wire it via `WithDHCPManager`.
- **dnsmasq reload is non-instantaneous.** A new reservation takes
  effect on the device's next DHCP renewal (typically ≤ leasetime
  seconds after the device next requests its lease). Existing
  leases remain bound to the previously-allocated IP until they
  renew.
- **Subnet validation is not performed.** The implementation only
  validates IPv4 syntax, not membership in the LAN subnet. An IP
  outside the configured DHCP pool will be persisted but ignored
  by dnsmasq. Future versions may add subnet checks.

---

## [0.14.0] - 2026-05-10

User request: devices using iOS 15+/Android 10+ "private WiFi
address" show up with their random MAC as both MAC *and* hostname —
you should be able to give them a friendly name in the dashboard and
have it stick.

Purely additive; library API and semantics unchanged.

### Added · 新增

- **Persistent alias store** — `argusweb.NewAliasStore(path string) *AliasStore`
  maintains a MAC → friendly-name map, backed by a JSON file
  (atomic write-tmp + rename). Corrupt files are treated as empty
  and repaired on the next successful write. Methods:
  `Lookup(mac) string`, `Set(mac, name) error` (empty name deletes),
  `All() map[string]string`. Empty-path constructor produces an
  in-memory store (handy for tests).

- **Server options** (all on `argusweb.Server`):
  - `WithAliases(*AliasStore)` — attach a store; `/api/devices` rows
    gain an optional `alias` field, dashboard prefers the alias for
    display
  - `WithWriteAuth(func(*http.Request) bool)` — gate mutating APIs;
    default policy allows loopback and RFC1918 private networks,
    which covers the common `-listen=0.0.0.0:9099` home-LAN case

- **REST endpoints for aliases** — `GET|POST|DELETE /api/aliases`:
  - `GET /api/aliases` → `{"aliases": {MAC(upper): name, ...}}`
  - `POST /api/aliases` `{"mac": "...", "name": "..."}` sets or
    clears (empty name deletes). Gated by write-auth.
  - `DELETE /api/aliases?mac=...` deletes. Gated by write-auth.
  - Without `WithAliases`, all three return `503`.

- **Inline rename in the dashboard** — each row's hostname cell
  shows a ✎ pencil button. Click → inline input → Enter to save
  / Esc to cancel / "清除" clears the alias. The alias is shown in
  accent color, with the original hostname kept as a grey hint in
  parentheses. Works on mobile (card layout) too.

- **`argusd -aliases=<path>`** CLI flag (default
  `/etc/argusd/aliases.json`, empty disables persistence).

- **12 regression tests** covering: case-insensitive lookup,
  empty-name delete, disk persistence across instances, corrupt-file
  recovery, 64-char name limit, `/api/devices` merge, `/api/aliases`
  GET/POST/DELETE, 503 when store unconfigured, 403 when write-auth
  denies, 400 on bad JSON, read-endpoint bypasses auth.

### Documentation

- `STABILITY.md`'s `argusweb` block extends the Stable wire surface
  with the alias field on `/api/devices`, the `/api/aliases` endpoint
  set, and the two new options.

### Caveats

- iOS/Android "private WiFi address" rotates the MAC over time. Aliases
  are keyed by the MAC observed at the time of naming; when the OS
  rotates, the new MAC has no alias until renamed again. This is
  inherent to the privacy feature. Users who want stable names on
  iOS can disable per-network MAC randomization under
  Settings → WiFi → (network name) → Private WiFi Address.
- The JSON store is best-effort: a crash between `rename` and
  `fsync` (on power loss, not normal process exit) can revert the
  last write. Fine for a dashboard affordance; don't treat it as a
  system of record.

---

## [0.13.3] - 2026-05-10

User request: dashboard device list should show an explicit
online/offline status column AND keep offline devices visible
instead of dropping them on disconnect.

### Added · 新增

- **Offline retention in `argusweb`** (opt-in, defaults on):
  `argusweb.Server` now maintains an in-process offline cache fed
  by SSE `EventOffline`/`EventOnline`/`EventChange` events. `/api/devices`
  merges the Watcher's `Known()` (online) with the offline cache
  (recently departed) into one list. Two new `Option`s:
  - `argusweb.WithOfflineRetention(d time.Duration)` — TTL for
    offline entries (default 7 days, zero disables retention)
  - `argusweb.WithOfflineMax(n int)` — soft cap; oldest entry is
    evicted when exceeded (default 512, zero disables the cap)

  Library surface is untouched — this is a dashboard-layer
  concern. `argus.Watcher.Known()` still means "currently online"
  and is unchanged.

- **`/api/devices` wire shape extension**:
  - Top-level body gains `online: N` and `offline: N` counts
  - Each row gains `status: "online" | "offline"` (mandatory) and
    `offline_at_ms` (unix-ms, set when status is "offline")
  - Rows are sorted online-first then alphabetically by MAC so the
    active fleet is always on top
  - Backward-compatible: existing fields are unchanged; the new
    fields are additive

- **Dashboard · Status column** (`argusweb/assets/dashboard.html`):
  - New leftmost column shows a green "在线" pill for online
    devices, a red "离线 N分钟前" pill for offline devices with a
    compact relative-time suffix
  - Offline rows are desaturated (55% opacity) so the eye is
    drawn to the online set first
  - Header count pill split into two: "在线 N" + "离线 N"
  - Mobile cards reshuffle: MAC + status pill on row 1, host/IP
    on row 2, link/radio + RSSI on row 3

- **7 new regression tests** in `argusweb/server_test.go`:
  - `TestDevicesOfflineEventRetainsDevice`
  - `TestDevicesOnlineEventEvictsFromOffline`
  - `TestDevicesOfflineRetentionTTL` — 20 ms TTL
  - `TestDevicesOfflineCapEvictsOldest` — max=2 eviction
  - `TestDevicesChangeEventUpdatesOfflineCacheEntry`
  - `TestDevicesStatusFieldAlwaysPresent`

---

## [0.13.2] - 2026-05-10

Patch release. Fixes the "device keeps flashing online/offline on the
web UI" user report. Two root causes in one cycle:

### Fixed · 修复

- **Library — WiFi reconnects no longer mislabel as wired** — when a
  phone disconnected and reassociated to the same SSID, the
  post-reconnect `Online` event was built from syslog+DHCP only (no
  `ubus` call during the handshake), leaving `Radio` and `SSID` empty.
  `Device.Wired()` returns `true` when `Radio == ""`, so the dashboard
  rendered a transient "有线 wired" badge for a WiFi device, followed
  ~1 s later by an `EventChange` filling in `Radio: "" → "5G"`. The
  net UX was a three-event burst — `OFFLINE → ONLINE (as wired) →
  CHANGE (to WiFi)` — for every single phone reconnect.

  Fix: `Watcher` now retains a `lastShape` map (MAC → last-observed
  `Radio` / `SSID` / `Vendor` / `Type` / `Channel`) that survives
  removal from `known`. `handleConnectHint` seeds the emitted
  `Device` from this cache when available, so the initial `Online`
  already carries the correct wireless fields. The diff poll loop
  refreshes the cache each tick. `WithBaseline` entries are also
  seeded on Run start. No API surface change.

  Added regression test `TestHandleConnectHintPreservesWirelessShape`.
  (`watcher.go`, `watcher_test.go`)

- **Dashboard — reconnect bursts coalesce into one row** — the
  events list now detects same-MAC events within a 10 s window and
  upgrades the existing row in place instead of inserting three
  separate entries:

  | Prev pill | Incoming | Result |
  |---|---|---|
  | OFFLINE | ONLINE | **RECONNECT** · "重连 RECONNECTED" |
  | ONLINE | OFFLINE | **FLAP** · "抖动 FLAP" |
  | any | CHANGE | keep previous pill, refresh detail |

  A WiFi reassociation now produces a single row that says
  "RECONNECT" with current device info, instead of three rows that
  look like real flapping. Devices outside the 10 s window still get
  a fresh row, so genuine disconnects + late reconnects are visible.
  (`argusweb/assets/dashboard.html`)

Desktop layout and library API are unchanged.

---

## [0.13.1] - 2026-05-10

Patch release. Responsive rework of the embedded dashboard so the
HTTP UI reads well on phones. No library / API changes; single-file
change to `argusweb/assets/dashboard.html`.

### Fixed · 修复

- **Mobile UX** for the built-in Web UI (`argusweb`):
  - 5-column device table collapses into stacked **cards** below the
    640 px breakpoint. Each card shows MAC + RSSI on the top row,
    hostname + IP on the second, radio / SSID / wired badge on the
    third, with proper wrapping for long hostnames (previously the
    desktop table forced horizontal scroll on narrow viewports).
  - Header: status pills stack below the title on narrow screens
    (previously they pushed off the right edge).
  - Events list: grid-template-areas layout so timestamp + pill
    stay on line 1, MAC + detail on line 2, instead of a single
    overflowing line. Page-level scrolling on mobile (removed the
    inner `max-height: 70vh` overflow container on narrow
    viewports — mobile users expect to swipe the page, not a
    nested region).
  - `viewport-fit=cover` + `env(safe-area-inset-*)` padding so the
    layout respects iPhone notches / home-bar. `theme-color`
    matches the dark background so the iOS status bar blends.
  - Long hostnames / MACs now `word-break: break-all` instead of
    forcing horizontal scroll.

Desktop layout is unchanged above 640 px — same 2-column grid,
same table, same SSE event list.

---

## [0.13.0] - 2026-05-10

Focus on **built-in dashboard**: a zero-dependency, single-file HTTP +
Server-Sent Events UI embedded in the binary. Opt-in via a new
`-listen` flag in `argusd`; the core library is unchanged (the
dashboard ships in a separate `argusweb` subpackage so consumers who
don't want `net/http` in their binary can skip it).

No breaking change.

### Added · 新增

- **`argusweb` subpackage** — HTTP + SSE dashboard:
  - `argusweb.NewServer(*argus.Watcher) *Server` — constructs an
    `http.Handler` with three routes
  - `(*Server).OnEvent(Event)` — fan-out entry; wire it alongside
    your `EventHandler` so incoming events stream to connected
    dashboard clients
  - `(*Server).Shutdown(ctx)` — drains SSE subscribers
  - HTTP surface:
    - `GET /` — single embedded HTML page with vanilla JS +
      EventSource (no CDN, no framework, no build step)
    - `GET /api/devices` — JSON snapshot of the current `Known()`
      set, keyed by the stable JSON field names from STABILITY.md
    - `GET /api/events` — Server-Sent Events stream; event names
      match `EventKind.String()` (`ONLINE` / `OFFLINE` / `CHANGE`);
      `data:` payload is the same JSON shape as
      `json.Marshal(argus.Event{})`
  - **Slow-subscriber safety**: each SSE connection has an 8-slot
    buffered channel; `OnEvent` drops events for subscribers whose
    buffers are full, so a stuck client never pins memory or blocks
    other subscribers
  - **Dashboard UX**: dark theme, bilingual labels (EN/中文), live
    RSSI-tiered color coding, 30 s periodic re-sync in case an
    event was dropped, auto-reconnect on transient disconnects
  - Zero third-party dependencies (`net/http` + `embed` from stdlib)
  - 6 unit tests: index HTML, 404, devices JSON, SSE hello frame,
    SSE event delivery, slow-subscriber drop, Shutdown cleanup

- **`argusd -listen=<addr>` flag** — opt-in Web UI:
  ```bash
  /tmp/argusd -listen=127.0.0.1:9099
  # then: curl -N http://127.0.0.1:9099/api/events
  # or open http://127.0.0.1:9099/ in a browser
  ```
  Unset (default) = no HTTP server, zero overhead. Bind to
  `127.0.0.1` for local-only access; put a reverse proxy in front
  for auth + TLS if you want remote access. Graceful shutdown
  wired into both SIGINT/SIGTERM and the Run-exit path. HTTP write
  timeout is disabled (SSE streams are long-lived) but read
  timeout stays at 10 s for request headers.

### Fixed · 修复

- **`argusd` SIGUSR1 control-flow bug** (discovered during the
  v0.12.0 soak): the SIGUSR1 handler lived in the main `for-select`
  next to the Run lifecycle branches. After printing the metrics
  snapshot, the outer loop iterated and started a second `Run()`,
  which immediately returned `ErrAlreadyRunning` and killed the
  daemon via `log.Fatalf`. Moved the handler to a dedicated
  goroutine bound to `exitCtx`. SIGHUP (which genuinely intends
  to restart Run) stays in the main loop. (`cmd/argusd/main.go`)

### Documentation

- `STABILITY.md` Stable surface extended with `argusweb.Server` +
  its method set.
- `SOAK_v0.12.0.md` — 5-minute router soak report covering the
  SIGUSR1 bug, the fix, and the clean re-run.

---

## [0.12.0] - 2026-05-10

Focus on **tracing + fuzz hardening**: opt-in distributed-tracing hook
(adapter for OpenTelemetry / OpenTracing / Datadog in ~15 lines) plus
fuzz targets for the two untrusted-text parsing surfaces.
Non-breaking — zero observable cost when the span hook is unregistered.

### Added · 新增

- **Distributed tracing hook** (`span.go`):
  - `SpanRecorder` interface — `Start(ctx, name) (ctx, finish func(error))`
  - `SpanRecorderFunc` adapter (mirror of `http.HandlerFunc`)
  - `WithSpanRecorder(r SpanRecorder) Option`
  - Currently wired at two lifecycle points: `argus.Run` (top-level
    span covering the baseline fetch + poll loop) and
    `argus.handleDisconnectHint` (the multi-stage 500 ms wait +
    ping + emit path — the only non-trivial logical trace in the
    library)
  - Panic isolation: recorder panics in both `Start` and `finish`
    are recovered; tracing failures never kill the caller
  - When unregistered (the default), every `startSpan` call site
    returns a shared `noopFinish` — single nil check, zero
    closure allocation
  - OTel adapter is ~15 lines (see godoc on `SpanRecorder`)

- **Fuzz targets** (`fuzz_test.go`):
  - `FuzzParseSyslogLine` — the syslog line parser is an
    untrusted-text surface (anything running on the router can emit
    lines via `logger(1)`). 10 seeds drawn from real MT7981
    samples. Ran 3 s locally at 18 K exec/s with no panics.
  - `FuzzLoadDHCPLeases` — `/tmp/dhcp.leases` parser. 8 seeds
    covering malformed whitespace, short rows, non-UTF-8. 3 s run
    at 1.5 K exec/s with no panics.
  - CI runs both for 5 s each on Go 1.25 (`.github/workflows/ci.yml`)
    so regressions show up on PR before release.

### Changed · 变更

- `.github/workflows/ci.yml` — added `Fuzz smoke` step gated on
  `matrix.go == '1.25'` (fuzz engine is more stable on the newest
  toolchain).

### Documentation

- `STABILITY.md` Stable surface extended with `SpanRecorder` /
  `SpanRecorderFunc` / `WithSpanRecorder`.

---

## [0.11.0] - 2026-05-10

Focus on **discoverability polish**: package-level godoc overview,
English error chain for observability pipelines, and main-package
test coverage raised from 66.8% to 75.1%. Non-breaking —
`Error()` strings change wording but the error surface (sentinels,
`errors.Is` / `errors.As` matching) is unchanged.

### Added · 新增

- **Package-level `doc.go`** — architecture diagram, quick-start,
  extension points, lifecycle, observability, error-handling, and
  supported-Go-version summary. pkg.go.dev now renders a proper
  overview at the top of the package page instead of the terse
  one-paragraph summary. (`doc.go`)

- **New tests** (12 total) raising main-package coverage to 75.1%:
  - `coverage_fills_test.go` — table-driven coverage for
    `DecisionKind.String` / `.Label` / `.MarshalJSON`,
    `LogLevel.String`, `ConfigError.Error`, `Decision.String`,
    `contains` helper, `isIn172` boundary, `WithDecisionHandler`
    registration, `DefaultHintSource.invalidateCache`,
    `invalidateHintsCache`, `EnsureFetcher` pre-set short-circuit
  - `enrich_parsers_test.go` — `loadARPCommand` with empty argv,
    bad executable, and synthetic `echo`-backed payload parsing
    (covers the IPv4 / IPv6 / FAILED / INCOMPLETE filter paths)
  - Added 2 cases to `timezone_test.go`: `TZ=CST-8` POSIX parsing
    and `TZ=UTC` IANA fallback

### Changed · 变更

- **Error messages translated to English** (13 call sites in
  `detect.go` / `fetcher.go` / `hostapd.go` / `logwatch.go` /
  `watcher.go`). Rationale: error chains flow through structured
  log pipelines and APMs; mixed-language error strings made
  grouping / dashboards harder for non-Chinese-speaking operators.
  User-facing Chinese content (decision `Label()` text, CLI table
  banner, `Config.String()` summary) is **unchanged** — product
  UX stays bilingual where appropriate.
  - `"无法读取 ubus 服务列表"` → `"list ubus services"`
  - `"未在 ubus 上找到 ahsapd.sta 或 hostapd.* 服务"` → `"no ahsapd.sta or hostapd.* service found on ubus"`
  - `"调用 ubus ahsapd.sta getStaInfo 失败"` → `"ubus call ahsapd.sta getStaInfo"`
  - `"解析 ubus 返回 JSON 失败"` → `"parse ubus ahsapd.sta JSON"`
  - `"hostapd 接口探测失败"` → `"detect hostapd interfaces"`
  - `"解析 %s get_status JSON 失败"` → `"parse %s get_status JSON"`
  - `"获取 logread stdout 失败"` → `"open logread stdout"`
  - `"启动 logread 失败"` → `"start logread"`
  - `"logread 扫描错误"` → `"logread scan error"`
  - `"logread 进程退出"` → `"logread process exited"`
  - `"onEvent 不能为 nil"` → `"onEvent must not be nil"`
  - `"初始基线拉取失败"` → `"baseline fetch"`
  - `"系统日志监听异常退出"` → `"syslog watcher exited"`
  - syslog drop counter message translated to English

### Documentation

- `doc.go` rewritten from 22 lines to a full package overview
  (architecture map, quick-start, extension points, lifecycle,
  observability, stability, supported Go versions).

---

## [0.10.0] - 2026-05-10

Focus on **label-bucketed metrics**: the unlabeled `argusmetrics.Counters`
answers "how many OFFLINE events total?", but production observability
pipelines usually need "how many OFFLINE events *per SSID*, *per band*,
*per MAC*?" Previously consumers wrote a custom `DecisionHandler` with
their own sharded map; v0.10.0 ships a standard implementation.

No breaking change.

### Added · 新增

- **`argusmetrics.LabeledCounters`** — Prometheus-style `CounterVec`
  equivalent, without the Prometheus dependency:
  ```go
  m := argusmetrics.NewLabeled([]string{"ssid", "band"}, extractor)
  w := argus.New(argus.WithDecisionHandler(m.OnDecision))
  // Snapshot keys: "CONNECT_EMIT|home|5G", "OFFLINE_EMIT|guest|2.4G", …
  ```
  - `NewLabeled(labels []string, extract LabelExtractor) *LabeledCounters`
  - `OnDecision(Decision)` — **40 ns/op, 2 allocs** (mutex +
    joined key); ~25× slower than the unlabeled 1.7 ns/op path,
    still negligible for Argus's decision rate
  - `Snapshot() map[string]uint64` — keys `"<kind>|<v1>|<v2>..."`,
    consumers split on "|" when bridging to a backend with
    structured labels
  - `LabelNames() []string` — defensive copy for Prometheus
    `CounterVec` declaration
  - `Reset()` — for tests
  - Arity mismatches from a broken `LabelExtractor` are silently
    dropped (prevents cardinality leaks from buggy extractors)
  (`argusmetrics/labeled.go`)

- **`LabelExtractor`** type — `func(argus.Decision) []string`. Must
  be cheap; called once per Decision.

- **`ExampleLabeledCounters`** — godoc example with `// Output:`
  directive demonstrating per-MAC bucketing. (`argusmetrics/example_test.go`)

- **Tests** (`argusmetrics/labeled_test.go`, 7 tests):
  - `TestLabeledCountersBasicKeying` — single label path
  - `TestLabeledCountersMultiLabel` — multi-label keying
  - `TestLabeledCountersArityMismatchDropped` — cardinality-leak guard
  - `TestLabeledCountersNilExtractor` — equivalent to unlabeled
  - `TestLabeledCountersConcurrentSafe` — 10 000 atomic adds / 50 goroutines
  - `TestLabeledCountersReset`
  - `TestLabeledCountersLabelNamesIsCopy` — defensive copy of label names
  - `BenchmarkLabeledOnDecision` — 40 ns/op, 2 allocs on M4

### Documentation

- `STABILITY.md` Stable surface extended with `LabeledCounters` /
  `NewLabeled` / `LabelExtractor` / `(*LabeledCounters).OnDecision` /
  `Snapshot` / `LabelNames` / `Reset`.

---

## [0.9.0] - 2026-05-10

Focus on **observability polish**: structured logging hook and
field-level config validation errors. Both are purely additive —
existing consumers see no behavior change.

### Added · 新增

- **Structured logging hook** — `LoggerHandler` / `LogLevel` /
  `LogAttr` types and `WithLogger(LoggerHandler) Option`:
  ```go
  argus.WithLogger(func(ctx context.Context, level argus.LogLevel, msg string, attrs ...argus.LogAttr) {
      slog.LogAttrs(ctx, slog.Level(level), msg, toSlog(attrs)...)
  })
  ```
  The library emits at Info (watcher starting, fetcher detected,
  watcher stopped), Warn (syslog buffer overflow, fetch tick failed,
  stop timeout), and Error (detect failure). The **hot decision
  path does NOT log** — every emission is a lifecycle or
  recoverable-anomaly event. When `WithLogger` is unregistered
  (default), log call sites bail on a single nil check. Logger
  panics are recovered; they never kill the caller. Adapters for
  `log/slog`, `zap`, and `zerolog` are all ~5 lines. (`logger.go`)

- **`ConfigError` struct** — `{Field, Value, Reason}` with
  `errors.As` support. `Config.Validate` now returns a
  `*ConfigError` (still unwraps to `ErrInvalidConfig` for existing
  `errors.Is` callers):
  ```go
  var ce *argus.ConfigError
  if errors.As(err, &ce) {
      formErrors[ce.Field] = ce.Reason  // "must be > 0"
  }
  ```
  Intended for web config UIs and form-level validation feedback;
  previously consumers had to regex-match `error.Error()` to
  identify the offending field. (`errors.go`, `watcher.go:Validate`)

- **`ExampleWithLogger`** and **`ExampleConfigError`** — godoc
  examples demonstrating both new facilities. The ConfigError
  example has an `// Output:` directive verifying the message
  format, so it's regression-locked. (`example_test.go`)

- **Tests** (`logger_test.go`, 5 tests):
  - `TestLoggerReceivesLifecycleEvents` — Run emits `watcher starting` at Info
  - `TestLoggerPanicIsolated` — panicking logger doesn't kill Run
  - `TestLoggerNilIsZeroCost` — unregistered logger is a no-op
  - `TestConfigErrorExposesFieldViaAs` — errors.As extracts *ConfigError
  - `TestConfigErrorFromRunIsUnwrappable` — errors.Is still works for coarse matching

### Changed · 变更

- `Config.Validate` — error type changed from `fmt.Errorf(...)` to
  `*ConfigError`. This is **non-breaking** for existing consumers:
  the `error` interface is unchanged, `errors.Is(err, ErrInvalidConfig)`
  still works, and the `Error()` string is stable in format
  (`argus: invalid config: <reason> (field=<name> value=<v>)`).
  New field-level extraction via `errors.As` is the added value.
- `Run` — no longer double-wraps the `Validate` error with
  `fmt.Errorf("%w: %v", ErrInvalidConfig, err)`. Returns the
  `*ConfigError` directly so `errors.As(err, &ConfigError{})` works
  at the Run call site. `errors.Is(err, ErrInvalidConfig)` behavior
  is preserved via `(*ConfigError).Unwrap`.

### Documentation

- `STABILITY.md` Stable surface extended with `LoggerHandler` /
  `LogLevel` / `LogAttr` / `ConfigError` / `WithLogger`.

---

## [0.8.0] - 2026-05-10

Focus on **adoption readiness**: explicit Go version support policy,
formalized context cancellation contract, and the maintenance signals
(security policy, code of conduct, issue/PR templates) that enterprise
consumers check before taking a dependency. No code-level breaking
changes; the library public surface is unchanged.

### Added · 新增

- **Go version support policy** — `go.mod` declares `go 1.21` (was
  `go 1.25`). Argus supports the **current Go release and the two
  preceding minor versions** (N-2). CI matrix now tests on Go 1.21,
  1.22, 1.23, 1.24, and 1.25. Consumers on older toolchains
  (OpenWrt SDKs, embedded builds) can now pin Argus without waiting
  for their Go upgrade path. (`go.mod`, `.github/workflows/ci.yml`)

- **Context cancellation contract** — `STABILITY.md` now contains a
  formal table documenting exactly what `Run` / `Stop` / `List` /
  `EnsureFetcher` / `HintSource.Hints` / `Fetcher.Fetch` do when
  `ctx.Done()` fires mid-call or when ctx is pre-cancelled. Key
  invariants:
  - `Run` returns `nil` (not `ctx.Err()`) on graceful cancellation —
    matches `http.Server.Shutdown` convention
  - `Stop` always waits for in-flight decisions to flush; if
    `stopCtx` expires, workers still exit in the background (never
    leak)
  - `Run` + `Stop` concurrency is safe; nil ctx is a programming
    error, not silently masked
  (`STABILITY.md`)

- **`context_contract_test.go`** — 6 regression tests enforcing the
  contract: `TestContractRunReturnsNilOnCtxCancel`,
  `TestContractStopIdempotent`,
  `TestContractStopReturnsDeadlineExceeded`,
  `TestContractRunAlreadyRunning`,
  `TestContractRunStopConcurrencySafe`,
  `TestContractListReturnsFetcherError`.

- **Security policy** — [`SECURITY.md`](./SECURITY.md) documents the
  private vulnerability reporting channel (email /
  GitHub security advisory), SLA (72 h ack, 7 d triage, 30 d fix
  for high/critical), supported-version table, and threat model.
  Argus is a local-network read-only observer, makes no outbound
  requests, and ships zero third-party dependencies.

- **Code of conduct** — [`CODE_OF_CONDUCT.md`](./CODE_OF_CONDUCT.md)
  (Contributor Covenant v2.1).

- **Issue / PR templates** — `.github/ISSUE_TEMPLATE/bug_report.yml`,
  `feature_request.yml`, and `config.yml` (blank issues disabled,
  security reports routed to private advisory). New
  `.github/pull_request_template.md` walks contributors through the
  stability-impact + test-plan checklist.

- **Release cadence & LTS policy** — `CONTRIBUTING.md` now documents:
  cadence (minor = theme-driven, not scheduled), supported Go
  versions (N-2), post-v1.0 LTS (current minor + security-only for
  previous minor for 6 months), and deprecation timeline (one full
  minor cycle minimum before removal).

### Fixed · 修复

- **Race in `TestSetupLocalTimezone`** — the test mutated global
  `time.Local` in a `defer`, racing with parallel tests that read
  `time.Now()`. Renamed to `TestDetectLocalLocationSafe` and
  rewritten to exercise only the non-mutating `DetectLocalLocation`
  path. Race exposed by the new multi-Go-version CI matrix
  (`go test -race -count=3`). (`timezone_test.go`)

### Documentation

- `STABILITY.md` adds the "Context cancellation contract" table to
  the Stable surface.
- `CONTRIBUTING.md` adds "Release cadence & LTS policy" and
  "Security" sections.

---

## [0.7.0] - 2026-05-10

Focus on **portability + observability**: make the enrichment pipeline
pluggable for non-OpenWrt targets and ship a zero-dependency metrics
collector that bridges cleanly to Prometheus / OpenTelemetry / StatsD.
No breaking change.

### Added · 新增

- **`Hint` exported type** — was previously an unexported `hint` struct.
  Now part of the Stable public surface so custom `HintSource`
  implementations can return it directly. (`enrich.go`)

- **`HintSource` interface** — single-method abstraction:
  ```go
  type HintSource interface {
      Hints(ctx context.Context) map[string]Hint
  }
  ```
  Consumers on non-OpenWrt systems (standard Linux, macOS dev loops,
  embedded devices with custom lease databases) can now inject their
  own hint source without forking internal enrichment logic.
  (`enrich.go`)

- **`DefaultHintSource` struct** — the existing `/tmp/dhcp.leases` +
  `ip neigh show` reader exposed as a configurable struct:
  - `LeasesPath string` — override default `/tmp/dhcp.leases`
  - `ARPCommand []string` — override default `["ip", "neigh", "show"]`
  - `CacheTTL time.Duration` — override default 5s cache window
  Useful for custom firmwares that store leases elsewhere (e.g.
  `/var/lib/misc/dnsmasq.leases` on stock OpenWrt 22+, or a shim
  path in tests). (`enrich.go`)

- **`WithHintSource(h HintSource) Option`** — functional option on
  `argus.New` to inject a custom source. When set, Argus bypasses
  `DefaultHintSource` entirely on every poll tick. (`watcher.go`)

- **`argusmetrics` subpackage** — zero-dependency in-process counter
  aggregator for `Decision` and `Event` streams:
  - `argusmetrics.New() *Counters` — construct
  - `Counters.OnDecision` satisfies `argus.DecisionHandler`; can be
    passed directly to `argus.WithDecisionHandler`
  - `Counters.OnEvent(Event)` — for business-level online/offline
    counts
  - `Counters.Snapshot() map[string]uint64` — stable string keys
    (`CONNECT_EMIT`, `OFFLINE_EMIT`, `EVENT_ONLINE`, …) ready to
    bridge to any metrics backend in ~10 lines
  - `Counters.Reset()` — for tests
  Hot path is **1.7 ns/op, 0 allocs** (atomic increment on a fixed
  [128]uint64 indexed by `DecisionKind`). No Prometheus, OTel, or
  StatsD dependency is pulled into Argus — consumers bridge in their
  own layer. (`argusmetrics/argusmetrics.go`)

- **`ExampleCounters`** — godoc example demonstrating the bridge
  pattern (Watcher → Counters → Snapshot → external backend).
  (`argusmetrics/example_test.go`)

- **Tests**:
  - `hintsource_test.go` — `TestWithHintSourceInjection`,
    `TestDefaultHintSourceCustomPaths`, `TestDefaultHintSourceCache`
  - `argusmetrics/argusmetrics_test.go` — concurrent-safety stress
    (10000 atomic adds across 100 goroutines), Reset, benchmark

### Changed · 变更

- Internal `hint` → `Hint` rename; all call sites updated. No
  behavior change; existing consumers that didn't depend on the
  unexported name are unaffected.
- `loadHints(ctx)` now delegates to a package-level
  `*DefaultHintSource` so the legacy call path and the new
  `HintSource` path share the same cache TTL semantics.

### Documentation

- `STABILITY.md` Stable surface extended with `Hint`, `HintSource`,
  `DefaultHintSource`, `WithHintSource`, and the `argusmetrics`
  subpackage (`Counters` + `OnDecision` + `OnEvent` + `Snapshot` +
  `Reset`).

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
  `import "github.com/xxl6097/argusd/argustest"` instead of forking internal
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

[Unreleased]: https://github.com/xxl6097/argusd/compare/v0.15.3...HEAD
[0.15.3]: https://github.com/xxl6097/argusd/compare/v0.15.2...v0.15.3
[0.15.2]: https://github.com/xxl6097/argusd/compare/v0.15.1...v0.15.2
[0.15.1]: https://github.com/xxl6097/argusd/compare/v0.15.0...v0.15.1
[0.15.0]: https://github.com/xxl6097/argusd/compare/v0.14.0...v0.15.0
[0.14.0]: https://github.com/xxl6097/argusd/compare/v0.13.3...v0.14.0
[0.13.3]: https://github.com/xxl6097/argusd/compare/v0.13.2...v0.13.3
[0.13.2]: https://github.com/xxl6097/argusd/compare/v0.13.1...v0.13.2
[0.13.1]: https://github.com/xxl6097/argusd/compare/v0.13.0...v0.13.1
[0.13.0]: https://github.com/xxl6097/argusd/compare/v0.12.0...v0.13.0
[0.12.0]: https://github.com/xxl6097/argusd/compare/v0.11.0...v0.12.0
[0.11.0]: https://github.com/xxl6097/argusd/compare/v0.10.0...v0.11.0
[0.10.0]: https://github.com/xxl6097/argusd/compare/v0.9.0...v0.10.0
[0.9.0]: https://github.com/xxl6097/argusd/compare/v0.8.0...v0.9.0
[0.8.0]: https://github.com/xxl6097/argusd/compare/v0.7.0...v0.8.0
[0.7.0]: https://github.com/xxl6097/argusd/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/xxl6097/argusd/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/xxl6097/argusd/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/xxl6097/argusd/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/xxl6097/argusd/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/xxl6097/argusd/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/xxl6097/argusd/releases/tag/v0.1.0
