package argus

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// SyslogEvent 是从 OpenWrt 系统日志中解析出的设备事件。
type SyslogEvent struct {
	Time time.Time
	Kind SyslogKind
	MAC  string // 小写冒号格式
	IP   string // DHCP 事件时有值
	Iface string // 无线接口名 (如 rax0)
	Raw  string // 原始日志行
}

// SyslogKind 标识系统日志事件类型。
type SyslogKind int

const (
	// SyslogWifiConnect: 无线设备完成关联 + 4-way 握手 (内核 wifi_sys_conn_act)。
	SyslogWifiConnect SyslogKind = iota + 1
	// SyslogWifiDisconnect: 无线设备断开关联 (内核 wifi_sys_disconn_act)。
	SyslogWifiDisconnect
	// SyslogDeauth: AP 收到或发出 Deauth 帧 (ap_peer_deauth_action)。
	SyslogDeauth
	// SyslogMacTableDelete: AP 从 MAC 表中删除设备 (MacTableDeleteEntry)。
	// 走出信号范围时 AP 不一定产生 disconnect/deauth, 但一定会 Del Sta。
	SyslogMacTableDelete
	// SyslogWPAComplete: WPA 握手完成 (AP SETKEYS DONE)。
	SyslogWPAComplete
	// SyslogMacTableInsert: AP 将新设备插入 MAC 表 (MacTableInsertEntry New Sta)。
	SyslogMacTableInsert
	// SyslogDHCPAck: DHCP 分配确认 (dnsmasq-dhcp DHCPACK)。
	SyslogDHCPAck
)

// String 返回事件类型的稳定英文标识。
func (k SyslogKind) String() string {
	switch k {
	case SyslogWifiConnect:
		return "WIFI_CONNECT"
	case SyslogWifiDisconnect:
		return "WIFI_DISCONNECT"
	case SyslogDeauth:
		return "DEAUTH"
	case SyslogMacTableDelete:
		return "MACTABLE_DELETE"
	case SyslogWPAComplete:
		return "WPA_COMPLETE"
	case SyslogMacTableInsert:
		return "MACTABLE_INSERT"
	case SyslogDHCPAck:
		return "DHCP_ACK"
	}
	return "UNKNOWN"
}

// Label 返回中文文案。
func (k SyslogKind) Label() string {
	switch k {
	case SyslogWifiConnect:
		return "无线接入"
	case SyslogWifiDisconnect:
		return "无线断开"
	case SyslogDeauth:
		return "认证踢出"
	case SyslogMacTableDelete:
		return "MAC表移除"
	case SyslogWPAComplete:
		return "认证完成"
	case SyslogMacTableInsert:
		return "MAC表新增"
	case SyslogDHCPAck:
		return "DHCP分配"
	}
	return "未知事件"
}

// IsDisconnect 报告事件是否表示设备断开。
// MacTableDeleteEntry 是设备离开信号范围后 AP 必定产生的最终事件,
// 即使 disconnect_act / deauth 被跳过。
func (k SyslogKind) IsDisconnect() bool {
	return k == SyslogWifiDisconnect || k == SyslogDeauth || k == SyslogMacTableDelete
}

// IsConnect 报告事件是否表示设备接入 (WPA 握手完成、MAC 表新增或 DHCP 分配)。
func (k SyslogKind) IsConnect() bool {
	return k == SyslogWPAComplete || k == SyslogMacTableInsert || k == SyslogDHCPAck
}

// SyslogHandler 接收系统日志事件回调。
type SyslogHandler func(SyslogEvent)

