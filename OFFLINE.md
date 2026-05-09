# 离线判定机制详解

本文档基于当前代码（`watcher.go` / `logwatch.go` / `prober.go` / `enrich.go`）全面分析设备离线判定的完整流程。

---

## 架构概览：双通道 + 三层筛选 + 冷却期

```
通道 A  系统日志（毫秒级）                通道 B  周期轮询（秒级兜底）
───────────────────────────              ────────────────────────────
logread -f                               每 PollInterval=1s
    │                                        │
  wifi_sys_disconn_act  (主动断开)        Fetcher.Fetch(ctx) → apRaw
  ap_peer_deauth_action (Deauth帧)        filterAlive(ping)  → alive
  Del Sta:<mac>         (MAC表移除)       loadARPStates()    ← 新增
    │                                        │
  syslogHint{Disconnect:true}            diff(known, cur, apRaw, apSet)
    │                                ┌──────┼──────┬────────┬─────────┐
    │                                ▼      ▼      ▼        ▼         ▼
    │                             ping   AP感知  RSSI分级  ARP状态   冷却期
    │                             过滤   息屏    加速判定   加速判定   防抖
    │                                        │
    └──────► 主循环 select ◄──────────────────┘
                  │
        handleDisconnectHint
                  │
         500ms 等待 + ping 确认
                  │
           EventOffline + 进入冷却期
```

共享状态：`known` / `misses` / `offlineCooldown`，主循环串行处理。

---

## 系统日志中的离线事件序列

### 场景 A：主动关闭 WiFi

```
t=0.000s  kern  ap_peer_deauth_action(): DE-AUTH from ba:79:...  ← DEAUTH
t=0.001s  kern  wifi_sys_disconn_act() Addr=ba:79:...            ← WIFI_DISCONNECT
t=0.002s  kern  MacTableDeleteEntry(): Del Sta:ba:79:...         ← MACTABLE_DELETE
```

三条日志几乎同时产生，`drainHintsFor` 清除后两条重复。

### 场景 B：设备走出信号范围（渐弱过程）

路由器不一定产生完整日志序列。实际观察到的典型过程：

```
t=0s      设备信号强 RSSI=-30 → 逐渐减弱
t=~60s    RSSI 降到 -75~-85，ping 时通时不通
t=~90s    RSSI<-85，ping 持续不通
t=~120s+  AP keepalive 超时，产生 Del Sta（**但不保证**）
```

> ⚠️ 关键：在信号渐弱的过程中，**AP 可能长时间不产生任何 disconnect/deauth/Del Sta 日志**。此时离线判定完全依赖通道 B。

### 场景 C：漫游（断开后迅速重连）

```
t=0.000s  Del Sta → syslogHint{Disconnect:true} 入队
t=0.050s  New Sta → syslogHint{Disconnect:false} 入队
t=0.500s  handleDisconnectHint 等 500ms 后 ping：设备已重连 → 不触发离线
```

---

## 触发 IsDisconnect() 的三类事件（`logwatch.go:85-90`）

| 事件类型 | 日志样例 | 触发场景 |
|---------|---------|---------|
| `SyslogWifiDisconnect` | `wifi_sys_disconn_act() Addr=ba:79:...` | 主动断开 |
| `SyslogDeauth` | `ap_peer_deauth_action(): DE-AUTH from ba:79:...` | Deauth 帧收发 |
| `SyslogMacTableDelete` | `MacTableDeleteEntry(): Del Sta:ba:79:...` | **所有离开场景的最终必然事件** |

---

## 通道 A：系统日志驱动离线

### 处理流程（`watcher.go:260-278`）

