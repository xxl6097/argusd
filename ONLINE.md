# 上线判定机制详解

本文档基于当前代码（`watcher.go` / `logwatch.go` / `prober.go` / `enrich.go`）全面分析设备上线判定的完整流程。

---

## 架构概览：双通道 + 冷却期

```
通道 A  系统日志（毫秒级）                通道 B  周期轮询（秒级兜底）
───────────────────────────              ────────────────────────────
logread -f                               每 PollInterval=1s
    │                                        │
  New Sta:<mac>       (MAC表新增)        Fetcher.Fetch(ctx)
  AP SETKEYS DONE     (WPA完成)          filterAlive(ping)
  DHCPACK <ip> <mac>  (DHCP分配)             │
    │                                    diff(known,cur,apRaw)
  syslogHint{Disconnect:false}               │
    │                                        │
    └──────► 主循环 select ◄──────────────────┘
                  │
        handleConnectHint           或    diff 中发现新 MAC
                  │                           │
         检查冷却期 & 防重复         检查冷却期 & 信号强度
                  │                           │
              EventOnline                EventOnline
```

两条通道共享 `known` 和 `offlineCooldown` map，由 `Run()` 主循环串行处理，无并发冲突。  
**哪条通道先感知到，就先触发事件；另一条通过 `known[mac]` 检查自动忽略。**

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

触发 `IsConnect()=true` 的三类事件（`logwatch.go:92-95`）：

| 事件类型 | 正则 | 日志样例 | 触发时机 |
|---------|------|---------|---------|
| `SyslogMacTableInsert` | `New Sta:([0-9a-fA-F:]{17})` | `MacTableInsertEntry: New Sta:ba:79:...` | AP 关联表插入（最早）|
| `SyslogWPAComplete` | `AP SETKEYS DONE\((\w+)\).*from\s+(...)` | `AP SETKEYS DONE(rax0) from ba:79:...` | 4-way 握手完成 |
| `SyslogDHCPAck` | `DHCPACK\([\w-]+\)\s+([\d.]+)\s+(...)` | `DHCPACK(br-lan) 192.168.1.5 ba:79:...` | dnsmasq 分配 IP |

---

## 通道 A：系统日志驱动上线

### 事件分发（`watcher.go:213-232`）

`WatchSyslog` 的回调过滤 IsConnect 事件，包装为 `syslogHint` 写入 channel：

```go
go WatchSyslog(ctx, func(e SyslogEvent) {
    mac := normalizeMAC(e.MAC)
    if mac == "" { return }
    var h syslogHint
    h.MAC = mac
    if e.Kind.IsDisconnect() {
        h.Disconnect = true
    } else if e.Kind.IsConnect() {
        h.Disconnect = false
        h.IP = e.IP   // 仅 DHCP_ACK 时有值
    } else {
        return
    }
    select {
    case w.syslogHints <- h:
    default:          // channel 满时丢弃, 轮询兜底
    }
}, nil)
```

### 上线处理（`watcher.go:282-314`）

```go
func (w *Watcher) handleConnectHint(ctx, h, known, onEvent) {
    // ① 防重复：已在 known 中 → 忽略
    if _, ok := known[h.MAC]; ok {
        return
    }

    // ② 立即拉取完整设备列表（不等下一轮轮询）
    devs, err := w.fetcher.Fetch(ctx)
    if err != nil { return }

    for _, d := range devs {
        if d.MAC == h.MAC {
            // ③a 找到该 MAC → 完整信息触发上线
            if d.IP == "" && h.IP != "" {
                d.IP = h.IP       // 用 DHCP 日志补全 IP
            }
            known[d.MAC] = d
            onEvent(Event{Kind: EventOnline, Device: d})
            return
        }
    }

    // ③b AP 关联表还未更新 → 用 DHCP IP + hints 构建基础记录
    if h.IP != "" {
        hints := loadHints(ctx)
        d := applyHints(Device{MAC: h.MAC, IP: h.IP, LastSeen: now}, hints[h.MAC])
        known[d.MAC] = d
        onEvent(Event{Kind: EventOnline, Device: d})
    }
    // ③c 无 IP 且 AP 无数据 → 放弃, 等轮询兜底
}
```

> ⚠️ 注意：通道 A 当前**不检查冷却期**。冷却期机制主要用于抑制通道 B 中弱信号抖动场景，通道 A 驱动的上线通常是真正的新接入。

### 防重复机制

同一设备连接时通常产生三条日志（New Sta → WPA_COMPLETE → DHCP_ACK），只有第一条通过 `known[h.MAC]` 检查：

