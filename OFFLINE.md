# 离线判定机制详解

本文档基于当前代码（`watcher.go` / `decision.go` / `logwatch.go` / `prober.go` / `enrich.go`）全面分析设备离线（`EventOffline`）判定的完整流程。所有阈值集中在 `Config`，默认值见 `DefaultConfig()`。

---

## 架构概览：双通道 + 三层筛选 + 冷却期 + 抖动抑制

```
通道 A  系统日志（毫秒级）                 通道 B  周期轮询（秒级兜底）
──────────────────────────                ──────────────────────────────
logread -f (runSyslog goroutine)          每 PollInterval=1s
    │                                         │
  wifi_sys_disconn_act  (WIFI_DISCONNECT)  fetchWithAPSet(ctx)
  ap_peer_deauth_action (DEAUTH)             ├── Fetcher.Fetch  → apRaw
  Del Sta:<mac>         (MACTABLE_DELETE)    └── filterAlive     → alive
    │                                           │
  syslogHint{Disconnect:true}               stateMu.Lock
    │                                           │
    ▼ (syslogHints channel cap=256)          loadARPStates(ctx)
    │                                           │
  runSyslogConsumer                      diff(...) → pending []Event
  (最多 16 并发 worker, 受 runWG 追踪)     ┌──────┼──────┬────────┬──────┐
    │                                     ▼      ▼      ▼        ▼       ▼
  handleDisconnectHint                 ping    AP感知  RSSI     ARP    冷却期
    │                                 过滤    息屏    分级     加速    + 抖动
  ① 入口: disconnectInFlight 已在?           │
       → DISCONNECT_SKIP_INFLIGHT            stateMu.Unlock
  ② 不在 known → DISCONNECT_IGNORE           │
  ③ Sleep 500ms (漫游窗口, 响应 ctx.Done)    for _, ev := range pending:
  ④ drainHintsFor (清同 MAC 重复 hint)          safeInvokeEvent(ev)
  ⑤ ping 可达? → DISCONNECT_PING_OK (漫游)
  ⑥ 再次检查 known (可能被 diff 删除)
  ⑦ 抖动窗口? → FLAP_SUPPRESS_OFFLINE
  ⑧ safeInvokeEvent(EventOffline) + cooldown  ← panic-safe
```

共享状态 `known` / `misses` / `offlineCooldown` / `lastEventAt` / `disconnectInFlight` 通过 `stateMu` 串行化。

**v0.5.0 起 Run 可重启**：`Stop(ctx)` 取消内部 ctx 并等 `runWG` 归零（所有 `runSyslog` / `runSyslogConsumer` / hint worker 退出），随后可再次 `Run(ctx)`。重启时 `offlineCooldown` / `lastEventAt` 保留（冷却仍有效），`misses` / `disconnectInFlight` 重置（跨 Run 无意义）。

**v0.4.0 起所有用户回调 panic-safe**：`EventOffline` 经 `safeInvokeEvent` 发射，用户回调 panic 通过 `onError` 上报为 `argus: EventHandler panicked: <value>`，不会杀死 worker。

---

## 系统日志中的离线事件

触发 `SyslogKind.IsDisconnect() == true` 的三类事件（`logwatch.go:89-91`）：

| 事件类型 | 日志样例 | 触发场景 |
|---------|---------|---------|
| `SyslogWifiDisconnect` | `wifi_sys_disconn_act() Addr=ba:79:...` | 主动断开 |
| `SyslogDeauth` | `DE-AUTH(seq-xxx) from ba:79:...` | Deauth 帧收发 |
| `SyslogMacTableDelete` | `MacTableDeleteEntry(): Del Sta:ba:79:...` | **所有离开场景的最终必然事件** |

### 场景 A：主动关闭 WiFi

```
t=0.000s  kern  DE-AUTH(seq-2)                     ← DEAUTH
t=0.001s  kern  wifi_sys_disconn_act() Addr=ba:79  ← WIFI_DISCONNECT
t=0.002s  kern  MacTableDeleteEntry: Del Sta:ba:79 ← MACTABLE_DELETE
```