// 日志匹配正则
var (
	// wifi_sys_conn_act 内核日志: Addr=ba:79:97:73:89:8d
	reWifiConn = regexp.MustCompile(`wifi_sys_conn_act\(\)\s+\d+:.*Addr=([0-9a-fA-F:]{17})`)
	// wifi_sys_disconn_act 内核日志: Addr=ba:79:97:73:89:8d
	reWifiDisconn = regexp.MustCompile(`wifi_sys_disconn_act\(\)\s+\d+:.*Addr=([0-9a-fA-F:]{17})`)
	// DE-AUTH(seq-xxx) from ba:79:97:73:89:8d, reason=N
	// 精确匹配 "DE-AUTH(...) from <mac>" 避免误抓同一行中其他 MAC。
	reDeauth = regexp.MustCompile(`DE-AUTH\([^)]*\)\s+from\s+([0-9a-fA-F:]{17})`)
	// MacTableInsertEntry: New Sta:ba:79:97:73:89:8d (新设备关联)
	reNewSta = regexp.MustCompile(`New Sta:([0-9a-fA-F:]{17})`)
	// MacTableDeleteEntry: Del Sta:ba:79:97:73:89:8d (设备从MAC表移除)
	// 设备走出信号范围时 AP 可能不产生 disconnect/deauth, 但一定会产生 Del Sta。
	reMacTableDel = regexp.MustCompile(`Del Sta:([0-9a-fA-F:]{17})`)
	// AP SETKEYS DONE(rax0) - ... from ba:79:97:73:89:8d
	reWPA = regexp.MustCompile(`AP SETKEYS DONE\((\w+)\).*from\s+([0-9a-fA-F:]{17})`)
	// dnsmasq-dhcp: DHCPACK(br-lan) 192.168.1.213 ba:79:97:73:89:8d
	reDHCPAck = regexp.MustCompile(`DHCPACK\([\w-]+\)\s+([\d.]+)\s+([0-9a-fA-F:]{17})`)
	// syslog 时间前缀: "Sat May  9 08:55:45 2026"
	// 捕获组: 1=Month(May), 2=Day(9), 3=HH:MM:SS, 4=Year(2026)
	reSyslogTime = regexp.MustCompile(`^\w+\s+(\w+)\s+(\d+)\s+(\d{2}:\d{2}:\d{2})\s+(\d{4})`)
)

// WatchSyslog 启动 `logread -f` 实时监听系统日志, 解析 WiFi / DHCP 相关事件
// 并通过 onEvent 回调上报。阻塞直到 ctx 取消或子进程退出。
//
// ctx 取消时会终止子进程并关闭 stdout 管道; 非 ctx 取消导致的退出通过返回值上报,
// 调用方应视为监听失败, 必要时自行重启。
//
// 子进程清理 (v1.2.4+):
//   - 启动前先调用 reapOrphanedLogreads() 清掉历史遗留的 PPid=1 logread 孤儿
//     (来自前一次 argusd 被 SIGKILL / OOM 杀死时未能清理的子进程)。
//   - cmd.SysProcAttr 设置 Pdeathsig=SIGTERM + Setpgid=true: 父进程死亡时
//     内核会主动给子进程发 SIGTERM (即使父被 SIGKILL), 同时进程组隔离允许
//     一并 kill 整组, 杜绝 busybox / sh 包装层的孤儿。
//
// 系统日志是被动监听, 不产生额外 CPU / 网络开销。
func WatchSyslog(ctx context.Context, onEvent SyslogHandler, onError ErrorHandler) error {
	if onEvent == nil {
		return nil
	}
	// Best-effort cleanup of stale logread orphans from a prior crash.
	if n := reapOrphanedLogreads(); n > 0 && onError != nil {
		safeInvokeErrorStandalone(onError,
			fmt.Errorf("reaped %d orphaned logread process(es) from prior run", n))
	}
	cmd := exec.CommandContext(ctx, "logread", "-f")
	cmd.SysProcAttr = linuxLogreadAttrs()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open logread stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start logread: %w", err)
	}

	// 确保子进程和管道在函数返回时释放
	defer func() {
		_ = stdout.Close()
		if cmd.Process != nil {
			// Kill the whole process group when Setpgid was used so
			// /bin/sh-wrapped descendants get cleaned up too. Negative
			// PID = process group on Linux. Falls back to plain Kill
			// on platforms where Setpgid was not applied.
			if cmd.SysProcAttr != nil && cmd.SysProcAttr.Setpgid {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			} else {
				_ = cmd.Process.Kill()
			}
		}
	}()

	sc := bufio.NewScanner(stdout)
	// 放大扫描缓冲以容纳超长日志行 (某些固件的 hexdump / stacktrace 可能很长)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)

	for sc.Scan() {
		if ev, ok := parseSyslogLine(sc.Text()); ok {
			onEvent(ev)
		}
	}
	scanErr := sc.Err()
	waitErr := cmd.Wait()

	// ctx 取消属于正常退出, 不作为错误返回
	if ctx.Err() != nil {
		return nil
	}
	if scanErr != nil {
		return fmt.Errorf("logread scan error: %w", scanErr)
	}
	if waitErr != nil {
		return fmt.Errorf("logread process exited: %w", waitErr)
	}
	return nil
}