```
t=0.000s  New Sta → handleConnectHint → not in known → Fetch → EventOnline, 加入 known
t=0.190s  AP SETKEYS DONE → handleConnectHint → 已在 known → 忽略
t=1.000s  DHCPACK → handleConnectHint → 已在 known → 忽略
```

---

## 通道 B：周期轮询上线（兜底）

### 数据拉取（`watcher.go:344-355`）

```
fetchWithAPSet(ctx)
  ├── Fetcher.Fetch(ctx)         → apRaw map（AP 关联表全部设备，含 RSSI）
  └── filterAlive(prober)        → alive map（仅 ping 可达设备）
```

### 上线判定 + 冷却期检查（`watcher.go:371-393`）

```go
for mac, d := range cur {
    delete(misses, mac)
    prev, ok := known[mac]
    switch {
    case !ok:
        // 冷却期检查：最近刚判离线的设备不立即触发上线
        if cdTime, inCD := cooldown[mac]; inCD && now.Sub(cdTime) < 90s {
            // 冷却期内：信号必须恢复到强信号 (≥ -65 dBm) 才允许上线
            if d.RSSI != 0 && d.RSSI < -65 {
                known[mac] = d    // 静默更新，不触发事件
                continue
            }
            // 信号恢复正常 或 RSSI=0(有线) → 正常上线
            delete(cooldown, mac)
        }
        onEvent(Event{Kind: EventOnline, Device: d})
    default:
        // 已知设备: 检查字段变化
        if cs := changedFields(prev, d); len(cs) > 0 {
            onEvent(Event{Kind: EventChange, Device: d, Changes: cs})
        }
    }
    known[mac] = d
}
```

### 冷却期机制详解

**目的**：防止弱信号边缘设备反复上线/离线抖动。

**工作原理**：
- 设备被判离线时（`diff` 或 `handleDisconnectHint` 中），在 `offlineCooldown[mac]` 记录离线时刻
- 冷却期长度 `90s`（`cooldownDuration`）
- 冷却期内，设备重新出现在 `cur` 中时：
  - 信号 `RSSI < -65 dBm`（弱/中等信号）→ **静默更新 known，不触发 EventOnline**
  - 信号 `RSSI ≥ -65 dBm`（强信号）→ 视为真正恢复，清除冷却并正常触发上线
  - `RSSI == 0`（有线设备，无信号数据）→ 直接正常触发上线
- 过期的冷却记录自动清理（`watcher.go:365-369`）

### 状态变更检测（`watcher.go:470-485`）

已在 `known` 中的设备，每次轮询会检查关键字段变化：

| 字段 | 触发 EventChange | 说明 |
|------|:---:|------|
| `IP` | ✅ | DHCP 续约/重分配 |
| `Hostname` | ✅（空→非空） | 空变非空触发，非空变空不触发（防抖动）|
| `Radio` | ✅ | 如 2.4G↔5G 漫游 |
| `SSID` | ✅ | 切换到不同 SSID |
| `RSSI` | ❌ | 持续抖动 |
| `Channel` | ❌ | 持续变化 |
| `UpTime` | ❌ | 持续递增 |

---

## 上线决策矩阵

| 场景 | known 中 | 冷却期内 | RSSI 条件 | 触发事件 |
|------|:---:|:---:|:---------:|---------|
| 新设备首次接入 | ❌ | ❌ | 任意 | `EventOnline` |
| 设备重新接入（冷却外）| ❌ | ❌ | 任意 | `EventOnline` |
| 设备重新接入（冷却内，弱信号）| ❌ | ✅ | < -65 dBm | **静默更新**（不触发）|
| 设备重新接入（冷却内，强信号）| ❌ | ✅ | ≥ -65 dBm | `EventOnline`（清除冷却）|
| 有线设备（无 RSSI）| ❌ | ✅ | == 0 | `EventOnline`（清除冷却）|
| iPhone 息屏恢复 | ✅ | 任意 | 任意 | **无事件**（已在 known）|
| IP 地址变化 | ✅ | 任意 | 任意 | `EventChange` |
| 同一次连接的多条日志 | ✅ | 任意 | 任意 | **无事件**（防重复）|

---

## 上线时延