三条日志几乎同时产生，`drainHintsFor` 清除后两条重复。

### 场景 B：设备走出信号范围（渐弱过程）

路由器不一定产生完整日志序列：

```
t=0s      RSSI=-30 → 逐渐减弱
t=~60s    RSSI -75~-85，ping 时通时不通
t=~90s    RSSI<-85，ping 持续不通
t=~120s+  AP keepalive 超时产生 Del Sta（不保证）
```

> ⚠️ 关键：信号渐弱过程中 **AP 可能长时间不产生任何 disconnect/deauth/Del Sta 日志**，离线完全依赖通道 B。

### 场景 C：漫游（断开后迅速重连）

```
t=0.000s  Del Sta → syslogHint{Disconnect:true} 入队
t=0.050s  New Sta → syslogHint{Disconnect:false} 入队
t=0.500s  handleDisconnectHint 等 500ms 后 ping：设备已重连 → DISCONNECT_PING_OK
```

---

## 通道 A：系统日志驱动离线

### 处理流程（`watcher.go:handleDisconnectHint`）

```go
func (w *Watcher) handleDisconnectHint(ctx context.Context, mac string,
    onEvent EventHandler, onError ErrorHandler) {
    emitDecision(DISCONNECT_HINT, mac, "")

    // ① 入口去重: 同 MAC 已有正在执行的 worker → 跳过
    //    (一次断开的 3 条 syslog 会派生 3 个 worker, 仅第一个完整执行)
    w.stateMu.Lock()
    if _, inflight := w.disconnectInFlight[mac]; inflight {
        w.stateMu.Unlock()
        emitDecision(DISCONNECT_SKIP_INFLIGHT, mac, "")
        return
    }
    d, ok := w.known[mac]
    if !ok {
        w.stateMu.Unlock()
        emitDecision(DISCONNECT_IGNORE_UNKNOWN, mac, "")
        return
    }
    w.disconnectInFlight[mac] = struct{}{}
    w.stateMu.Unlock()
    defer func() {
        w.stateMu.Lock()
        delete(w.disconnectInFlight, mac)
        w.stateMu.Unlock()
    }()

    // ② 500ms 等待: 漫游 / 瞬断留重连窗口 (响应 ctx.Done 以便 Stop 快速返回)
    select {
    case <-time.After(500 * time.Millisecond):
    case <-ctx.Done():
        return
    }

    // ③ 清空 channel 里该 MAC 的后续重复 hint (disconnect/deauth/Del Sta)
    w.drainHintsFor(mac)

    // ④ ping 确认是否真正不可达
    if w.prober != nil && d.IP != "" {
        if w.prober.Reachable(ctx, d.IP) {
            w.stateMu.Lock()
            delete(w.misses, mac)       // 已重连, 清 miss 计数
            w.stateMu.Unlock()
            emitDecision(DISCONNECT_PING_OK, mac, "IP=...")
            return
        }
    }

    // ⑤ 再次检查 known: Sleep 期间可能已被 diff 或其他 handler 删除
    w.stateMu.Lock()
    if _, stillKnown := w.known[mac]; !stillKnown {
        w.stateMu.Unlock()
        return
    }
    delete(w.known, mac)
    delete(w.misses, mac)
    if !w.cfg.DisableCooldown {
        w.offlineCooldown[mac] = time.Now()
    }

    // ⑥ 抖动抑制: 30s 窗口内同类 Offline 压制
    now := time.Now()
    if w.shouldSuppressFlap(mac, EventOffline, now) {
        w.stateMu.Unlock()
        emitDecision(FLAP_SUPPRESS_OFFLINE, mac, "")
        return
    }
    w.recordEvent(mac, EventOffline, now)
    w.stateMu.Unlock()

    // ⑦ panic-safe 发射 EventOffline (锁已释放, 回调阻塞不会影响共享状态)
    emitDecision(OFFLINE_EMIT, mac, "via=syslog")
    w.safeInvokeEvent(onEvent, onError, Event{Time: now, Kind: EventOffline, Device: d})
}
```

### 通道 A 时间线

