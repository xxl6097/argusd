# 上线判定机制详解

本文档基于当前代码（`watcher.go` / `decision.go` / `logwatch.go` / `enrich.go`）全面分析设备上线（`EventOnline`）判定的完整流程。所有冷却期 / 抖动抑制 / 信号强度阈值均集中在 `Config`，默认值见 `DefaultConfig()`。

---

## 架构概览：双通道 + 冷却期 + 抖动抑制

```
通道 A  系统日志（毫秒级）                   通道 B  周期轮询（秒级兜底）
────────────────────────────               ──────────────────────────────
logread -f  (runSyslog goroutine)          每 PollInterval=1s
    │                                          │
  New Sta:<mac>       (MACTABLE_INSERT)    Fetcher.Fetch(ctx)
  AP SETKEYS DONE     (WPA_COMPLETE)       filterAlive(ping)
  DHCPACK <ip> <mac>  (DHCP_ACK)               │
    │                                      diff(known, cur, apRaw)
  syslogHint{Disconnect:false, MAC, IP}        │
    │                                          │
    ▼  写入 syslogHints channel (cap=256)       │
    │                                          │
  runSyslogConsumer goroutine                  │
  (最多 16 并发 worker)                        │
    │                                          │
  handleConnectHint                      diff 发现新 MAC
    │                                          │
  emitConnectEvent ◄──────── 统一发射点 ───────►
    │
  ① known 中？ → 跳过
  ② 冷却期 + 弱信号 → 静默 (COOLDOWN_SUPPRESS_ONLINE)
  ③ 冷却期 + 强信号/有线 → 清除冷却 (COOLDOWN_CLEARED)
  ④ 抖动窗口内同类 Online → 静默 (FLAP_SUPPRESS_ONLINE)
  ⑤ 正常发射 → EventOnline (CONNECT_EMIT / via=poll)
```

两条通道通过 `stateMu` 串行化对 `known` / `misses` / `offlineCooldown` / `lastEventAt` 的访问。**哪条通道先感知，就先发射事件，另一条被 `known[mac]` 或 `FlapSuppressionWindow` 挡住。**

---

## 系统日志中的上线事件序列

设备连接 WiFi 的完整内核日志序列（路由器实测）：

```
t=0.000s  kern  MacTableInsertEntry(): New Sta:ba:79:97:73:89:8d    ← MACTABLE_INSERT
t=0.002s  kern  ap_cmm_peer_assoc_req_action(): Recv Assoc from STA
t=0.003s  kern  wifi_sys_conn_act() Addr=ba:79:97:73:89:8d          ← WIFI_CONNECT
t=0.170s  kern  WPABuildPairMsg1 <=== send Msg1 of 4-way
t=0.180s  kern  WPABuildPairMsg3 <=== send Msg3 of 4-way
t=0.185s  kern  WifiSysUpdatePortSecur
t=0.190s  kern  AP SETKEYS DONE(rax0) from ba:79:97:73:89:8d        ← WPA_COMPLETE
t=1.000s  dhcp  DHCPACK(br-lan) 192.168.1.213 ba:79:97:73:89:8d     ← DHCP_ACK
```

触发 `SyslogKind.IsConnect() == true` 的三类事件（`logwatch.go:94-96`）：

| 事件类型 | 正则 | 日志样例 | 触发时机 |
|---------|------|---------|---------|
| `SyslogMacTableInsert` | `New Sta:([0-9a-fA-F:]{17})` | `MacTableInsertEntry: New Sta:ba:79:...` | AP 关联表插入（最早）|
| `SyslogWPAComplete` | `AP SETKEYS DONE\((\w+)\).*from\s+(...)` | `AP SETKEYS DONE(rax0) from ba:79:...` | 4-way 握手完成 |
| `SyslogDHCPAck` | `DHCPACK\([\w-]+\)\s+([\d.]+)\s+(...)` | `DHCPACK(br-lan) 192.168.1.213 ba:79:...` | dnsmasq 分配 IP |

> 注意：`SyslogWifiConnect`（`wifi_sys_conn_act`）**不**归入 `IsConnect`，因为它早于 4-way 握手，可能出现在关联失败前。

---

## 通道 A：系统日志驱动上线

### 事件分发（`watcher.go:runSyslog`）

