# Argus

[English README →](./README.md)

> **OpenWrt 实时设备监控 + 静态 IP 仪表盘 —— 六路数据融合、秒级事件流、零依赖 Web UI**

[![Go Reference](https://pkg.go.dev/badge/github.com/xxl6097/argusd.svg)](https://pkg.go.dev/github.com/xxl6097/argusd)
[![Go Report Card](https://goreportcard.com/badge/github.com/xxl6097/argusd)](https://goreportcard.com/report/github.com/xxl6097/argusd)
[![Go version](https://img.shields.io/github/go-mod/go-version/xxl6097/argusd)](go.mod)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Tests](https://img.shields.io/badge/tests-passing-brightgreen)](.)
[![Release](https://img.shields.io/github/v/release/xxl6097/argusd?sort=semver)](https://github.com/xxl6097/argusd/releases)

![仪表盘](./docs/images/dashboard-desktop.png)

Argus 是一个针对 OpenWrt 路由器的**实时设备感知库与命令行工具**,融合六路数据源(ahsapd · hostapd · syslog · DHCP 租约 · ARP · ICMP)形成秒级事件流:上线(`Online`) / 离线(`Offline`) / 状态变更(`Change`),并内置**零依赖 Web UI**(静态 IP 预留、设备别名、一键修复)。已在 OpenWrt 官方版与 MediaTek 厂商固件(C-Life 等)上验证。名字取自希腊神话中的百眼巨人——他的眼睛永远不会同时闭上——永不沉睡的守望者。

**30 秒跑起来**:

```bash
# 下载地址: https://github.com/xxl6097/argusd/releases
scp argusd root@192.168.1.1:/tmp/ && ssh root@192.168.1.1 \
  '/tmp/argusd -listen :8080 -aliases /etc/argusd/aliases.json'
# 浏览器访问 http://<router-ip>:8080/
```

---

## 目录

1. [特性](#特性)
2. [快速开始](#快速开始)
3. [Web UI · 内置仪表盘](#web-ui--内置仪表盘-v0130)
4. [架构](#架构)
5. [API 速查](#api-速查)
6. [配置调优](#配置调优)
7. [可观测性](#可观测性)
8. [路线图](#路线图)
9. [兼容性](#兼容性)
10. [贡献](#贡献)

---

## 特性

- 🔀 **多源融合** —— 厂商 ubus(ahsapd) + 官方 hostapd + 系统日志 + DHCP 租约 + ARP 状态 + ICMP 探测,六路同步汇聚为单一事件流。
- 🏭 **零配置多厂商兼容** —— 启动时自动探测 ubus 可用服务,厂商固件与官方 OpenWrt 无需配置切换。
- ⚡ **毫秒级事件** —— 通过实时日志(内核关联 / 断开 / Deauth / DHCP 分配)在 1-2 秒内识别上下线。
- 🛡️ **多维离线判定** —— 三层判定:ICMP 可达性 + AP 关联表感知(息屏保护) + RSSI 信号分级 + ARP 失败加速。
- 🌊 **抗抖动** —— 90 秒冷却期 + 30 秒同类事件压制,弱信号边缘设备不再反复上下线。通过 `Config.DisableCooldown` / `DisableFlapSuppression` 可独立关闭任一机制。
- 🧩 **纯标准库 · 单静态二进制** —— 纯 Go 标准库,静态编译,约 2.6 MB,直接丢到 OpenWrt 路由器 `/tmp` 即可运行。
- 🔬 **可观测性** —— 四路可观测性出口,全部 opt-in, 未注册时零成本: `DecisionHandler`(1.7 ns/op, 0 分配)暴露 17 种内部判定分支;`WithLogger` 结构化日志(~5 行桥接 slog/zap/zerolog);`WithSpanRecorder` 分布式追踪(OTel ~15 行);`argusmetrics` 子包自带零依赖计数器(`Counters` / `LabeledCounters`),可直接桥接 Prometheus / OTLP。
- 🔒 **安全硬化** —— IP 双重校验(正则 + `net.ParseIP`)、hostapd 接口名白名单,杜绝命令注入。
- 🧵 **并发安全** —— `sync.Mutex` 保护共享状态,事件在锁外发射;60+ 测试 + 9 个生命周期测试均通过 `-race`。
- 🛟 **Panic 隔离** —— 用户回调(`EventHandler` / `ErrorHandler` / `DecisionHandler`)全部被 `defer recover` 包裹。业务 handler panic 会经 `onError` 上报,不会杀死 Watcher 的任何 goroutine。
- 🔄 **可热重载 (v0.5.0+)** —— `Watcher.Stop(ctx)` + 再次 `Run()` 在热重载配置时保留 `known` / 冷却 / 抖动状态(SIGHUP 模式)。MT7981 真机验证:10 次重启后线程数 15 → 15,零泄漏。详见 [`docs/SIGHUP-real-device-test.md`](./docs/SIGHUP-real-device-test.md)。
- 🎯 **类型化错误 + 结构化校验** —— 5 种 sentinel 错误,全部支持 `errors.Is` 判别;`Config.Validate` 返回 `*ConfigError`(字段/值/原因),通过 `errors.As` 取字段级详情,非常适合 Web 配置 UI 做表单校验。

---

## 快速开始

### 作为库使用

```go
import (
    "context"
    "fmt"
    "log"
    "os/signal"
    "syscall"

    argus "github.com/xxl6097/argusd"
)

func main() {
    ctx, stop := signal.NotifyContext(context.Background(),
        syscall.SIGINT, syscall.SIGTERM)
    defer stop()

    w := argus.New(
        argus.OnFetcherDetected(func(k argus.FetcherKind) {
            log.Printf("数据源: %s", k)
        }),
    )

    err := w.Run(ctx, func(e argus.Event) {
        switch e.Kind {
        case argus.EventOnline:
            fmt.Printf("[+] %s 上线 %s\n", e.Device.MAC, e.Device.IP)
        case argus.EventOffline:
            fmt.Printf("[-] %s 离线\n", e.Device.MAC)
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

### 作为 CLI 使用

常见 OpenWrt 架构的预编译二进制发布在 [Releases 页面](https://github.com/xxl6097/argusd/releases)(amd64 / arm64 / armv5 / armv7 / mips / mipsle / mips64 / mips64le / riscv64 / 386, 全部静态链接)。

```bash
# 下载对应架构的包, 校验, 上传路由器。
VER=v1.0.1
TARGET=linux-mipsle-softfloat   # 替换为你的架构
curl -LO "https://github.com/xxl6097/argusd/releases/download/${VER}/argusd_${VER}_${TARGET}.tar.gz"
curl -LO "https://github.com/xxl6097/argusd/releases/download/${VER}/SHA256SUMS"
sha256sum -c SHA256SUMS --ignore-missing
tar -xzf argusd_${VER}_${TARGET}.tar.gz
scp argusd_${VER}_${TARGET}/argusd root@192.168.1.1:/tmp/argusd
ssh root@192.168.1.1 '/tmp/argusd'
```

或从源码构建:

```bash
# 跨编译到 OpenWrt (以 aarch64 路由器为例)。
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
    go build -trimpath -ldflags="-s -w" \
    -o argusd ./cmd/argusd

scp argusd root@192.168.1.1:/tmp/
ssh root@192.168.1.1 '/tmp/argusd'
```

输出示例:

```
2026/05/09 18:40:21 数据源: ahsapd
MAC 地址             IP 地址          主机名            厂商     类型    信号         无线
──────────────────────────────────────────────────────────────────────────────────────────
2C:CF:67:1D:27:AC    192.168.1.11     raspberrypi       rasp..   PC      -            wired
B0:FC:36:32:94:61    192.168.1.5      lenovo            DESK..   Phone   -38(极强)    5G/avgb-5G
BA:79:97:73:89:8D    192.168.1.213    BA799773898D      -        Phone   -44(极强)    5G/avgb-5G
──────────────────────────────────────────────────────────────────────────────────────────
共 4 台设备在线 (WiFi: 3, 有线: 1)

[2026-05-09 18:42:03] [系统日志] 无线接入 BA:79:...
[2026-05-09 18:42:03] [系统日志] DHCP分配 BA:79:... IP=192.168.1.213
[2026-05-09 18:42:03] [事件]    上线    BA:79:... 192.168.1.213 iPhone -44(极强) 5G/avgb-5G
```

---

## Web UI · 内置仪表盘 (v0.13.0+)

Argus 在 `argusweb` 子包内置了零依赖的 HTTP + SSE 仪表盘:单一嵌入式 HTML、原生 JS、移动端自适应、纯中文界面 (v0.15.1)。`argusd` 加 `-listen :8080` 即可启动,或在你自己的 HTTP 服务中挂载 `argusweb.NewServer`。

### 界面概览

**桌面端主视图** —— 左侧设备表(状态/MAC/IP/主机名/厂商/信号/类型 七列,IP 列带 🔒 静态标记和 📌 一键预约按钮,主机名列带 ✎ 重命名按钮),右侧实时事件流(SSE 推送),右上角是连接状态、在线/离线计数和系统按钮(重启网络/重启路由器)。

![桌面端](./docs/images/dashboard-desktop.png)

**静态 IP 弹窗** —— 点 📌 进入,可直接指定 IP、填名称(支持中文/空格);勾选"立即生效(重启 WiFi)"则执行 `wifi reload` / `ahsapd restart` 让所有客户端瞬断 3~5 秒自动重连,新 IP 立刻生效;不勾选则只写配置 + 尝试踢该设备,适用于厂商固件支持单机踢的环境。

![静态 IP 弹窗](./docs/images/dashboard-static-ip.png)

**别名重命名** —— 点 ✎ 进入行内重命名表单,允许中文、空格、点号、连字符,空字符串即清除别名;持久化到 `aliases.json`(原子写入)。

![重命名](./docs/images/dashboard-rename.png)

**IP 冲突一键替换** —— 当目标 IP 已被其它 MAC 预留时,后端返回 `409 Conflict`,前端弹出确认框并标明占用者 MAC;点"确定"自动 DELETE 原占用者再重试 POST,点"取消"两端配置都不变。

![IP 冲突](./docs/images/dashboard-ip-conflict.png)

**移动端(宽度 ≤ 640px)** —— 自动切卡片布局,每台设备一张卡,MAC / 状态徽章 / 主机名 / 厂商 / 无线 / 信号 自上而下排列,适合手机查看与操作。

![移动端](./docs/images/dashborad-mobile.png)

### 功能清单

| 功能 | 说明 | 引入版本 |
|---|---|---|
| 实时设备表 | SSE 推送;MAC / IP / 主机名 / 品牌 / 类型 / 信号 / 无线 / 状态 8 列实时刷新 | v0.13.0 |
| 在线/离线列 | 离线设备按 `WithOfflineRetention` 保留(默认 7 天 / 512 条);以"X 分钟前"相对时间显示 | v0.13.3 |
| 移动端自适应 | 640px 以下自动切换卡片布局 | v0.13.1 |
| 响应式列宽 | `table-layout:auto` + 每列 min-width:屏幕够宽时列自动撑满,窄屏才截断并保留 hover tooltip | v0.15.5 |
| 抖动合并 | 10 秒内的 OFFLINE→ONLINE 抖动合并为一次 RECONNECT | v0.13.2 |
| 厂商列 | OUI 品牌查询,超长省略号显示并悬停提示 | v0.15.0 |
| 设备别名 | ✎ 行内重命名,允许中文/空格/点号,JSON 持久化、原子写 | v0.14.0 / v0.15.4 |
| 静态 IP 预留 | 📌 按钮弹窗预约,UCI 持久化,可选"立即生效"执行配置重载 + 租约清理 + ARP 清理 + 踢设备 | v0.15.0 / v0.15.2 / v0.15.7 |
| IP 冲突保护 | 目标 IP 已占用时返回 409,UI 提供一键"替换"(先删旧再改) | v0.15.3 / v0.15.5 |
| 一键修复 | `POST /api/dhcp?purge_argus=1` 清除所有 `dhcp.argus_*` 段,用于配置被污染时恢复 | v0.15.3 |
| 立即生效(可选) | 弹窗里勾选后执行 `wifi reload` / 重启 ahsapd,所有客户端秒级重连;用于厂商固件不支持单机踢的场景 | v0.15.8 |
| 系统按钮 | 右上角 "重启网络"(软重启,5-15 秒 LAN 瞬断,配置保留)和 "重启路由器"(硬重启,30-60 秒全断)两个按钮,各自带确认对话框 | v0.15.9 |
| 写操作鉴权 | 默认仅放行环回与内网,可自定义;覆盖所有写操作(别名 / DHCP / 系统) | v0.14.0 |

### UI 细节

- **状态徽章** · 连接状态(`已连接` / `重连中…`)、在线/离线计数常驻右上角。
- **🔒 图标** · 已配置静态 IP 的设备 IP 前显示锁符,hover 文字"已静态分配"。
- **📌 按钮** · 点开静态 IP 弹窗;若该 MAC 已预留,底部会多出红色"移除"按钮。
- **✎ 按钮** · 点击进入行内重命名表单,回车保存,Esc 取消,空字符串即清除别名。
- **事件徽章颜色** · `上线`/`重连` 绿色、`离线`/`抖动` 红色、`变更` 黄色。
- **长文本 hover** · 任何列被 ellipsis 截断后,鼠标悬停显示完整内容。
- **Toast 反馈** · 保存静态 IP 后底部弹出多行状态条:`已重载 / 已清除旧租约 / 已清除 ARP 缓存 / 已踢出 / 已重启 WiFi`,一眼看懂服务端到底做了什么。
- **离线设备仍可管理** · 离线条目半透明显示,但 ✎ / 📌 按钮仍可点,可以提前为不在线的设备设置别名和静态 IP。

### 启动

```bash
# CLI: 在所有网卡的 8080 端口监听
./argusd -listen :8080 \
         -aliases /etc/argusd/aliases.json   # 可选: 启用别名存储
# 浏览器访问 http://<router-ip>:8080/
```

或在 Go 代码里挂载:

```go
w := argus.New(argus.WithFetcher(...))

aliases := argusweb.NewAliasStore("/etc/argusd/aliases.json")
dhcp, _ := argusweb.NewUCIDHCPManager() // 非 OpenWrt 主机返回 ErrDHCPManagerUnavailable

srv := argusweb.NewServer(w,
    argusweb.WithAliases(aliases),
    argusweb.WithDHCPManager(dhcp),
    argusweb.WithOfflineRetention(7*24*time.Hour),
    argusweb.WithOfflineMax(512),
    argusweb.WithWriteAuth(func(r *http.Request) bool {
        // 自定义鉴权
        return r.Header.Get("X-Token") == os.Getenv("ARGUS_TOKEN")
    }),
)
w.RegisterEventHandler(srv.OnEvent) // 让 SSE 流转发事件
go http.ListenAndServe(":8080", srv)
```

### HTTP API

所有响应均为 JSON,写操作受 `WithWriteAuth` 控制(默认环回 + RFC1918 放行,其它返回 403)。

| 路由 | 方法 | 说明 |
|---|---|---|
| `/` | GET | 仪表盘 HTML(单文件嵌入) |
| `/api/devices` | GET | `{count, online, offline, capabilities:{aliases,dhcp}, devices:[...]}`;每行含 `status` / `offline_at_ms` / `alias` |
| `/api/events` | GET | SSE 流,事件名 = `EventKind.String()`(`ONLINE` / `OFFLINE` / `CHANGE`) |
| `/api/aliases` | GET / POST / DELETE | MAC ↔ 友好名 CRUD;`503` 表示未挂 `WithAliases` |
| `/api/dhcp` | GET / POST / DELETE | 静态 DHCP 预留 CRUD;`503` 表示未挂 `WithDHCPManager`;POST/DELETE 支持 `?restart_wifi=1` 触发"立即生效"(v0.15.8+) |
| `/api/dhcp?purge_argus=1` | POST | 一键清除全部 `dhcp.argus_*` 段(恢复工具,v0.15.3+) |
| `/api/system/restart-network` | POST | `/etc/init.d/network restart` 软重启网络服务(v0.15.9+) |
| `/api/system/reboot` | POST | `/sbin/reboot` 彻底重启路由器(v0.15.9+) |

POST `/api/dhcp` 错误码:

- `400` —— MAC / IP / name 非法
- `403` —— 写操作鉴权未通过
- `409` —— 目标 IP 已被其它 MAC 预留;body `{error, ip, owner_mac}` 指明冲突方 (v0.15.3+)
- `503` —— 服务未挂载 DHCPManager

`applyReport`(所有 DHCP 写操作响应 `apply` 字段)包含的状态:`reloaded[]` · `pruned[]` · `arp_flushed` · `kicked` · `wifi_restarted`,前端据此渲染 toast。

完整 wire shape 见 [`STABILITY.md`](./STABILITY.md)(自 v0.13.0 起为稳定 API 表面)。

### DHCP 后端兼容性

`NewUCIDHCPManager()` 仅在 OpenWrt(任何带 `uci` CLI 的系统)上可用;其它平台返回 `ErrDHCPManagerUnavailable`。已在 MediaTek MT7981 / C-Life 厂商固件(odhcpd)与官方 OpenWrt(dnsmasq)上验证。

> **注意双 DHCP 服务器** · 如果 LAN 里有"旁路由"(iStoreOS / OpenClash 等)默认开启 DHCP,会和主路由抢答,导致设备网关随机变成旁路由 IP、静态预留间歇失效。排查:主路由上 `ip neigh` 看各设备网关;修复:在旁路由上 `uci set dhcp.lan.ignore=1 && uci commit dhcp && /etc/init.d/dnsmasq restart`。

---

## 架构

六路数据进入融合引擎,由 Watcher 统一产出三类回调:业务事件 / 决策观测 / 错误上报。

```
                       ┌──────────────┐
                       │   logread    │ ← 实时内核事件
                       │      -f      │   (关联/断开/Deauth/DHCPACK)
                       └──────┬───────┘
                              │
 ┌─ ubus call ────┐    ┌──────┼──────┐     ┌─ ARP 状态 ───┐
 │ ahsapd.sta 或  │ →  │   融合       │  ←  │ ip neigh     │
 │ hostapd.<iface>│    │   引擎       │     │ FAILED/OK    │
 └────────────────┘    │             │     └──────────────┘
                       │             │
 ┌─ DHCP 租约 ────┐    │             │     ┌─ ICMP 探测 ──┐
 │ /tmp/dhcp.     │ →  │             │  ←  │ ping -c 1    │
 │   leases       │    │             │     │ -W 1         │
 └────────────────┘    └──────┬──────┘     └──────────────┘
                              │
                        ┌─────▼──────┐
                        │  Watcher   │  ← diff + 冷却 + 抖动抑制
                        └─────┬──────┘
                              │
                 ┌────────────┼────────────┐
                 ▼            ▼            ▼
            EventHandler  DecisionHandler  ErrorHandler
            业务事件       决策观测         错误上报
```

完整判定流程参见 [`ONLINE.md`](./ONLINE.md) 和 [`OFFLINE.md`](./OFFLINE.md)。

---

## API 速查

| 类型 | 用途 |
|------|------|
| `argus.Watcher` | 主入口:`New(opts...) *Watcher`, `Run`, `Stop`, `List`, `Known`, `EnsureFetcher`, `FetcherKind` |
| `argus.Event` / `EventKind` | 业务事件(上线 / 离线 / 变更) |
| `argus.Decision` / `DecisionKind` | 内部判定链路(17 种分支) |
| `argus.Config` / `argus.ConfigError` | 阈值配置 + 结构化校验错误(v0.9.0+) |
| `argus.Fetcher` | 数据源接口,自动探测 |
| `argus.Prober` | 活性探测,默认 `ICMPProber{Timeout: 1s}` |
| `argus.Hint` / `argus.HintSource` / `argus.DefaultHintSource` | 可注入的补全来源(v0.7.0+)—— 非 OpenWrt 平台的 DHCP/ARP |
| `argus.LoggerHandler` / `LogLevel` / `LogAttr` | 结构化日志钩子(v0.9.0+) |
| `argus.SpanRecorder` / `SpanRecorderFunc` | 分布式追踪钩子(v0.12.0+) |
| `argus.SyslogEvent` | 原始系统日志解析结果 |
| `argus.DetectLocalLocation()` | 解析 `/etc/TZ` → `*time.Location`,不修改全局状态 |
| `argus.SetupLocalTimezone()` | *已废弃*,修改全局 `time.Local` |
| Sentinel 错误 | `ErrHandlerRequired` / `ErrInvalidConfig` / `ErrNoFetcher` / `ErrFetchFailed` / `ErrAlreadyRunning`,可用 `errors.Is` 判别 |
| `github.com/xxl6097/argusd/argusmetrics` | 零依赖 `Counters` + `LabeledCounters`(v0.7.0 / v0.10.0+) |
| `github.com/xxl6097/argusd/argustest` | 下游测试用的数据源 fixture(v0.6.0+) |

函数式选项:

```go
argus.WithConfig(cfg)                      // 覆盖默认
argus.WithFetcher(custom)                  // 注入自定义数据源
argus.WithProber(nil)                      // 关闭活性探测
argus.WithBaseline(old.Known())            // 热重载保留设备表
argus.WithHintSource(custom)               // 自定义补全源 (v0.7.0+)
argus.WithLogger(h)                        // 结构化日志 (v0.9.0+)
argus.WithSpanRecorder(r)                  // 分布式追踪 (v0.12.0+)
argus.OnFetcherDetected(func(k) {...})     // 自动探测回调
argus.WithDecisionHandler(func(d) {...})   // 决策观测
```

---

## 配置调优

所有阈值集中在 `argus.Config`,传零值保留默认。

```go
w := argus.New(argus.WithConfig(argus.Config{
    // 轮询节奏
    PollInterval:  1 * time.Second,   // 默认 1s
    OfflineMisses: 5,                 // 默认 5
    FetchTimeout:  3 * time.Second,   // 默认 3s

    // 抗抖动
    OfflineCooldown:            90 * time.Second,
    CooldownReleaseRSSI:        -65,
    WeakRSSI:                   -80,
    ExtremelyWeakRSSI:          -88,
    WeakMissThreshold:          5,
    ExtremelyWeakMissThreshold: 2,
    FlapSuppressionWindow:      30 * time.Second,
}))
```

使用建议:

| 场景 | 建议配置 |
|------|----------|
| 激进响应、容忍噪音 | `FlapSuppressionWindow: 0`, `OfflineCooldown: time.Nanosecond` |
| 家庭自动化 | 保留默认 |
| 拥挤无线环境 | `WeakRSSI: -75`, `WeakMissThreshold: 10` |
| 完全信任 AP 关联表 | `WithProber(nil)` |

---

## 可观测性

Watcher 对外暴露五路 opt-in 可观测性通道, 不同受众用不同通道。

| 通道 | 类型 | 频率 | 用途 |
|------|------|------|------|
| `EventHandler`(`Run` 参数) | `Event` | 稀疏 | 业务逻辑(home/away 自动化) |
| `ErrorHandler`(`Run` 参数) | `error` | 罕见 | 非致命错误 |
| `WithDecisionHandler` | `Decision` | 密集 | 调参 / 排障 |
| `WithLogger`(v0.9.0+) | `LogLevel` + attrs | 生命周期 + 异常 | slog/zap/zerolog 桥接 |
| `WithSpanRecorder`(v0.12.0+) | span start/finish | 每次 Run + 每次断开 | OTel / Datadog 追踪 |

外加 `argusmetrics` 子包用于进程内计数器聚合(约 10 行就能桥接到 Prometheus / OTLP;详见 godoc)。

需要镜像原始系统日志时,直接调用 `WatchSyslog(ctx, func(SyslogEvent), onError)`,它是独立函数,非 Watcher 选项。

决策跟踪示例:

```
[决策] CONNECT_HINT     BA:79:... (IP=192.168.1.213)
[决策] CONNECT_EMIT     BA:79:... (IP=192.168.1.213)
[事件] 上线             BA:79:... 192.168.1.213 iPhone -44(极强) 5G/avgb-5G
[决策] POLL_WEAK_MISS   BA:79:... (RSSI=-82 misses=3/5)
[决策] POLL_WEAK_MISS   BA:79:... (RSSI=-85 misses=5/5)
[决策] OFFLINE_EMIT     BA:79:... (via=poll RSSI=-85)
[事件] 离线             BA:79:...
```

不注册 `DecisionHandler` 时完全零成本:不分配对象、不调用 `time.Now()`。

---

## 路线图

- [x] ahsapd / hostapd 双数据源 + 自动探测
- [x] `logread -f` 实时日志流
- [x] ICMP 活性探测 + 并发信号量
- [x] 冷却期 + 抖动抑制
- [x] 决策回调可观测性
- [x] 竞态检测全部通过(多 Go 版本矩阵 1.21-1.25)
- [x] 生命周期:`Stop` + 热重启(v0.5.0)
- [x] 可移植性:`HintSource` 抽象(v0.7.0)
- [x] 指标:`argusmetrics.Counters` + `LabeledCounters`(v0.7.0 / v0.10.0)
- [x] 结构化日志钩子 `WithLogger`(v0.9.0)
- [x] 结构化配置校验错误 `ConfigError`(v0.9.0)
- [x] 分布式追踪钩子 `SpanRecorder`(v0.12.0)
- [x] Syslog / DHCP 租约解析器 fuzz 目标(v0.12.0)
- [x] 内置 Web UI(HTTP + SSE, v0.13.0)
- [x] 设备别名,允许中文(v0.14.0 / v0.15.4)
- [x] 静态 IP 预留 + 立即生效(v0.15.0 / v0.15.2 / v0.15.7 / v0.15.8)
- [x] IP 冲突 409 + 一键替换 + 一键修复(v0.15.3 / v0.15.5)
- [x] 系统接口:重启路由器 + 重启网络(v0.15.9)
- [x] **v1.0 已发布** —— SemVer v1 规则下稳定表面锁定
- [ ] 直连 `ubus` socket,跳过 CLI
- [ ] 仅 IPv6 设备支持
- [ ] Home Assistant `device_tracker` 桥接
- [ ] Prometheus `/metrics` 出口(argusweb 桥接)

---

## 兼容性

| 平台 | 数据源 | 状态 |
|------|--------|------|
| MediaTek MT7981 厂商固件 | ahsapd | ✅ 参考目标 |
| OpenWrt 23.05+ 官方 | hostapd.* | 🧪 待实测 |
| 任意带 `logread`+`ubus` 的 Linux | 仅 syslog | ⚠️ 仅事件,无设备列表 |

Go 1.21+(N-2 策略:当前版本 + 前两个 minor 版本)。不使用 cgo,可跨编译到任何 OpenWrt 支持的 GOOS/GOARCH。

---

## 贡献

欢迎 PR,详见 [`CONTRIBUTING.md`](./CONTRIBUTING.md)。提交前请本地通过:

```bash
go vet ./...
go test -race ./...
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./cmd/argusd
```

---

## 更多文档

- [`CHANGELOG.md`](./CHANGELOG.md) —— 版本历史(新特性 & Bug 修复)
- [`STABILITY.md`](./STABILITY.md) —— API 稳定性承诺与 v1.0 条件
- [`ONLINE.md`](./ONLINE.md) —— 上线判定深度解析
- [`OFFLINE.md`](./OFFLINE.md) —— 离线与冷却机制解析
- [`docs/SIGHUP-real-device-test.md`](./docs/SIGHUP-real-device-test.md) —— v0.5.0 SIGHUP 热重载真机测试报告
- [`docs/blog/ios-static-ip.md`](./docs/blog/ios-static-ip.md) —— 调试故事:OpenWrt + iPhone 静态 IP 不生效的三种死法
- [GoDoc](https://pkg.go.dev/github.com/xxl6097/argusd) —— API 文档

---

## 许可证

MIT © 2026 —— 见 [`LICENSE`](./LICENSE)

---

*"每一台设备,每一次事件,每一只眼睛都不闭上。"*