```
t=0.000s  日志事件 → syslogHint 入队 → runSyslogConsumer 启动 worker
t=0.000s  handleDisconnectHint 开始 → DISCONNECT_HINT
t=0.500s  500ms 等待结束, drainHintsFor 去重
t=0.500s  ping -c 1 -W 1 <ip>
t=1.500s  ping 超时 → OFFLINE_EMIT + cooldown[mac]=now
```

**延迟 ≈ 1.5s**（500ms 等待 + 1s ping 超时）。

---

## 通道 B：周期轮询离线

### 数据拉取

```
fetchWithAPSet(ctx)
  ├── Fetcher.Fetch(ctx)     → apRaw  (AP 关联表全部设备, 含 RSSI)
  └── filterAlive(prober)    → alive  (ping 可达子集)

diff() 内额外加载:
  loadARPStates(ctx)         → 每台设备的 ARP 状态 (REACHABLE/STALE/FAILED/...)
```

### 第一层：活性探测（`prober.go:filterAlive`）

```
对每台有 IP 的设备并行执行 ping -c 1 -W 1 <ip>
  可达 → 保留在 alive map
  不可达 → 剔除 (进入通道 B 后续分层判定)
空 IP 设备直接视为可达 (信任 AP 关联表)
```

- **安全防护**：IP 经正则 `^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}$` + `net.ParseIP` 双重校验，防命令注入
- `ICMPProber.Timeout` 默认 1s；超时由 `context.WithTimeout(timeout+500ms)` 兜底

### 第二层：AP 关联表 + RSSI 分级判定（`watcher.go:diff` 中段）

`diff` 在持有 `stateMu` 的情况下只收集事件到 `pending []Event`，由 `Run` 在释放锁之后经 `safeInvokeEvent` 逐条发射。`emitIfNotSuppressed` 是闭包，内联 `FlapSuppressionWindow` 检查后 `append` 到 `pending`。

当设备不在 `alive` 中但仍在 `apSet`（AP 关联表有）时：

```go
if rawDev, inAP := apSet[mac]; inAP {
    rssi := rawDev.RSSI
    d.RSSI = rssi                // 更新 known 中 RSSI 为实时值
    known[mac] = d

    pingOK := false
    if prober != nil && d.IP != "" {
        pingOK = prober.Reachable(ctx, d.IP)
    }
    if pingOK {
        delete(misses, mac)      // 息屏场景 (iPhone), 清计数
        continue
    }

    // ping 不通 + 信号弱 → 分级加速
    if rssi != 0 && rssi < cfg.WeakRSSI {        // 默认 -80
        misses[mac]++
        threshold := cfg.WeakMissThreshold        // 默认 5
        if rssi < cfg.ExtremelyWeakRSSI {         // 默认 -88
            threshold = cfg.ExtremelyWeakMissThreshold   // 默认 2
        }
        emitDecision(POLL_WEAK_MISS, mac, "RSSI=... misses=N/T")
        if misses[mac] >= threshold {
            if inCooldown(mac) {                 // DisableCooldown=false 才检查
                emitDecision(COOLDOWN_SUPPRESS_OFFLINE, mac, "")
            } else {
                emitIfNotSuppressed(EventOffline, d)    // 含 Flap 检查, append pending
            }
            noteCooldown(mac)                    // DisableCooldown=true 时 no-op
            delete(known, mac)
            delete(misses, mac)
        }
        continue
    }

    // ping 不通但信号正常 (息屏) → 不计离线
    delete(misses, mac)
    emitDecision(POLL_SLEEP_PROTECT, mac, "RSSI=...")
    continue
}
```

**RSSI 分级策略**：

| RSSI 范围 | ping 不通时的处理 | 默认到离线时长 |
|-----------|------------------|---------------|
| `>= WeakRSSI`（默认 ≥ -80 dBm）| **息屏保护**，不计入离线（清 `misses`）| 永不触发 |
| `WeakRSSI ~ ExtremelyWeakRSSI`（默认 -80 ~ -88 dBm）| 连续 `WeakMissThreshold` 次（默认 5）判离线 | ≈ 5s |
| `< ExtremelyWeakRSSI`（默认 < -88 dBm）| 连续 `ExtremelyWeakMissThreshold` 次（默认 2）判离线 | ≈ 2s |