```go
WatchSyslog(ctx, func(e SyslogEvent) {
    mac := normalizeMAC(e.MAC)
    if mac == "" { return }
    var h syslogHint
    h.MAC = mac
    if e.Kind.IsDisconnect() {
        h.Disconnect = true
    } else if e.Kind.IsConnect() {
        h.Disconnect = false
        h.IP = e.IP        // 仅 DHCP_ACK 时有值
    } else {
        return
    }
    select {
    case w.syslogHints <- h:          // channel 容量 256
    default:
        atomic.AddUint64(&w.droppedHints, 1)   // 满时丢弃, 30s 聚合上报
    }
}, onError)
```

### 消费与并发（`watcher.go:runSyslogConsumer`）

```go
sem := make(chan struct{}, 16)    // 最多 16 个并发 handle goroutine
for h := range w.syslogHints {
    sem <- struct{}{}
    go func(h syslogHint) {
        defer func() { <-sem }()
        if h.Disconnect {
            w.handleDisconnectHint(ctx, h.MAC, onEvent)
        } else {
            w.handleConnectHint(ctx, h, onEvent, onError)
        }
    }(h)
}
```

每个 hint 在独立 goroutine 中处理，`handleDisconnectHint` 的 500ms 等待不会阻塞后续事件；信号量 16 防止 goroutine 爆炸。

### 上线处理（`watcher.go:handleConnectHint`）

```go
func (w *Watcher) handleConnectHint(ctx, h, onEvent, onError) {
    emitDecision(CONNECT_HINT, mac, "IP=<dhcp_ip>")

    // ① 防重复: 已在 known 中 → 跳过
    if _, alreadyKnown := w.known[h.MAC]; alreadyKnown {
        emitDecision(CONNECT_SKIP_KNOWN, mac, "")
        return
    }

    // ② 直接用 DHCP IP + /tmp/dhcp.leases + ip neigh 构建基础记录
    //    重要: 不调用 Fetcher.Fetch ——
    //    WiFi 握手期间路由器 CPU 紧张, ubus call 经常 "signal: killed",
    //    盲目重试只会拖慢流程、丢失事件。
    hints := loadHints(ctx)
    d := applyHints(Device{
        MAC:      h.MAC,
        IP:       h.IP,
        LastSeen: time.Now(),
    }, hints[h.MAC])

    w.emitConnectEvent(d, onEvent)
}
```

> 关键变化：与早期版本不同，**通道 A 不再调用 `Fetcher.Fetch`**。路由器在 WiFi 握手期间 CPU 极度紧张，ubus 调用会反复被内核 kill。现在只用 `syslog → DHCP/ARP hints` 发射基础上线事件；`RSSI / Radio / SSID / Vendor` 等字段由后续 `diff` 轮询通过 `EventChange` 补齐。

### 统一发射点（`watcher.go:emitConnectEvent`）

所有上线事件——无论是通道 A 还是通道 B——最终都经过以下检查链：

```go
func (w *Watcher) emitConnectEvent(d Device, onEvent EventHandler) {
    now := time.Now()

    // ① 双重检查 known (handle 与 diff 并发时可能已加入)
    if _, already := w.known[d.MAC]; already {
        emitDecision(CONNECT_SKIP_KNOWN, d.MAC, "double-check")
        return
    }

    // ② 冷却期判定
    if cdTime, inCD := w.offlineCooldown[d.MAC]; inCD &&
        now.Sub(cdTime) < cfg.OfflineCooldown {
        // 冷却期内 + RSSI 弱 → 静默更新 known, 刷新 cooldown
        if d.RSSI != 0 && d.RSSI < cfg.CooldownReleaseRSSI {  // 默认 -65
            w.offlineCooldown[d.MAC] = now    // 刷新: 防止自然过期后走完整离线
            w.known[d.MAC] = d
            emitDecision(COOLDOWN_SUPPRESS_ONLINE, d.MAC, "RSSI=...")
            return
        }
        // 冷却期内 + 强信号/有线 (RSSI=0) → 清除冷却, 继续发射
        delete(w.offlineCooldown, d.MAC)
        emitDecision(COOLDOWN_CLEARED, d.MAC, "RSSI=...")
    }

    // ③ 抖动抑制: 窗口期内同类 Online → 静默更新
    if w.shouldSuppressFlap(d.MAC, EventOnline, now) {    // 30s 窗口
        w.known[d.MAC] = d
        emitDecision(FLAP_SUPPRESS_ONLINE, d.MAC, "")
        return
    }

    // ④ 正常发射
    w.known[d.MAC] = d
    w.recordEvent(d.MAC, EventOnline, now)
    emitDecision(CONNECT_EMIT, d.MAC, "IP=...")
    onEvent(Event{Time: now, Kind: EventOnline, Device: d})
}
```

