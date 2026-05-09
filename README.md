# Argus

> **EN** — The hundred-eyed watcher for OpenWrt device presence.
> **中文** — 百眼守望者 · 多源融合的 OpenWrt 接入设备观察库。

[![Go Reference](https://pkg.go.dev/badge/github.com/xxl6097/argus.svg)](https://pkg.go.dev/github.com/xxl6097/argus)
[![Go Report Card](https://goreportcard.com/badge/github.com/xxl6097/argus)](https://goreportcard.com/report/github.com/xxl6097/argus)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Tests](https://img.shields.io/badge/tests-passing-brightgreen)](.)

**EN** — Argus is a Go library and CLI tool for real-time device presence detection on OpenWrt routers. It fuses six data sources into a single millisecond-grade event stream: `Online` / `Offline` / `Change`. Named after the hundred-eyed giant of Greek myth — whose eyes never all slept — Argus keeps watch over every WiFi station and wired host on your LAN.

**中文** — Argus 是一个针对 OpenWrt 路由器的**实时设备感知库与命令行工具**,融合六路数据源形成毫秒级事件流:上线(`Online`) / 离线(`Offline`) / 状态变更(`Change`)。名字取自希腊神话中的百眼巨人——他的眼睛永远不会同时闭上——永不沉睡的守望者。

---

## 目录 · Table of Contents

1. [特性 · Features](#特性--features)
2. [快速开始 · Quick Start](#快速开始--quick-start)
3. [架构 · Architecture](#架构--architecture)
4. [API 速查 · API Overview](#api-速查--api-overview)
5. [配置调优 · Configuration](#配置调优--configuration)
6. [可观测性 · Observability](#可观测性--observability)
7. [路线图 · Roadmap](#路线图--roadmap)
8. [兼容性 · Compatibility](#兼容性--compatibility)
9. [贡献 · Contributing](#贡献--contributing)

---

## 特性 · Features

- 🔀 **多源融合 · Multi-source fusion**
  **EN** — ahsapd + hostapd + `logread -f` + DHCP leases + ARP states + ICMP probe, all merged into one stream.
  **中文** — 厂商 ubus(ahsapd) + 官方 hostapd + 系统日志 + DHCP 租约 + ARP 状态 + ICMP 探测,六路同步汇聚为单一事件流。

- 🏭 **零配置多厂商兼容 · Vendor-agnostic zero-config**
  **EN** — Auto-detects `ahsapd` (vendor firmware) or `hostapd.*` (stock OpenWrt) at startup.
  **中文** — 启动时自动探测 ubus 可用服务,厂商固件与官方 OpenWrt 无需配置切换。

- ⚡ **毫秒级事件 · Sub-second events**
  **EN** — Kernel log streaming (`New Sta`, `Del Sta`, `Deauth`, `DHCPACK`…) delivers online/offline in 1-2 s.
  **中文** — 通过实时日志(内核关联 / 断开 / Deauth / DHCP 分配)在 1-2 秒内识别上下线。

- 🛡️ **多维离线判定 · Multi-dimensional offline detection**
  **EN** — Four-layer decision: ICMP ping + AP association table + RSSI tiers + ARP `FAILED/INCOMPLETE`.
  **中文** — 四层判定:ICMP 可达性 + AP 关联表感知(息屏保护) + RSSI 信号分级 + ARP 失败加速。

- 🌊 **抗抖动 · Flap suppression**
  **EN** — 90 s cooldown plus 30 s same-kind suppression window eliminates weak-signal thrashing.
  **中文** — 90 秒冷却期 + 30 秒同类事件压制,弱信号边缘设备不再反复上下线。

- 🧩 **纯标准库 · Pure stdlib, single static binary**
  **EN** — ~2.6 MB static binary (`CGO_ENABLED=0`, GOARCH=arm64). Drop into `/tmp` and run.
  **中文** — 纯 Go 标准库,静态编译,约 2.6 MB,直接丢到 OpenWrt 路由器 `/tmp` 即可运行。

- 🔬 **可观测性 · Observability**
  **EN** — Optional `DecisionHandler` exposes 16 internal branch decisions for tuning and debugging.
  **中文** — 可选的决策回调暴露内部 16 种判定分支,调参与排障非常友好。

- 🔒 **安全硬化 · Security hardened**
  **EN** — IP regex + `net.ParseIP` double validation, interface whitelist — no command injection.
  **中文** — IP 双重校验(正则 + `net.ParseIP`)、hostapd 接口名白名单,杜绝命令注入。

- 🧵 **并发安全 · Concurrency-safe**
  **EN** — `sync.Mutex` protects shared state; `go test -race` clean.
  **中文** — `sync.Mutex` 保护共享状态,`go test -race` 全部通过。

---

## 快速开始 · Quick Start

### 作为库使用 · Use as a library

```go
import argus "github.com/xxl6097/argus"

func main() {
    // EN: parse router's /etc/TZ into time.Local (so logs match wall clock).
    // 中文: 解析 /etc/TZ 到 time.Local, 让日志时间和路由器一致。
    argus.SetupLocalTimezone()

    ctx, stop := signal.NotifyContext(context.Background(),
        syscall.SIGINT, syscall.SIGTERM)
    defer stop()

    w := argus.New(
        argus.OnFetcherDetected(func(k argus.FetcherKind) {
            log.Printf("data source / 数据源: %s", k)
        }),
    )

    err := w.Run(ctx, func(e argus.Event) {
        switch e.Kind {
        case argus.EventOnline:
            fmt.Printf("[+] %s joined / 上线 %s\n", e.Device.MAC, e.Device.IP)
        case argus.EventOffline:
            fmt.Printf("[-] %s left / 离线\n", e.Device.MAC)
        case argus.EventChange:
            for _, c := range e.Changes {
                fmt.Printf("[~] %s %s: %q → %q\n",
                    e.Device.MAC, c.Field, c.Old, c.New)
            }
        }
    }, nil)
    if err != nil {
        log.Fatal(err)
    }
}
```

### 作为 CLI 使用 · Use as a CLI

**EN** — Prebuilt binaries for common OpenWrt CPU architectures are published on the [Releases page](https://github.com/xxl6097/argusd/releases) (amd64 / arm64 / armv5 / armv7 / mips / mipsle / mips64 / mips64le / riscv64 / 386, all static).
**中文** — 常见 OpenWrt 架构的预编译二进制发布在 [Releases 页面](https://github.com/xxl6097/argusd/releases)(amd64 / arm64 / armv5 / armv7 / mips / mipsle / mips64 / mips64le / riscv64 / 386, 全部静态链接)。

```bash
# EN: Download the matching archive, verify, and deploy.
# 中文: 下载对应架构的包, 校验, 上传路由器。
VER=v0.1.0        # 替换为实际版本
TARGET=linux-mipsle-softfloat   # 替换为你的架构
curl -LO "https://github.com/xxl6097/argusd/releases/download/${VER}/argusd_${VER}_${TARGET}.tar.gz"
curl -LO "https://github.com/xxl6097/argusd/releases/download/${VER}/SHA256SUMS"
sha256sum -c SHA256SUMS --ignore-missing
tar -xzf argusd_${VER}_${TARGET}.tar.gz
scp argusd_${VER}_${TARGET}/argusd root@192.168.1.1:/tmp/argusd
ssh root@192.168.1.1 '/tmp/argusd'
```

Or build from source · 或从源码构建:

```bash
# EN: Cross-compile for OpenWrt (aarch64 example).
# 中文: 跨编译到 OpenWrt (以 aarch64 路由器为例)。
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
    go build -trimpath -ldflags="-s -w" \
    -o argusd ./cmd/argusd

# EN: Deploy and run.
# 中文: 上传并运行。
scp argusd root@192.168.1.1:/tmp/
ssh root@192.168.1.1 '/tmp/argusd'
```

Sample output · 输出示例:

```
2026/05/09 18:40:21 data source / 数据源: ahsapd
MAC 地址             IP 地址          主机名            厂商     类型    信号         无线
──────────────────────────────────────────────────────────────────────────────────────────
2C:CF:67:1D:27:AC    192.168.1.11     raspberrypi       rasp..   PC      -            wired
B0:FC:36:32:94:61    192.168.1.5      lenovo            DESK..   Phone   -38(极强)    5G/avgb-5G
BA:79:97:73:89:8D    192.168.1.213    BA799773898D      -        Phone   -44(极强)    5G/avgb-5G
──────────────────────────────────────────────────────────────────────────────────────────
4 devices online · 4 台设备在线 (WiFi: 3, Wired 有线: 1)

[2026-05-09 18:42:03] [syslog/系统日志] WIFI_CONNECT    BA:79:...
[2026-05-09 18:42:03] [syslog/系统日志] DHCP_ACK        BA:79:... IP=192.168.1.213
[2026-05-09 18:42:03] [event/事件]   ONLINE / 上线      BA:79:... 192.168.1.213 iPhone -44(极强) 5G/avgb-5G
```

---

## 架构 · Architecture

**EN** — Six feeds enter the Event Fusion Engine; the Watcher emits events (business), decisions (observability), and errors (failures).
**中文** — 六路数据进入融合引擎,由 Watcher 统一产出三类回调:业务事件 / 决策观测 / 错误上报。

```
                       ┌──────────────┐
                       │   logread    │ ← realtime kernel events / 实时内核事件
                       │      -f      │   (Connect/Disconnect/Deauth/DHCPACK)
                       └──────┬───────┘
                              │
 ┌─ ubus call ────┐    ┌──────┼──────┐     ┌─ ARP state ──┐
 │ ahsapd.sta or  │ →  │  Event      │  ←  │ ip neigh     │
 │ hostapd.<iface>│    │  Fusion     │     │ FAILED/OK    │
 └────────────────┘    │  Engine     │     └──────────────┘
                       │  融合引擎    │
 ┌─ DHCP leases ──┐    │             │     ┌─ ICMP probe ─┐
 │ /tmp/dhcp.     │ →  │             │  ←  │ ping -c 1    │
 │   leases       │    │             │     │ -W 1         │
 └────────────────┘    └──────┬──────┘     └──────────────┘
                              │
                        ┌─────▼──────┐
                        │  Watcher   │  ← diff + cooldown + flap-suppress
                        │  监听器     │
                        └─────┬──────┘
                              │
                 ┌────────────┼────────────┐
                 ▼            ▼            ▼
            EventHandler  DecisionHandler  ErrorHandler
            业务事件       决策观测         错误上报
            (business)    (observability)  (failures)
```

**EN** — See [`ONLINE.md`](./ONLINE.md) and [`OFFLINE.md`](./OFFLINE.md) for detailed decision flows.
**中文** — 完整判定流程参见 [`ONLINE.md`](./ONLINE.md) 和 [`OFFLINE.md`](./OFFLINE.md)。

---

## API 速查 · API Overview

| Type · 类型 | Purpose · 用途 |
|------|---------|
| `argus.Watcher` | **EN** Main entry · **中文** 主入口;`New(opts...) *Watcher` |
| `argus.Event` / `EventKind` | **EN** Business events (Online/Offline/Change) · **中文** 业务事件 |
| `argus.Decision` / `DecisionKind` | **EN** Internal decision trace (16 branches) · **中文** 内部判定链路(16 种分支) |
| `argus.Config` | **EN** Tunable thresholds · **中文** 阈值配置 |
| `argus.Fetcher` | **EN** Data source interface, auto-detected · **中文** 数据源接口,自动探测 |
| `argus.Prober` | **EN** Liveness probe; default `ICMPProber{Timeout: 1s}` · **中文** 活性探测,默认 ICMP |
| `argus.SyslogEvent` | **EN** Raw syslog parse result · **中文** 原始系统日志解析结果 |
| `argus.SetupLocalTimezone()` | **EN** Parse `/etc/TZ` (e.g. `CST-8`) · **中文** 从 `/etc/TZ` 设置 `time.Local` |

Functional options · 函数式选项:

```go
argus.WithConfig(cfg)                      // EN: override defaults · 中文: 覆盖默认
argus.WithFetcher(custom)                  // EN: custom data source · 中文: 注入自定义数据源
argus.WithProber(nil)                      // EN: disable liveness probe · 中文: 关闭活性探测
argus.OnFetcherDetected(func(k) {...})     // EN: detection callback · 中文: 自动探测回调
argus.WithDecisionHandler(func(d) {...})   // EN: decision trace · 中文: 决策观测
```

---

## 配置调优 · Configuration

**EN** — All thresholds live in `argus.Config`. Zero values preserve defaults.
**中文** — 所有阈值集中在 `argus.Config`,传零值保留默认。

```go
w := argus.New(argus.WithConfig(argus.Config{
    // Polling cadence · 轮询节奏
    PollInterval:  1 * time.Second,   // default · 默认 1s
    OfflineMisses: 5,                 // default · 默认 5
    FetchTimeout:  3 * time.Second,   // default · 默认 3s

    // Anti-flap · 抗抖动
    OfflineCooldown:            90 * time.Second,
    CooldownReleaseRSSI:        -65,
    WeakRSSI:                   -80,
    ExtremelyWeakRSSI:          -88,
    WeakMissThreshold:          5,
    ExtremelyWeakMissThreshold: 2,
    FlapSuppressionWindow:      30 * time.Second,
}))
```

Guidelines · 使用建议:

| Scenario · 场景 | Suggested change · 建议配置 |
|----------|------------------|
| Aggressive IoT gateway · 激进响应、容忍噪音 | `FlapSuppressionWindow: 0`, `OfflineCooldown: time.Nanosecond` |
| Home/away automation · 家庭自动化 | **EN** keep defaults · **中文** 保留默认 |
| Crowded WiFi environment · 拥挤无线环境 | `WeakRSSI: -75`, `WeakMissThreshold: 10` |
| Trust AP table only · 完全信任 AP 关联表 | `WithProber(nil)` |

---

## 可观测性 · Observability

**EN** — Argus exposes three Watcher callback channels; use the right one for the right audience.
**中文** — Watcher 对外暴露三条回调通道,不同受众用不同通道。

| Channel · 通道 | Type · 类型 | Frequency · 频率 | Use case · 用途 |
|---------|------|-----------|----------|
| `EventHandler` (arg to `Run`) | `Event` | Sparse · 稀疏 | **EN** Business logic · **中文** 业务逻辑(home/away 自动化) |
| `ErrorHandler` (arg to `Run`) | `error` | Rare · 罕见 | **EN** Non-fatal failures · **中文** 非致命错误 |
| `WithDecisionHandler` | `Decision` | Dense · 密集 | **EN** Tuning / debugging · **中文** 调参 / 排障 |

**EN** — For raw syslog mirroring, call `WatchSyslog(ctx, func(SyslogEvent), onError)` directly — it's a standalone helper, not a Watcher option.
**中文** — 需要镜像原始系统日志时,直接调用 `WatchSyslog(ctx, func(SyslogEvent), onError)`,它是独立函数,非 Watcher 选项。

Sample decision trace · 决策跟踪示例:

```
[decision/决策] CONNECT_HINT     BA:79:... (IP=192.168.1.213)
[decision/决策] CONNECT_EMIT     BA:79:... (IP=192.168.1.213)
[event/事件]    ONLINE           BA:79:... 192.168.1.213 iPhone -44(极强) 5G/avgb-5G
[decision/决策] POLL_WEAK_MISS   BA:79:... (RSSI=-82 misses=3/5)
[decision/决策] POLL_WEAK_MISS   BA:79:... (RSSI=-85 misses=5/5)
[decision/决策] OFFLINE_EMIT     BA:79:... (via=poll RSSI=-85)
[event/事件]    OFFLINE          BA:79:...
```

**EN** — `DecisionHandler` is zero-cost when not registered — no allocations, no time calls.
**中文** — 不注册 `DecisionHandler` 时完全零成本:不分配对象、不调用 `time.Now()`。

---

## 路线图 · Roadmap

- [x] **EN** ahsapd / hostapd dual fetcher with auto-detection
      **中文** ahsapd / hostapd 双数据源 + 自动探测
- [x] **EN** syslog `logread -f` real-time stream
      **中文** `logread -f` 实时日志流
- [x] **EN** ICMP liveness probe with parallel semaphore
      **中文** ICMP 活性探测 + 并发信号量
- [x] **EN** Cooldown + flap suppression
      **中文** 冷却期 + 抖动抑制
- [x] **EN** Decision handler observability
      **中文** 决策回调可观测性
- [x] **EN** `go test -race` clean
      **中文** 竞态检测全部通过
- [ ] **EN** Direct `ubus` socket integration (skip CLI) · **中文** 直连 `ubus` socket,跳过 CLI
- [ ] **EN** Prometheus exporter · **中文** Prometheus 指标导出
- [ ] **EN** IPv6-only device support · **中文** 仅 IPv6 设备支持
- [ ] **EN** Home Assistant `device_tracker` bridge · **中文** Home Assistant 桥接
- [ ] **EN** Built-in web UI · **中文** 内置 web UI

---

## 兼容性 · Compatibility

| Platform · 平台 | Data source · 数据源 | Status · 状态 |
|----------|-------------|--------|
| MediaTek MT7981 vendor fw · 厂商固件 | ahsapd | ✅ **EN** Reference target · **中文** 参考目标 |
| OpenWrt 23.05+ stock · 官方 | hostapd.* | 🧪 **EN** Theoretical · **中文** 待实测 |
| Any Linux with `logread`+`ubus` | syslog-only | ⚠️ **EN** Events only, no device table · **中文** 仅事件,无设备列表 |

**EN** — Go 1.25+. No cgo. Cross-compiles to any GOOS/GOARCH that runs OpenWrt.
**中文** — Go 1.25+,不使用 cgo,可跨编译到任何 OpenWrt 支持的 GOOS/GOARCH。

---

## 贡献 · Contributing

**EN** — PRs welcome. See [`CONTRIBUTING.md`](./CONTRIBUTING.md). Before submitting:
**中文** — 欢迎 PR,详见 [`CONTRIBUTING.md`](./CONTRIBUTING.md)。提交前请本地通过:

```bash
go vet ./...
go test -race ./...
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./cmd/argusd
```

---

## 更多文档 · More Docs

- [`ONLINE.md`](./ONLINE.md) — **EN** online decision deep-dive · **中文** 上线判定深度解析
- [`OFFLINE.md`](./OFFLINE.md) — **EN** offline + cooldown analysis · **中文** 离线与冷却机制解析
- [GoDoc](https://pkg.go.dev/github.com/xxl6097/argus) — API reference · API 文档

---

## 许可证 · License

MIT © 2026 — see [`LICENSE`](./LICENSE)

---

*"Every station. Every event. Every eye open."*
*"每一台设备,每一次事件,每一只眼睛都不闭上。"*