// safeInvokeErrorStandalone is a panic-isolated ErrorHandler invocation
// for use outside the *Watcher context (where the regular
// (*Watcher).safeInvokeError lives). The standalone WatchSyslog function
// can't reach a Watcher, but still wants the same "user code panic
// shouldn't kill the goroutine" guarantee.
func safeInvokeErrorStandalone(cb ErrorHandler, err error) {
	if cb == nil {
		return
	}
	defer func() { _ = recover() }()
	cb(err)
}

// parseSyslogLine 尝试从一行日志中提取设备事件。
func parseSyslogLine(line string) (SyslogEvent, bool) {
	ts := parseSyslogTimestamp(line)

	if m := reWifiDisconn.FindStringSubmatch(line); m != nil {
		return SyslogEvent{Time: ts, Kind: SyslogWifiDisconnect, MAC: strings.ToLower(m[1]), Raw: line}, true
	}
	if m := reDeauth.FindStringSubmatch(line); m != nil {
		return SyslogEvent{Time: ts, Kind: SyslogDeauth, MAC: strings.ToLower(m[1]), Raw: line}, true
	}
	if m := reMacTableDel.FindStringSubmatch(line); m != nil {
		return SyslogEvent{Time: ts, Kind: SyslogMacTableDelete, MAC: strings.ToLower(m[1]), Raw: line}, true
	}
	if m := reWifiConn.FindStringSubmatch(line); m != nil {
		return SyslogEvent{Time: ts, Kind: SyslogWifiConnect, MAC: strings.ToLower(m[1]), Raw: line}, true
	}
	if m := reNewSta.FindStringSubmatch(line); m != nil {
		return SyslogEvent{Time: ts, Kind: SyslogMacTableInsert, MAC: strings.ToLower(m[1]), Raw: line}, true
	}
	if m := reWPA.FindStringSubmatch(line); m != nil {
		return SyslogEvent{Time: ts, Kind: SyslogWPAComplete, Iface: m[1], MAC: strings.ToLower(m[2]), Raw: line}, true
	}
	if m := reDHCPAck.FindStringSubmatch(line); m != nil {
		return SyslogEvent{Time: ts, Kind: SyslogDHCPAck, IP: m[1], MAC: strings.ToLower(m[2]), Raw: line}, true
	}
	return SyslogEvent{}, false
}

// parseSyslogTimestamp 从 syslog 行头解析时间, 使用路由器本机时区。
// syslog 行头格式: "Sat May  9 08:55:45 2026"
// 包含完整的月/日/时/年, 解析后直接使用, 无需二次调整 (旧实现有跨年 bug)。
func parseSyslogTimestamp(line string) time.Time {
	m := reSyslogTime.FindStringSubmatch(line)
	if m == nil {
		return time.Now()
	}
	now := time.Now()
	// 组合为 "Jan 9 08:55:45 2026" 由 time.Parse 处理月份 / 日期
	full := m[1] + " " + m[2] + " " + m[3] + " " + m[4]
	t, err := time.ParseInLocation("Jan 2 15:04:05 2006", full, now.Location())
	if err != nil {
		return now
	}
	return t
}