### 防重复机制

同一设备连接时通常产生三条日志（New Sta → WPA_COMPLETE → DHCP_ACK），只有第一条通过 `known[mac]` 检查：

```
t=0.000s  New Sta       → handleConnectHint → 不在 known → emitConnectEvent → EventOnline
t=0.190s  WPA_COMPLETE  → handleConnectHint → 已在 known → CONNECT_SKIP_KNOWN
t=1.000s  DHCPACK       → handleConnectHint → 已在 known → CONNECT_SKIP_KNOWN
```

---

## 通道 B：周期轮询上线（兜底 + 字段补齐）

### 数据拉取（`watcher.go:fetchWithAPSet`）

```
fetchWithAPSet(ctx)
  ├── Fetcher.Fetch(ctx)         → apRaw  (AP 关联表全部设备, 含 RSSI/Radio/SSID)
  └── filterAlive(prober)        → alive  (仅 ping 可达的子集)
```

### 上线分支（`watcher.go:diff` 前半段）

```go
for mac, d := range cur {
    delete(misses, mac)
    _, ok := known[mac]
    switch {
    case !ok:
        // 冷却期检查
        if cdTime, inCD := cooldown[mac]; inCD && now.Sub(cdTime) < cfg.OfflineCooldown {
            if d.RSSI != 0 && d.RSSI < cfg.CooldownReleaseRSSI {
                cooldown[mac] = now            // 刷新冷却
                known[mac] = d                 // 静默更新
                emitDecision(COOLDOWN_SUPPRESS_ONLINE, mac, "RSSI=...")
                continue
            }
            delete(cooldown, mac)
            emitDecision(COOLDOWN_CLEARED, mac, "RSSI=...")
        }
        emitIfNotSuppressed(EventOnline, d)    // 含 FlapSuppressionWindow
    default:
        // 已知设备: 检查字段变化 (不经 FlapSuppression)
        if cs := changedFields(prev, d); len(cs) > 0 {
            onEvent(Event{Kind: EventChange, Device: d, Changes: cs})
        }
    }
    known[mac] = d
}
```

轮询分支里的 `emitIfNotSuppressed` 内联实现了与 `emitConnectEvent` 相同的 `FlapSuppressionWindow` 逻辑，便于 diff 在持有 `stateMu` 的情况下直接判定。

### 字段变更检测（`watcher.go:changedFields`）

| 字段 | 触发 `EventChange` | 说明 |
|------|:---:|------|
| `IP` | ✅ | DHCP 续约 / 重分配 |
| `Hostname` | ✅（仅空→非空）| 空变非空触发，非空变空不触发（防抖动）|
| `Radio` | ✅ | 如 2.4G ↔ 5G 漫游 |
| `SSID` | ✅ | 切换到不同 SSID |
| `RSSI` | ❌ | 持续抖动，不触发 |
| `Channel` | ❌ | 持续变化，不触发 |
| `UpTime` | ❌ | 持续递增，不触发 |

---

## 冷却期机制（`OfflineCooldown` / `CooldownReleaseRSSI`）

**目的**：防止弱信号边缘设备反复产生 `EventOnline` ↔ `EventOffline` 抖动。

**实现**：

- 任何离线触发点（通道 A 的 `handleDisconnectHint` 或 diff 的三个离线分支）都会无条件写入 `offlineCooldown[mac] = now`
- 冷却期长度由 `Config.OfflineCooldown` 控制（默认 90s）
- 冷却期内设备重新出现时：
  - `RSSI != 0 && RSSI < CooldownReleaseRSSI`（默认 -65）→ 静默更新 `known`，**刷新** cooldown 保持抑制，不触发 `EventOnline`
  - `RSSI >= CooldownReleaseRSSI` 或 `RSSI == 0`（有线设备无 RSSI 数据）→ 清除 cooldown，正常发射
- diff 每轮开始会清理过期冷却记录（`watcher.go` line ~680）

**刷新策略的意义**：如果冷却期自然过期后设备仍处于弱信号，直接发射 `EventOnline` 会立即被下一次离线检测再次拉下线。刷新 cooldown 保持设备"隐身"直到信号恢复或彻底离开。

---

## 抖动抑制窗口（`FlapSuppressionWindow`）

**目的**：覆盖冷却期之外的中等信号快闪（例如 RSSI -70 左右短时抖动）。

