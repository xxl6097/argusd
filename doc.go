// Package argus 提供 OpenWrt 路由器接入设备的实时发现与上下线监听能力。
//
// Argus 以希腊神话中的百眼巨人命名, 寓意多源融合、永不沉睡的守望者:
//   - 厂商私有 ubus (ahsapd) 与 OpenWrt 官方 hostapd 自动探测切换
//   - 系统日志 logread -f 实时流 (毫秒级感知关联 / 断开 / 漫游)
//   - 活性 ping 探测 + ARP 状态感知, 识别 AP 关联表残留的假在线
//   - DHCP 租约 + ARP 表补全主机名 / IP
//   - 90s 冷却期 + 30s 抖动抑制窗口, 压制弱信号边缘设备的假抖动
//
// Argus is the all-seeing watcher for OpenWrt device presence:
// fusing ahsapd / hostapd / syslog / DHCP / ARP / ICMP into a single
// real-time event stream (Online / Offline / Change), with optional
// decision-trace callbacks for deep observability.
//
// 典型用法:
//
//	w := argus.New()
//	devices, _ := w.List(ctx)            // 一次性拉取
//	w.Run(ctx, func(e argus.Event) {     // 实时监听
//	    // 处理 EventOnline / EventOffline / EventChange
//	}, nil)
package argus