### 第三层：ARP 状态加速（`watcher.go:diff` 后段）

当设备**也不在 `apSet`**（AP 关联表也已移除）时，查 `ip neigh show` 的状态：

```go
arp, hasARP := arpStates[mac]
if !hasARP && d.IP != "" {
    arp, hasARP = arpStates["_ip:"+d.IP]   // MAC 失效时用 IP 反查
}
if hasARP && (arp.State == "FAILED" || arp.State == "INCOMPLETE") {
    emitDecision(POLL_ARP_FAILED, mac, "state=...")
    if inCooldown(mac) {
        emitDecision(COOLDOWN_SUPPRESS_OFFLINE, mac, "")
    } else {
        emitIfNotSuppressed(EventOffline, d)
    }
    noteCooldown(mac)
    delete(known, mac)
    delete(misses, mac)
    continue
}

// 默认路径: misses 累加
misses[mac]++
if misses[mac] >= cfg.OfflineMisses {     // 默认 5
    emitDecision(POLL_MISSES_EXHAUSTED, mac, "misses=N/T")
    if inCooldown(mac) {
        emitDecision(COOLDOWN_SUPPRESS_OFFLINE, mac, "")
    } else {
        emitIfNotSuppressed(EventOffline, d)
    }
    noteCooldown(mac)
    delete(known, mac)
    delete(misses, mac)
}
```

`ip neigh show` 状态码含义：

- `REACHABLE` / `STALE` / `DELAY` / `PROBE` → 内核仍认为可达，走默认 misses 计数（≈ 5s）
- `FAILED` / `INCOMPLETE` → 内核多次 ARP 失败已确认不可达，**立即触发离线**（≈ 1s）

---

## 冷却期机制（`OfflineCooldown` / `CooldownReleaseRSSI`）

所有离线触发点——通道 A 的 `handleDisconnectHint` 和 diff 的三个离线分支——都会：

1. 无条件写入 `cooldown[mac] = now`（通过 `noteCooldown(mac)` 辅助函数）
2. 从 `known` / `misses` 移除设备
3. 若**已**在冷却期内（`inCooldown(mac)` 返回 true），走 `COOLDOWN_SUPPRESS_OFFLINE` 分支，**不重复**发射 `EventOffline`（但仍刷新 cooldown）

- 冷却期长度 `Config.OfflineCooldown`（默认 90s）
- diff 每轮开头清理过期记录：`if now.Sub(t) > cfg.OfflineCooldown { delete(cooldown, mac) }`
- **对上线的影响**：冷却期内设备重新出现，`RSSI < CooldownReleaseRSSI`（默认 -65）时静默更新 known（见 ONLINE.md）

**显式关闭（v0.3.0+）**：`Config.DisableCooldown = true` 完全关闭冷却期——`inCooldown` 恒返回 false，`noteCooldown` 不写入。零值默认启用。语义比"让 `OfflineCooldown` 极小"更清晰（因为 `WithConfig` 对零值按"保留默认"处理，没有"显式关闭"的零值写法）。

---

## 抖动抑制窗口（`FlapSuppressionWindow`）

与冷却期互补，覆盖冷却期解除后的短时同类快闪。

- `lastEventAt[mac]` 记录最近一次**外发**事件的时刻和类型
- 窗口期（默认 30s）内产生的**同类**事件（Online→Online / Offline→Offline）被静默
- 不同类事件（Online→Offline 或反之）不压制，正常刷新 `lastEventAt`
- 设 `FlapSuppressionWindow = 0` **或** `Config.DisableFlapSuppression = true`（v0.3.0+）完全关闭

通道 A 和通道 B 都会经过这一层：
- 通道 A 在 `handleDisconnectHint` 中显式调用 `shouldSuppressFlap`
- 通道 B 在 `diff` 的 `emitIfNotSuppressed` 闭包内联实现

---

## 完整的离线判定流程图