| 设备类型 / 场景 | 通道 A 延迟 | 通道 B 延迟 | **实际延迟** |
|--------------|------------|------------|------------|
| WiFi 新设备接入（有 AP 数据）| ≈ 即时（New Sta）| 0-1s | **≈ 即时** |
| WiFi 新设备接入（AP 表慢）| ≈ 190ms（WPA_COMPLETE）| 0-1s | **≈ 190ms** |
| WiFi 重连（离线后冷却期外）| ≈ 即时 | 0-1s | **≈ 即时** |
| WiFi 重连（冷却期内、弱信号）| —（静默）| —（静默）| **不触发** |
| WiFi 重连（冷却期内、强信号）| ≈ 即时 | 0-1s | **≈ 即时** |
| 有线设备（DHCP）| DHCPACK ≈ 即时 | 0-1s | **≈ 即时** |
| 有线设备（静态 IP）| 无日志 | 0-1s | **0-1s** |
| logread 失效（退化）| 不工作 | 0-1s | **0-1s** |

---

## 关键时间线

### 场景 1：WiFi 设备首次接入

```
t=0.000s  设备发起关联
t=0.000s  内核: New Sta → syslogHint 入队
t=0.000s  handleConnectHint:
            → 不在 known
            → Fetcher.Fetch: AP 关联表已有 → EventOnline（完整设备信息）
            → known[mac] = d
t=0.190s  AP SETKEYS DONE → 已在 known → 忽略
t=1.000s  DHCPACK → 已在 known → 忽略
t=1.000s  轮询 diff: 已在 known → 检查 EventChange（无变化）
```

### 场景 2：弱信号区设备反复断开重连（被冷却期压制）

```
t=0s     设备在线 RSSI=-30
t=60s    RSSI 衰减到 -82, ping 不通 → misses++
t=64s    misses=5, RSSI<-80 → EventOffline
         → offlineCooldown[mac] = t=64s
t=65s    RSSI=-78, ping 偶然可达 → 回到 cur → 冷却期内 + RSSI<-65
         → 静默更新 known，不触发 EventOnline
t=72s    RSSI=-80, ping 不通 → 直接进入离线分支（known 中有）
         → 冷却期内 → 静默移除 known（不触发 EventOffline）
t=154s   冷却期到期 (90s)
t=155s   RSSI=-50 恢复 → 冷却期已清理 → 正常 EventOnline
```

**14 分钟内，原本会产生 20+ 次抖动，冷却期抑制后仅 1 次离线 + 1 次最终上线。**

### 场景 3：有线设备插网线

```
t=0.000s  设备插网线，发送 DHCP Request
t=0.500s  dnsmasq: DHCPACK → syslogHint{IP="192.168.1.x"} 入队
t=0.500s  handleConnectHint:
            → 不在 known
            → Fetcher.Fetch: 有线设备通常不在 ahsapd 关联表
            → h.IP 有值 → loadHints + applyHints → EventOnline（基础记录）
t=1.000s  轮询兜底: 已在 known → 无事件
```

### 场景 4：iPhone 息屏恢复

```
iPhone 息屏中: AP 关联表有, ping 不通, RSSI 正常 → diff 识别为息屏
             → delete(misses)，保持 known 中，无事件

iPhone 亮屏: ping 恢复 → 进入 cur
           → diff: 已在 known → 检查 EventChange（通常无变化）
           → 无事件
```

---

## 基线拉取（启动时）

```go
// watcher.go:206-209
known, err := w.fetchByMAC(ctx)
misses := map[string]int{}
```

- 基线设备直接进入 `known`，**不触发 EventOnline**
- 之后通道 A / B 才开始产生事件
- 获取启动时设备列表：调用 `w.List(ctx)`

---

## 数据补全机制

上线事件中的 `Device` 字段来自多层数据源，按优先级合并：

```
① ahsapd/hostapd 原始数据
   MAC / IP / Hostname / Vendor / Type / Radio / SSID / RSSI / Channel / UpTime / AccessTime

② syslogHint 中的 DHCP IP（h.IP）
   仅在 fetcher 未返回 IP 时填入

③ /tmp/dhcp.leases（enrich.go）
   Hostname（主要来源）+ IP

④ ip neigh show（enrich.go）
   IP 兜底，跳过 FAILED/INCOMPLETE/IPv6
```

---

## 边界情况

### 1. 设备 MAC 地址格式异常

```go
mac := normalizeMAC(s.MACAddress)
if mac == "" { continue }
```
- 长度不是 12 位的 MAC 直接丢弃
- 支持紧凑大写 `B0FC36329461` 转 `b0:fc:36:32:94:61`

### 2. MAC 随机化（iOS/Android 隐私 MAC）

```
旧 MAC 断开 → EventOffline → 进入冷却
新 MAC 接入 → 不同 MAC，冷却不适用 → EventOnline
```
表现为"一台旧设备下线 + 一台新设备上线"。