**实现**：`lastEventAt[mac]` 记录每台设备最近一次**外发**事件的时刻和类型；同一 MAC 在 `FlapSuppressionWindow`（默认 30s）内产生的**同类**事件被静默。

| 条件 | 处理 |
|------|------|
| 前一次事件类型相同（Online→Online / Offline→Offline） | 静默，不发射，不更新 `lastEventAt` |
| 前一次事件类型不同（Online→Offline 或反之） | 正常发射并刷新 `lastEventAt` |
| 未发射过任何事件 | 正常发射 |
| `FlapSuppressionWindow == 0` | 完全关闭此机制 |

`FlapSuppressionWindow` 与 `OfflineCooldown` 互补：前者抑制短时快闪，后者压制长时间弱信号。

---

## 上线决策矩阵

| 场景 | known 中 | 冷却期内 | RSSI 条件 | 抖动窗口 | 结果 |
|------|:---:|:---:|:---------:|:---:|------|
| 新设备首次接入 | ❌ | ❌ | 任意 | 外 | `EventOnline` |
| 冷却期外重连 | ❌ | ❌ | 任意 | 外 | `EventOnline` |
| 冷却期内 + 弱信号 | ❌ | ✅ | < -65 | — | **静默**（刷新 cooldown）|
| 冷却期内 + 强信号 | ❌ | ✅ | ≥ -65 | 外 | 清除冷却 → `EventOnline` |
| 冷却期内 + 有线（RSSI=0）| ❌ | ✅ | == 0 | 外 | 清除冷却 → `EventOnline` |
| 抖动窗口内同类 Online | ❌ | ❌ | 任意 | 内 | **静默**（已在 30s 内上过线）|
| iPhone 息屏恢复 | ✅ | 任意 | 任意 | — | 无事件（已在 known）|
| IP / Radio / SSID / Hostname 变化 | ✅ | 任意 | 任意 | — | `EventChange` |
| 同一次连接的多条日志 | ✅ | 任意 | 任意 | — | 无事件（防重复）|

---

## 上线时延汇总

| 场景 | 通道 A 延迟 | 通道 B 延迟 | **实际延迟** |
|--------------|------------|------------|------------|
| WiFi 新设备接入 | ≈ 即时（New Sta）| 0-1s | **≈ 即时** |
| WiFi 重连（冷却期外）| ≈ 即时 | 0-1s | **≈ 即时** |
| WiFi 重连（冷却期内 + 弱信号）| —（静默）| —（静默）| **不触发** |
| WiFi 重连（冷却期内 + 强信号）| ≈ 即时 | 0-1s | **≈ 即时** |
| 有线设备（DHCP）| DHCPACK ≈ 即时 | 0-1s | **≈ 即时** |
| 有线设备（静态 IP，无 DHCP 日志）| 无日志 | 0-1s | **0-1s** |
| `logread` 失效（退化）| 不工作 | 0-1s | **0-1s** |

---

## 关键时间线

### 场景 1：WiFi 新设备首次接入

```
t=0.000s  设备发起关联
t=0.000s  kern: New Sta:ba:79:... → syslogHint 入队
t=0.001s  runSyslogConsumer 取走 hint → 启动 worker
t=0.001s  handleConnectHint:
            → CONNECT_HINT
            → 不在 known
            → loadHints + applyHints: 构建基础 Device (DHCP/ARP 可能尚无)
            → emitConnectEvent:
                → 不在冷却期, 不在抖动窗口
                → CONNECT_EMIT → onEvent(EventOnline)
                → known[mac] = d
t=0.190s  WPA_COMPLETE → handleConnectHint → CONNECT_SKIP_KNOWN
t=1.000s  DHCPACK      → handleConnectHint → CONNECT_SKIP_KNOWN
t=1.000s  diff 轮询: 已在 known → changedFields 对比 → 可能触发 EventChange
                                                       (补齐 RSSI/Radio/SSID)
```

### 场景 2：弱信号区反复上下线（冷却期 + 抖动窗口联合压制）

```
t=0s      设备在线 RSSI=-30
t=60s     RSSI 衰减到 -82, ping 不通 → POLL_WEAK_MISS (misses=1)
t=64s     POLL_WEAK_MISS (misses=5/5) → OFFLINE_EMIT + cooldown=t=64s
t=65s     RSSI=-78, 回到 cur → 冷却期内 + RSSI<-65
          → COOLDOWN_SUPPRESS_ONLINE (刷新 cooldown=t=65s)
t=72s     RSSI=-80, ping 不通 → diff 离线分支 → 冷却期内
          → COOLDOWN_SUPPRESS_OFFLINE (静默移除, 刷新 cooldown=t=72s)
...
t=162s    cooldown 过期 (90s 自 t=72s), RSSI=-50 稳定 → 正常 EventOnline
```