```
               设备不在 cur (alive) 中
                        │
                        ▼
           ┌── 设备在 apSet (AP关联表) 吗？──┐
           │                                │
           是                               否
           │                                │
           ▼                                ▼
     (第二层 RSSI 分级)              (第三层 ARP 状态)
           │                                │
     ping 可达？                      ARP=FAILED/INCOMPLETE?
      ├ 是 → POLL_SLEEP_PROTECT        ├ 是 → POLL_ARP_FAILED → 离线分支
      │     delete(misses)             │
      └ 否 → RSSI 分级:                └ 否 → misses++
            RSSI < ExtremelyWeakRSSI         │
             → threshold = 2                 └ misses ≥ OfflineMisses?
            RSSI < WeakRSSI                     ├ 是 → POLL_MISSES_EXHAUSTED
             → threshold = 5                    │     → 离线分支
            RSSI ≥ WeakRSSI                     └ 否 → 等下一轮
             → POLL_SLEEP_PROTECT
            misses ≥ threshold?
             ├ 是 → POLL_WEAK_MISS → 离线分支
             └ 否 → 等下一轮

  离线分支 (emitIfNotSuppressed):
           ┌── 冷却期内？ ──┐
           否              是
           │               │
           ▼               ▼
     抖动窗口内？      COOLDOWN_SUPPRESS_OFFLINE
     同类 Offline?    (静默, 仍刷新 cooldown)
      ├ 是 → FLAP_SUPPRESS_OFFLINE
      └ 否 → OFFLINE_EMIT → onEvent(EventOffline)
                            │
                            ▼
                 cooldown[mac] = now
                 delete(known, mac)
                 delete(misses, mac)
```

---

## 离线决策矩阵

| ping 可达 | AP 关联表 | RSSI | ARP | 冷却期内 | 抖动窗口同类 | 处理 |
|:---:|:---:|:---:|:---:|:---:|:---:|------|
| ✅ | ✅ | 任意 | 任意 | 任意 | — | 在 alive，保持在线 |
| ❌ | ✅ | ≥ -80 | — | 任意 | — | `POLL_SLEEP_PROTECT`，清 misses |
| ❌ | ✅ | -80~-88 | — | ❌ | ❌ | 5 次后 `OFFLINE_EMIT` |
| ❌ | ✅ | -80~-88 | — | ❌ | ✅ | 5 次后 `FLAP_SUPPRESS_OFFLINE` |
| ❌ | ✅ | -80~-88 | — | ✅ | — | 5 次后 `COOLDOWN_SUPPRESS_OFFLINE` |
| ❌ | ✅ | < -88 | — | ❌ | ❌ | 2 次后 `OFFLINE_EMIT` |
| ❌ | ❌ | — | FAILED | ❌ | ❌ | 立即 `OFFLINE_EMIT` |
| ❌ | ❌ | — | FAILED | ✅ | — | 立即 `COOLDOWN_SUPPRESS_OFFLINE` |
| ❌ | ❌ | — | 其他 | ❌ | ❌ | 5 次后 `OFFLINE_EMIT` |

---

## 离线时延汇总

| 场景 | 默认延迟 | 触发通道 / 分支 |
|------|---------|----------------|
| 主动关 WiFi（Deauth / Disconnect / Del Sta）| **≈ 1.5s** | 通道 A |
| 关机（AP 踢出 + Del Sta）| **≈ 1.5s** | 通道 A |
| 走出范围（RSSI 急剧下降到 < -88）| **≈ 2s** | 通道 B 第二层（极弱）|
| 走出范围（RSSI 在 -80~-88 持续）| **≈ 5s** | 通道 B 第二层（弱）|
| 走出范围 + AP 最终踢出 + Del Sta | AP timeout + **≈ 1.5s** | 通道 A 接力 |
| 走出范围 + AP 踢出 + ARP FAILED | **≈ 1s** | 通道 B 第三层 |
| 走出范围 + AP 踢出 + ARP 仍 STALE | **≈ 5s** | 通道 B 默认 misses |
| 有线拔线（有 IP）| **≈ 5s** | 通道 B 默认 misses |
| iPhone 息屏 | **永不触发** | 第二层 `POLL_SLEEP_PROTECT` |
| 弱信号反复抖动 | **首次 ≈ 5s，后续 90s 内静默** | 冷却期 + 抖动窗口联合压制 |