### 3. AP 关联表更新慢于日志

```
New Sta → Fetch: AP 关联表还没这台设备
       → h.IP 为空 (New Sta 无 IP) → 放弃
后续:
AP SETKEYS DONE → Fetch: AP 关联表已就绪 → EventOnline
```

### 4. 设备快速断开再连接（漫游）

由通道 A 的 `handleDisconnectHint` 500ms 等待 + ping 确认处理，连接事件被 `known[mac]` 检查挡住。

### 5. logread -f 失效

通道 A 退化，完全依赖通道 B，延迟从"≈ 即时" 变为 "0-1s"。

---

## 完整数据流图

```
              logread -f（系统日志）
                      │
      ┌───────────────┼────────────────┐
      ▼               ▼                ▼
 New Sta:<mac>   AP SETKEYS       DHCPACK
 (MACTABLE_INSERT) DONE (WPA)    <ip> <mac>
      │               │           (DHCP_ACK)
      └──────┬─────── ┘               │
         IsConnect()=true             │
             │                        │
             └──────────┬─────────────┘
                        ▼
           syslogHint{Disconnect:false, MAC, IP}
                        │
              ┌─────────┴──────────────────────────────────────┐
              ▼                                                │
       handleConnectHint                                       │
              │                                                │
        在 known 中？ ──是──→ 忽略（防重复）                    │
              │                                                │
              否                                               │
              │                                                │
        Fetcher.Fetch(ctx) 立即拉取                             │
              │                                                │
        找到该 MAC？                                            │
         ├ 是 → 完整 Device → EventOnline                      │
         └ 否 → 有 IP(h.IP)？                                   │
               ├ 是 → 基础 Device + hints → EventOnline        │
               └ 否 → 放弃（等轮询）                            │
                                                               │
                      ─── 同时每秒 ───                          │
                                                               │
                  fetchWithAPSet(ctx) ◄──────────────────────────┘
                          │
                ┌─────────┴──────────┐
                ▼                    ▼
             apRaw                alive map
           (全部设备)           (ping 可达)
                          │
                    diff(known, cur)
                          │
              发现 mac 不在 known？
                          │
               冷却期内? ──否──→ EventOnline
                          │
                          是
                          │
               RSSI ≥ -65?
                ├ 是或 0 → 清除冷却 → EventOnline
                └ 否 → 静默更新 known（不触发）
```

---

## 相关代码索引

| 文件 | 行号 | 函数/段落 | 职责 |
|------|------|---------|------|
| `watcher.go` | 133-147 | `New()` | 初始化 syslogHints / offlineCooldown |
| `watcher.go` | 213-232 | `Run` 中 `WatchSyslog` 回调 | 过滤 IsConnect 事件，写入 channel |
| `watcher.go` | 240-245 | `Run` select 分支 | 根据 hint 类型分发 |
| `watcher.go` | 282-314 | `handleConnectHint` | 日志驱动快速上线：防重复 → Fetch → EventOnline |
| `watcher.go` | 360-393 | `diff` 上半部分 | 轮询上线 + 冷却期检查 + EventChange |
| `watcher.go` | 470-485 | `changedFields` | 字段差异检测 |
| `logwatch.go` | 92-95 | `IsConnect` | 判定 3 类事件为接入信号 |
| `logwatch.go` | 100-119 | 正则定义 | 日志匹配规则 |
| `enrich.go` | 19-101 | `loadHints`/`applyHints` | DHCP/ARP 补全 |

---

## 总结

上线判定 = **系统日志即时通知（通道 A）** + **周期轮询兜底（通道 B）** + **冷却期防抖动**

| 维度 | 通道 A | 通道 B |
|------|--------|--------|
| 延迟 | ≈ 即时（New Sta/WPA_COMPLETE）| 0-1s |
| 覆盖 | WiFi 设备 + 有线（DHCP）| 全部设备 |
| 可靠性 | 依赖 logread -f 进程 | 不依赖外部进程 |
| 冷却期 | 不检查（信任日志信号）| 90s 内弱信号静默 |
| 防重复 | `known[mac]` 检查 | `!ok` in diff |

**核心机制**：
1. `SyslogMacTableInsert`（New Sta）是最早的上线信号，比 WPA_COMPLETE 早 ~190ms
2. 同一连接的多条日志由 `known[mac]` 检查确保只触发一次
3. 冷却期（90s）+ 强信号阈值（-65 dBm）有效抑制弱信号区抖动
4. 双通道互补：任一失效另一条保证最终一致性