```go
func (w *Watcher) handleDisconnectHint(ctx, mac, known, misses, onEvent) {
    d, ok := known[mac]
    if !ok { return }

    // ① 等 500ms：给漫游/瞬断留重连窗口
    time.Sleep(500 * time.Millisecond)

    // ② 清空该 MAC 的后续重复 hint
    w.drainHintsFor(mac)

    // ③ ping 确认是否真正不可达
    if w.prober != nil && d.IP != "" {
        if w.prober.Reachable(ctx, d.IP) {
            delete(misses, mac)   // 已重连，清计数
            return
        }
    }

    // ④ ping 不通 → 触发离线 + 进入冷却期
    onEvent(Event{Kind: EventOffline, Device: d})
    w.offlineCooldown[mac] = time.Now()   // ← 冷却期标记
    delete(known, mac)
    delete(misses, mac)
}
```

### 通道 A 时间线

```
t=0.000s  日志事件 → syslogHint 入队
t=0.000s  handleDisconnectHint 开始
t=0.500s  500ms 等待结束 + drainHintsFor 去重
t=0.500s  ping -c 1 -W 1 <ip>
t=1.500s  ping 超时 → EventOffline + 冷却期标记
```

**延迟 ≈ 1.5s**。

---

## 通道 B：周期轮询离线（三层筛选 + 冷却期）

### 数据拉取（`watcher.go:341-355`）

```
fetchWithAPSet(ctx)
  ├── Fetcher.Fetch(ctx)       → apRaw map（含 RSSI 等实时字段）
  └── filterAlive(prober)      → alive map（仅 ping 可达）

diff() 内额外加载：
  loadARPStates(ctx)            → 每台设备的 ARP 状态
```

### 第一层：活性探测（`prober.go:45-72`）

```
filterAlive(ctx, raw, prober)
  → 对每台有 IP 的设备并行执行 ping -c 1 -W 1 <ip>
  → 可达 → 保留在 alive map
  → 不可达 → 从 alive map 剔除
```

- **安全防护**：IP 经正则 `^\d{1,3}...` + `net.ParseIP` 双重校验，防命令注入
- 空 IP 设备直接视为可达（信任 AP 关联表）

### 第二层：AP 关联表 + RSSI 分级判定（`watcher.go:402-438`）

当设备不在 `alive` 中但仍在 `apSet`（AP 关联表有）时，按 **RSSI 强度分级**：

```go
if rawDev, inAP := apSet[mac]; inAP {
    rssi := rawDev.RSSI
    d.RSSI = rssi            // 更新 known 中 RSSI 为实时值
    known[mac] = d

    // ping 再次确认
    pingOK := prober.Reachable(ctx, d.IP)
    if pingOK {
        delete(misses, mac)  // 息屏场景，清计数
        continue
    }

    // ping 不通 + 信号弱 → 分级加速
    if rssi != 0 && rssi < -80 {
        misses[mac]++
        threshold := 5                      // 弱信号: 5 次 ≈ 5s
        if rssi < -88 {
            threshold = 2                   // 极弱信号: 2 次 ≈ 2s
        }
        if misses[mac] >= threshold {
            // 冷却期检查
            if _, inCD := cooldown[mac]; !inCD {
                onEvent(Event{Kind: EventOffline, Device: d})
            }
            cooldown[mac] = now             // 无论是否触发事件都刷新冷却
            delete(known, mac)
            delete(misses, mac)
        }
        continue
    }

    // ping 不通但信号正常 → 息屏保护
    delete(misses, mac)
    continue
}
```

**RSSI 分级策略**：

| RSSI 范围 | ping 不通时的处理 |
|-----------|------------------|
| ≥ -80 dBm | **息屏保护**，不计入离线（清 misses）|
| -80 ~ -88 dBm（弱信号）| 连续 **5 次**（≈5s）判离线 |
| < -88 dBm（极弱信号）| 连续 **2 次**（≈2s）判离线 |

### 第三层：ARP 状态加速（`watcher.go:441-454`）

当设备也不在 `apSet`（AP 关联表也没了）时，查 ARP 状态：