---

## 关键时间线

### 场景 1：主动关闭 WiFi

```
t=0.000s  设备关 WiFi
t=0.001s  DEAUTH + WIFI_DISCONNECT + MACTABLE_DELETE 日志同时产生
t=0.001s  runSyslogConsumer 取第一条 hint → worker → handleDisconnectHint
          → DISCONNECT_HINT
t=0.501s  500ms 等待结束, drainHintsFor 丢弃后两条
t=0.501s  ping 超时 (1s)
t=1.501s  OFFLINE_EMIT → EventOffline + cooldown=t=1.501s
```

### 场景 2：走出信号范围（无日志）

```
t=0s      在线 RSSI=-30
t=60s     diff: apSet 有, ping 不通, RSSI=-82 → POLL_WEAK_MISS (1/5)
t=61s     POLL_WEAK_MISS (2/5)
t=62s     POLL_WEAK_MISS (3/5)
t=63s     POLL_WEAK_MISS (4/5)
t=64s     POLL_WEAK_MISS (5/5) → OFFLINE_EMIT + cooldown=t=64s
```

### 场景 3：弱信号边缘抖动（冷却期 + 抖动窗口压制）

```
t=64s     首次 OFFLINE_EMIT, cooldown=t=64s, lastEventAt=Offline@t=64s
t=66s     RSSI=-75 回到 alive → 冷却期内 + RSSI<-65
          → COOLDOWN_SUPPRESS_ONLINE (刷新 cooldown=t=66s)
t=75s     RSSI=-82 ping 不通 → diff 第二层 → 5 轮后达阈值
          → 冷却期内 → COOLDOWN_SUPPRESS_OFFLINE
          → 刷新 cooldown=t=80s
...
t=170s    cooldown 过期, RSSI=-50 稳定 → CONNECT_EMIT
t=180s    信号再次变弱触发离线 → 冷却期外
          → lastEventAt 显示 10s 前 Online, 类型不同 → 不压制 → OFFLINE_EMIT
```

若 t=170s 发射 Online 后 10s 内又尝试发射 Online（例如弱信号刷新），`FlapSuppressionWindow` 会直接挡住。

### 场景 4：漫游（500ms 窗口吸收）

```
t=0.000s  Del Sta → syslogHint{Disconnect:true} 入队
t=0.001s  handleDisconnectHint 开始 → Sleep 500ms
t=0.050s  New Sta → syslogHint{Disconnect:false} 入队
t=0.500s  drainHintsFor (没有额外 disconnect hint)
t=0.500s  ping → 已重连可达 → DISCONNECT_PING_OK
t=0.500s  delete(misses), 不触发 EventOffline
t=0.500s  connect hint 在独立 worker 中处理:
          handleConnectHint → known 中已有 mac (未被删除) → CONNECT_SKIP_KNOWN
```

**漫游全程无任何事件触发。**

---

## 边界情况

### 1. 同一断开的多条日志

DEAUTH + WIFI_DISCONNECT + MACTABLE_DELETE 几乎同时产生。这三条 hint 派生 3 个 worker，但 `handleDisconnectHint` 入口的 `disconnectInFlight` 集合（持 `stateMu` 以 CAS 方式抢占）保证只有第一个进入 500ms Sleep + ping 流程，第二/第三个立即发 `DISCONNECT_SKIP_INFLIGHT` 决策返回（实测 < 1ms）。第一个 worker 在 Sleep 结束后调用 `drainHintsFor` 清空 channel 中该 MAC 的尚未派发的 hint。

⚠️ 注意：`drainHintsFor` 只能处理**已入队**的 hint。若有一条 hint 在 500ms 内才入队并被新 worker 取走，新 worker 仍会被 `disconnectInFlight` 挡住；它再次进入 `handleDisconnectHint` 时若集合已清空（第一条已结束），则走 `known` 二次检查，发现 MAC 已被清除 → `DISCONNECT_IGNORE_UNKNOWN`。