若设备在冷却期外但在 30s 抖动窗口内再次尝试上线，则由 `FLAP_SUPPRESS_ONLINE` 挡下。

### 场景 3：有线设备插网线

```
t=0.000s  设备插网线, 发送 DHCP Request
t=0.500s  dnsmasq: DHCPACK(br-lan) 192.168.1.x <mac> → syslogHint{IP=192.168.1.x}
t=0.500s  handleConnectHint:
            → loadHints: /tmp/dhcp.leases 可能已有 hostname
            → applyHints(Device{MAC, IP=h.IP}) → emitConnectEvent
            → RSSI=0 (有线) → 冷却期 / 抖动窗口均放行 → CONNECT_EMIT
t=1.000s  diff 轮询: 已在 known → 一般无 EventChange
```

### 场景 4：iPhone 息屏恢复

```
iPhone 息屏中: 在 apRaw 中, ping 不通, RSSI 正常 (-60)
            → diff 第二层: ping 不通 + RSSI > WeakRSSI (-80)
            → POLL_SLEEP_PROTECT: delete(misses), 保持 known
            → 无事件

iPhone 亮屏: ping 恢复 → 进入 alive → diff 发现 mac 已在 known
          → changedFields: 无变化 → 无事件
```

---

## 基线拉取（启动时）

```go
// watcher.go:Run
baseline, _ := w.fetchByMAC(ctx)
for mac, d := range baseline {
    w.known[mac] = d
}
// 启动后才开始 go runSyslog / go runSyslogConsumer / ticker 循环
```

- 启动时已在线的设备直接进入 `known`，**不**触发 `EventOnline`
- 之后通道 A / B 才开始产生事件
- 业务侧若需获取启动时的设备列表：调用 `w.List(ctx)`（会应用冷却期过滤）

---

## 数据补全与多源合并

上线事件的 `Device` 字段按优先级合并：

```
① Fetcher 原始数据 (ahsapd/hostapd)
   → MAC / IP / Hostname / Vendor / Type / Radio / SSID / RSSI / Channel /
     UpTime / AccessTime

② syslogHint 中的 DHCP IP (仅通道 A, 当 Fetcher 未返回 IP 时)

③ /tmp/dhcp.leases (enrich.go:loadDHCPLeases)
   → Hostname 主要来源 + IP

④ ip neigh show (enrich.go:loadARP)
   → IP 兜底, 跳过 FAILED/INCOMPLETE/IPv6
```

`applyHints` 只在字段为空时填入，不覆盖已有值。`loadHints` 带 5s TTL 缓存（`hintsCacheTTL`），避免每个 hint 都 fork `ip neigh show` 子进程。

---

## 决策观测（`DecisionHandler`）

通道 A / B 的每个分支都会通过 `emitDecision` 上报 `DecisionKind`，供 `WithDecisionHandler` 注册的回调消费。上线相关决策点：

| DecisionKind | 触发点 |
|---|---|
| `CONNECT_HINT` | 收到接入日志 hint |
| `CONNECT_SKIP_KNOWN` | 已在 known，跳过 |
| `CONNECT_EMIT` | 正式发射 EventOnline |
| `COOLDOWN_SUPPRESS_ONLINE` | 冷却期 + 弱信号，静默 |
| `COOLDOWN_CLEARED` | 冷却期内信号恢复，清除 |
| `FLAP_SUPPRESS_ONLINE` | 抖动窗口内同类事件压制 |

未注册 `DecisionHandler` 时完全零成本：`emitDecision` 发现 `w.onDecision == nil` 立即返回，不分配对象、不调用 `time.Now()`。

---

## 边界情况

### 1. MAC 地址格式异常

`normalizeMAC` 把紧凑大写 `B0FC36329461` 转为 `b0:fc:36:32:94:61`；长度非 12 位返回空串，`handleConnectHint` 丢弃。

### 2. MAC 随机化（iOS/Android 隐私 MAC）

旧 MAC 下线 → 新 MAC 与旧 MAC 完全不同 → 冷却期不适用 → 新 MAC 正常 `EventOnline`；业务表现为"一台旧设备下线 + 一台新设备上线"。

### 3. `syslogHints` channel 已满