```go
arp, hasARP := arpStates[mac]
if !hasARP && d.IP != "" {
    arp, hasARP = arpStates["_ip:"+d.IP]   // 用 IP 反查（MAC 可能已失效）
}
if hasARP && (arp.State == "FAILED" || arp.State == "INCOMPLETE") {
    // ARP 已确认不可达 → 立即触发离线
    if _, inCD := cooldown[mac]; !inCD {
        onEvent(Event{Kind: EventOffline, Device: d})
    }
    cooldown[mac] = now
    delete(known, mac)
    delete(misses, mac)
    continue
}

// 默认路径：连续 misses 计数
misses[mac]++
if misses[mac] >= offlineMisses {   // 默认 5
    if _, inCD := cooldown[mac]; !inCD {
        onEvent(Event{Kind: EventOffline, Device: d})
    }
    cooldown[mac] = now
    delete(known, mac)
    delete(misses, mac)
}
```

`ip neigh show` 的状态码：
- `REACHABLE` / `STALE` / `DELAY` / `PROBE`：仍被认为可达，走默认 misses 计数
- `FAILED` / `INCOMPLETE`：内核已多次 ARP 失败确认，立即触发离线

---

## 冷却期机制

### 目的

防止弱信号边缘设备反复产生 `EventOffline` → `EventOnline` → `EventOffline` 抖动。

### 实现

- 所有离线触发点（通道 A 和通道 B 三条分支）都会：
  1. 若不在冷却期 → 触发 `EventOffline`
  2. 无条件写入 `cooldown[mac] = now`
  3. 从 `known` / `misses` 移除设备
- 冷却期长度 `90s`（`cooldownDuration`）
- 过期记录在 diff 开始时自动清理

### 作用范围

- **上线端**：冷却期内设备重新出现，弱信号（< -65 dBm）时静默更新，不触发 `EventOnline`
- **离线端**：冷却期内设备再次被判定离线，只更新冷却时间，不触发重复的 `EventOffline`

### 效果

在 RSSI 持续徘徊 -76 ~ -88 的信号边缘场景中：
- **旧版**：14 分钟内产生 20+ 次上线/离线抖动
- **新版**：只有 1 次离线事件，冷却期结束前所有波动被静默压制

---

## 完整的离线判定流程图

```
               设备不在 cur（alive map）中
                        │
                        ▼
           ┌── 设备在 apSet（AP关联表）吗？──┐
           │                                │
           是                               否
           │                                │
           ▼                                ▼
     (RSSI 分级判定)              (ARP 状态查询)
           │                                │
     ping 可达？                    ARP=FAILED/INCOMPLETE?
      ├ 是 → 息屏保护               ├ 是 → 立即判离线
      │     delete(misses)         │      (检查冷却期)
      │                             │
      └ 否 → RSSI < -80?            └ 否 → misses++
            ├ < -88 → threshold=2         │
            ├ < -80 → threshold=5         └ ≥ OfflineMisses(5)?
            └ ≥ -80 → 息屏保护                   ├ 是 → 判离线
                     delete(misses)              │      (检查冷却期)
                                                 └ 否 → 等下一轮
            misses ≥ threshold?
             ├ 是 → 判离线（检查冷却期）
             └ 否 → 等下一轮

  判离线分支:
           ┌── 是否已在冷却期 ──┐
           否                  是
           │                   │
           ▼                   ▼
      触发 EventOffline   静默移除（不触发事件）
           │                   │
           └────── cooldown[mac] = now ──────┘
                        │
                从 known / misses 移除
```

---

## 离线决策矩阵

| ping 可达 | AP 关联表 | RSSI | ARP | 在冷却期 | 处理 |
|:---:|:---:|:---:|:---:|:---:|------|
| ✅ | ✅ | 任意 | 任意 | 任意 | 在 alive，正常在线 |
| ❌ | ✅ | ≥ -80 | — | 任意 | 息屏保护，misses=0 |
| ❌ | ✅ | -80~-88 | — | ❌ | misses++，达 5 次触发离线 |
| ❌ | ✅ | -80~-88 | — | ✅ | misses++，达 5 次静默移除 |
| ❌ | ✅ | < -88 | — | ❌ | misses++，达 2 次触发离线 |
| ❌ | ❌ | — | FAILED | ❌ | 立即触发离线 |
| ❌ | ❌ | — | FAILED | ✅ | 立即静默移除 |
| ❌ | ❌ | — | 其他 | ❌ | misses++，达 5 次触发离线 |