### 2. 设备 IP 为空

`w.prober != nil && d.IP != ""` 不满足 → 跳过 ping，直接走 `known` 二次检查 + 离线发射逻辑（日志已明确报告断开）。

### 3. `syslogHints` channel 已满（容量 256）

`select default` 丢弃新 hint，累加 `droppedHints`；每 30s 聚合通过 `onError` 上报。通道 B 在 ≤1s 内兜底。

### 4. Sleep 500ms 期间被 diff 或另一 worker 清除

`handleDisconnectHint` 恢复后持锁二次检查 `known[mac]`，若已消失直接返回，不触发事件。

### 5. ping 超时但设备实际在线

极低概率（路由器 CPU 高负载、无线干扰）：单轮 ping 失败 → `misses++`；下一轮恢复 → `delete(misses)`；需连续多轮才触发离线，单次误判不成事件。

### 6. Context 取消 / Stop 调用

- `Run` 主循环通过 `runCtx.Done()` 退出（`runCtx` 派生自用户 ctx，也被 `Stop()` 触发）
- `runSyslog` / `runSyslogConsumer` 随 ctx 退出，worker 信号量释放
- `exec.CommandContext` 自动终止 `logread -f` 和 `ping` 子进程
- `Stop(stopCtx)` 等 `runWG` 归零 或 `stopCtx` 超时；超时返回 `context.DeadlineExceeded`，残留 worker 会自行退出
- 正在 500ms Sleep 的 `handleDisconnectHint` 会被 `ctx.Done()` 立即唤醒退出（不发射离线事件）
- **MT7981 真机验证**：10 次 SIGHUP（即 Stop + Run 循环）后 `Threads` 数稳定在 15，无 goroutine 泄漏（见 `docs/SIGHUP-real-device-test.md`）

### 7. List(ctx) 与 Run 并发

`List` 也会应用冷却期过滤（`watcher.go:List` 中对 `offlineCooldown` 的判定），保持与 Run 的"在线"定义一致——刚判离线且仍弱信号的设备不会出现在 List 返回结果中。

---

## 决策观测（`DecisionHandler`）

离线相关决策点（`decision.go`）：

| DecisionKind | 触发点 |
|---|---|
| `DISCONNECT_HINT` | 收到断开日志 hint |
| `DISCONNECT_IGNORE_UNKNOWN` | 不在 known，忽略 |
| `DISCONNECT_SKIP_INFLIGHT` | 同 MAC 已有正在处理的 worker，去重跳过 |
| `DISCONNECT_PING_OK` | 500ms 后 ping 仍可达（漫游）|
| `OFFLINE_EMIT` | 正式发射 EventOffline |
| `FLAP_SUPPRESS_OFFLINE` | 抖动窗口内同类压制 |
| `COOLDOWN_SUPPRESS_OFFLINE` | 冷却期内重复离线，静默移除 |
| `POLL_SLEEP_PROTECT` | apSet + 信号正常 + ping 不通，息屏 |
| `POLL_WEAK_MISS` | 弱信号 + ping 不通，累加 miss |
| `POLL_ARP_FAILED` | apSet 无 + ARP FAILED/INCOMPLETE |
| `POLL_MISSES_EXHAUSTED` | 默认 misses 达阈值 |

未注册 `DecisionHandler` 时完全零成本：`emitDecision` / 闭包 `emitDecision` 都在 `onDecision == nil` 时立即返回。

---

## 相关代码索引