channel 容量 256。满时 `select default` 丢弃新 hint，累加 `droppedHints` 原子计数；每 30s 若有丢弃，通过 `onError` 上报 "最近 30s 内因缓冲区满丢弃 N 条 syslog 事件"。通道 B 在 ≤1s 内兜底。

### 4. `handleConnectHint` 与 diff 并发发射同一 MAC

两侧都持 `stateMu`。`emitConnectEvent` 和 diff 内联的发射逻辑都会二次检查 `known[d.MAC]`；先到者写入 `known` 并发射，后到者看到 `already` 直接 skip。

### 5. `logread -f` 进程退出

`runSyslog` 的 goroutine 返回，`onError` 上报 "系统日志监听异常退出"。通道 A 彻底退化，完全依赖通道 B（延迟从 ≈ 即时 变为 0-1s）。

### 6. 缓冲区丢弃的 hint

即使 hint 被丢弃，通道 B 每秒都会把这台设备拉进 `cur`，在下一轮 diff 中作为 "not in known" 触发 `EventOnline`；最大延迟 = `PollInterval`（默认 1s）。

---

## 相关代码索引

| 文件 | 函数 / 段落 | 职责 |
|------|-----------|------|
| `watcher.go` | `New` / `Watcher` 字段 | 初始化 `syslogHints`（cap=256）/ `offlineCooldown` / `lastEventAt` |
| `watcher.go` | `runSyslog` | `WatchSyslog` 回调，过滤 IsConnect，写 channel，丢弃计数 |
| `watcher.go` | `runSyslogConsumer` | 信号量（16）+ 并发 worker，分派 connect/disconnect |
| `watcher.go` | `handleConnectHint` | 不调 Fetcher，用 DHCP/ARP hints 构建基础 Device |
| `watcher.go` | `emitConnectEvent` | 统一发射点：防重复 / 冷却期 / 抖动抑制 |
| `watcher.go` | `shouldSuppressFlap` / `recordEvent` | `FlapSuppressionWindow` 实现 |
| `watcher.go` | `diff`（前半段） | 轮询发现新 MAC，同样走冷却 / 抖动抑制 |
| `watcher.go` | `changedFields` | 字段差异检测，生成 `EventChange` |
| `decision.go` | `DecisionKind` 常量 | 16 种决策点定义（CONNECT_*/COOLDOWN_*/FLAP_*） |
| `logwatch.go` | `IsConnect` | 判定 3 类事件为接入信号 |
| `logwatch.go` | 正则定义 | `reNewSta` / `reWPA` / `reDHCPAck` |
| `enrich.go` | `loadHints` / `applyHints` | `/tmp/dhcp.leases` + `ip neigh show` 补全 |
| `fetcher.go` | `AhsapdFetcher` / `HostapdFetcher` | ubus 拉取 |
| `detect.go` | `DetectFetcher` | 自动识别 ahsapd / hostapd |

---

## 总结

上线判定 = **系统日志即时通知（通道 A）** + **周期轮询兜底（通道 B）** + **冷却期压制（长时）** + **抖动抑制（短时）**

| 维度 | 通道 A | 通道 B |
|------|--------|--------|
| 延迟 | ≈ 即时 | 0-1s |
| 数据来源 | syslog hint + `loadHints`（DHCP/ARP） | `Fetcher.Fetch` + ping 过滤 |
| 字段完整度 | 基础（MAC/IP/Hostname） | 完整（+RSSI/Radio/SSID/Vendor…）|
| 协作 | 先发射基础 Online | 后续通过 `EventChange` 补齐字段 |
| 冷却期检查 | ✅（`emitConnectEvent`） | ✅（diff 内联） |
| 抖动抑制 | ✅（`shouldSuppressFlap`） | ✅（diff 内联） |
| 依赖 | `logread -f` 进程 | 每秒 ubus + ping |

**核心设计**：
1. **通道 A 不调 Fetcher**——避免 WiFi 握手期间 ubus 被 kill，用 DHCP/ARP 快速发射基础上线；通道 B 通过 `EventChange` 补齐字段。
2. **`emitConnectEvent` 是唯一发射点**——所有 Online 都经它做冷却期 / 抖动抑制二次检查，防止通道 A / B 并发重复发射。
3. **冷却期 + 抖动窗口互补**——前者压制长时间弱信号抖动（90s / -65 dBm），后者抑制中等信号快闪（30s 窗口）。
4. **双通道互补**——任一失效，另一条保证最终一致性。