---

## 离线时延汇总

| 场景 | 延迟 | 触发通道 |
|------|------|---------|
| 主动关 WiFi（Deauth/Disconnect 日志）| **≈ 1.5s** | 通道 A |
| 关机（AP 踢出 + Del Sta）| **≈ 1.5s** | 通道 A |
| 走出范围（RSSI 急剧下降到 < -88）| **≈ 2s** | 通道 B（RSSI 极弱分支）|
| 走出范围（RSSI 在 -80~-88 持续）| **≈ 5s** | 通道 B（RSSI 弱分支）|
| 走出范围 + AP 最终踢出 + Del Sta | AP timeout + **≈ 1.5s** | 通道 A 接力 |
| 走出范围 + AP 踢出但无日志 + ARP FAILED | **≈ 1s** | 通道 B（ARP 加速）|
| 走出范围 + AP 踢出但无日志 + ARP 仍 STALE | **≈ 5s** | 通道 B（misses 默认）|
| 有线拔线（有 IP）| **≈ 5s** | 通道 B |
| iPhone 息屏 | **永不触发** | 息屏保护 |
| 弱信号区反复抖动 | **首次 ≈ 5s，后续 90s 内静默** | 冷却期压制 |

---

## 关键时间线

### 场景 1：主动关闭 WiFi

```
t=0.000s  设备关 WiFi
t=0.001s  DEAUTH + WIFI_DISCONNECT + MACTABLE_DELETE 日志同时产生
t=0.001s  第一条 hint 入队，handleDisconnectHint 开始
t=0.501s  500ms 等待 + drainHintsFor 丢弃其余两条
t=0.501s  ping 超时 → EventOffline
          offlineCooldown[mac] = t=0.501s
t=1.501s  触发离线
```

### 场景 2：走出信号范围（无日志）

```
t=0s      在线 RSSI=-30
t=60s     轮询: apSet 有, ping 不通, RSSI=-82 → misses=1
t=61s     RSSI=-83 → misses=2
t=62s     RSSI=-85 → misses=3
t=63s     RSSI=-87 → misses=4
t=64s     RSSI=-88 → misses=5 ≥ 5 → EventOffline + 冷却期
```

### 场景 3：弱信号边缘抖动（冷却期有效压制）

```
t=64s     首次 EventOffline, cooldown=t=64s
t=66s     RSSI=-75, 回到 alive → known 中无 → 冷却期内 + RSSI<-65
          → 静默更新 known，不触发 EventOnline
t=75s     RSSI=-82, ping 不通 → 走 RSSI 分级分支
          → 5 轮后静默从 known 移除（冷却期内，不触发）
          → 刷新 cooldown=t=80s
...
t=170s    cooldown 过期，若设备稳定出现 → 正常 EventOnline
```

### 场景 4：漫游（500ms 窗口吸收）

```
t=0.000s  Del Sta → syslogHint{Disconnect:true} 入队
t=0.001s  handleDisconnectHint 开始，进入 500ms 等待
t=0.050s  New Sta → syslogHint{Disconnect:false} 入队
t=0.500s  等待结束，drainHintsFor 清空 channel 该 MAC 的后续 hint
t=0.500s  ping 该设备 IP → 已重连，ping 可达
t=0.500s  delete(misses)，不触发 EventOffline

t=0.500s  connect hint 仍在 channel → handleConnectHint
          → mac 已在 known（未被删除）→ 忽略
```

**漫游全程无任何事件触发。**

---

## 边界情况

### 1. 同一断开的多条日志

DEAUTH + WIFI_DISCONNECT + MACTABLE_DELETE 同时产生 → 第一条触发 `handleDisconnectHint` 进入 500ms 等待 → `drainHintsFor` 清空同 MAC 的后续 hint → 只触发一次 `EventOffline`。

### 2. 设备 IP 为空