| 文件 | 函数 / 段落 | 职责 |
|------|-----------|------|
| `watcher.go` | `Watcher` 字段 | `offlineCooldown` / `misses` / `lastEventAt` |
| `watcher.go` | `runSyslog` | 过滤 IsDisconnect，写 channel，droppedHints 计数 |
| `watcher.go` | `runSyslogConsumer` | 信号量（16）+ worker 派发 |
| `watcher.go` | `handleDisconnectHint` | Sleep 500ms + drainHints + ping + 离线 + 冷却 |
| `watcher.go` | `drainHintsFor` | 清空同 MAC 后续 hint |
| `watcher.go` | `shouldSuppressFlap` | `FlapSuppressionWindow` 实现 |
| `watcher.go` | `fetchWithAPSet` | 返回 `apRaw`（含 RSSI）+ `alive` |
| `watcher.go` | `diff`（后半段） | 三层筛选 + 冷却期 + 抖动抑制 |
| `decision.go` | `DecisionKind` 常量 | `OFFLINE_EMIT` / `POLL_*` / `COOLDOWN_SUPPRESS_*` / `FLAP_SUPPRESS_*` |
| `prober.go` | `filterAlive` | 并行 ping 过滤 |
| `prober.go` | `ICMPProber.Reachable` | 正则 + `net.ParseIP` 双重校验 + ping |
| `logwatch.go` | `IsDisconnect` | 3 类事件判定 |
| `logwatch.go` | 正则定义 | `reWifiDisconn` / `reDeauth` / `reMacTableDel` |
| `enrich.go` | `loadARPStates` | 解析 `ip neigh show` 状态 |

---

## 总结

离线判定 = **日志通道（通道 A）** + **三层筛选轮询（通道 B）** + **冷却期压制** + **抖动抑制**

**通道 A（日志驱动，≈ 1.5s）**：
- 触发源：`wifi_sys_disconn_act` / `deauth` / `Del Sta`
- 处理：500ms 等待（吸收漫游）+ ping 确认 + 二次 known 检查

**通道 B（三层筛选，默认 1-5s）**：
- 第一层（活性探测）：识别 AP 关联表残留
- 第二层（AP 关联表 + RSSI 分级）：
  - `RSSI ≥ WeakRSSI`（默认 -80）：息屏保护
  - `WeakRSSI ~ ExtremelyWeakRSSI`：5 次判离线（≈ 5s）
  - `< ExtremelyWeakRSSI`（默认 -88）：2 次判离线（≈ 2s）
- 第三层（ARP 状态）：`FAILED` / `INCOMPLETE` 立即判离线（≈ 1s）

**冷却期（默认 90s）**：
- 任何离线触发都写入 cooldown
- 冷却期内重复离线走 `COOLDOWN_SUPPRESS_OFFLINE`（静默但刷新 cooldown）
- 上线端：冷却期内 + RSSI 弱 → 静默（见 ONLINE.md）

**抖动抑制（默认 30s）**：
- 同一 MAC 窗口内同类事件压制
- 与冷却期互补：前者管长时弱信号，后者管短时快闪

**核心优化**：
1. `SyslogMacTableDelete`（Del Sta）覆盖无 disconnect/deauth 的走出范围场景
2. `apRaw` 保留实时 RSSI，供 diff 做分级判定
3. ARP `FAILED`/`INCOMPLETE` 作为 ahsapd 空返回时的加速信号
4. 冷却期刷新策略（每次抑制都刷新 cooldown）消除自然过期后重复走完整离线流程的问题
5. 抖动窗口防止冷却期解除后立刻又触发同类事件
6. **入口级 `disconnectInFlight` 去重（v0.2.0+）**：同一 MAC 的 3 条 syslog 只让第一个 worker 走完整 500ms+ping 流程，后续走 `DISCONNECT_SKIP_INFLIGHT` 立即返回
7. **事件发射锁外化（v0.4.0+）**：diff 只把事件收集到 `pending`，Run 在 `stateMu` 释放之后经 `safeInvokeEvent` 逐条发射；用户回调 panic 经 `onError` 上报，不会破坏共享状态或杀死 goroutine
8. **Run 可重启（v0.5.0+）**：`Stop()` + `Run()` 循环支持 SIGHUP 热重载；`offlineCooldown` / `lastEventAt` 跨 Run 保留（继续压制抖动），`disconnectInFlight` 重置（旧 worker 已随 ctx 退出，不跨 Run）
9. **可配置关闭**：`Config.DisableCooldown` / `Config.DisableFlapSuppression` 提供显式开关，不依赖魔法值