`w.prober != nil && d.IP != ""` 条件不满足 → 跳过 ping，直接触发 `EventOffline`（日志已明确报告断开）。

### 3. syslogHints channel 满（容量 64）

`select { case ch <- h: default: }` 丢弃新 hint。通道 B 在 ≤5s 内兜底检测。

### 4. Context 取消

- `Run` 主循环通过 `select ctx.Done()` 退出
- `exec.CommandContext` 自动终止 `logread -f`、`ping` 子进程
- 不触发任何 EventOffline

### 5. ping 超时但设备实际在线

极低概率（路由器 CPU 高负载、无线干扰）：
- 单轮 ping 失败 → misses++
- 下一轮 ping 恢复 → delete(misses)
- 需连续多轮才触发离线，单次误判不成事件

---

## 核心代码索引

| 文件 | 行号 | 函数/段落 | 职责 |
|------|------|---------|------|
| `watcher.go` | 121-123 | `offlineCooldown` 字段 | 记录每台设备最近离线时刻 |
| `watcher.go` | 213-232 | `Run` 中 `WatchSyslog` 回调 | 过滤 IsDisconnect 事件 |
| `watcher.go` | 241-242 | select disconnect 分支 | 分发到 `handleDisconnectHint` |
| `watcher.go` | 260-278 | `handleDisconnectHint` | 500ms 等 + ping + EventOffline + 冷却期 |
| `watcher.go` | 317-332 | `drainHintsFor` | 清空同 MAC 重复 hint |
| `watcher.go` | 344-355 | `fetchWithAPSet` | 返回 apRaw（含 RSSI）+ alive |
| `watcher.go` | 360-466 | `diff` | 三层筛选 + 冷却期压制 |
| `watcher.go` | 362-369 | diff 冷却期清理 | 过期记录自动删除 |
| `watcher.go` | 402-438 | diff 第二层（RSSI 分级）| 息屏/弱/极弱分级处理 |
| `watcher.go` | 441-454 | diff 第三层（ARP）| FAILED/INCOMPLETE 加速 |
| `watcher.go` | 456-464 | diff 默认分支 | misses 累加 |
| `prober.go` | 45-72 | `filterAlive` | 并行 ping 过滤 |
| `prober.go` | 29-43 | `ICMPProber.Reachable` | 带正则校验的单设备 ping |
| `logwatch.go` | 85-90 | `IsDisconnect` | 3 类事件判定 |
| `enrich.go` | — | `loadARPStates` | 解析 ip neigh show |

---

## 总结

离线判定 = **日志通道（通道 A）** + **三层筛选轮询（通道 B）** + **冷却期防抖动**

**通道 A（日志驱动）**：
- 触发源：`wifi_sys_disconn_act` / `deauth` / `Del Sta`
- 处理：500ms 等待（吸收漫游）+ ping 确认
- 延迟：**≈ 1.5s**

**通道 B 第一层（ping 过滤）**：识别 AP 关联表残留

**通道 B 第二层（AP 关联表 + RSSI 分级）**：
- RSSI ≥ -80：息屏保护，不计离线
- RSSI -80~-88：连续 5 次判离线（**≈ 5s**）
- RSSI < -88：连续 2 次判离线（**≈ 2s**）

**通道 B 第三层（ARP 状态）**：FAILED/INCOMPLETE 时立即判离线（**≈ 1s**）

**冷却期（90s）**：
- 弱信号边缘抖动设备只产生首次离线事件
- 冷却期内的所有重复上线/离线被静默压制
- 冷却期内上线需 RSSI ≥ -65 才允许触发事件

**关键优化点**：
1. `SyslogMacTableDelete`（Del Sta）覆盖走出范围但无 disconnect/deauth 的场景
2. `apRaw` 保留实时 RSSI，供 diff 做分级判定
3. ARP 状态（FAILED/INCOMPLETE）作为 ahsapd 空返回时的补充信号
4. 冷却期消除弱信号边缘（-75~-85 dBm）的事件风暴
