package argus

import (
	"bufio"
	"context"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Hint 是 HintSource 返回的单条辅助信息, 用于在 Fetcher 数据缺失时
// 填补 Device 的 IP / Hostname 字段。
//
// 导出于 v0.7.0, 便于自定义 HintSource 实现。
type Hint struct {
	IP       string
	Hostname string
}

// HintSource 抽象"从 DHCP 租约 / ARP 表获取设备辅助信息"的能力,
// 允许使用方在非 OpenWrt 平台 (Docker 容器 / 自研固件) 注入自定义实现。
//
// 默认由 DefaultHintSource 提供 (读 /tmp/dhcp.leases + `ip neigh show`)。
// 返回值按小写 MAC 索引; 实现可自行决定是否带缓存。
type HintSource interface {
	Hints(ctx context.Context) map[string]Hint
}

// DefaultHintSource 是库的默认 HintSource 实现, 读取 OpenWrt 标准路径。
// 可通过修改字段覆盖路径 / 命令, 适用于定制固件。
//
// 并发安全: 内部带 TTL 缓存, 默认 5 秒, 避免每秒多次 fork 子进程。
type DefaultHintSource struct {
	// LeasesPath DHCP 租约文件路径, 留空使用默认 /tmp/dhcp.leases。
	LeasesPath string
	// ARPCommand 列出 ARP 表的命令 (argv 形式, 不走 shell), 留空使用默认
	// []string{"ip", "neigh", "show"}。
	ARPCommand []string
	// CacheTTL 缓存过期时长, 0 或负值使用默认 5s。设为极短值 (如 1ns) 可实际禁用。
	CacheTTL time.Duration

	mu       sync.Mutex
	cached   map[string]Hint
	cachedAt time.Time
}

// defaultHintInstance 是包级默认 HintSource, 兼容旧 loadHints 共享缓存的调用场景。
// 库初始化时填充; 使用方不应修改此变量。
var defaultHintInstance = &DefaultHintSource{}

// Hints 实现 HintSource 接口。
func (h *DefaultHintSource) Hints(ctx context.Context) map[string]Hint {
	leases := h.LeasesPath
	if leases == "" {
		leases = "/tmp/dhcp.leases"
	}
	arpCmd := h.ARPCommand
	if len(arpCmd) == 0 {
		arpCmd = []string{"ip", "neigh", "show"}
	}
	ttl := h.CacheTTL
	if ttl <= 0 {
		ttl = 5 * time.Second
	}

	h.mu.Lock()
	if h.cached != nil && time.Since(h.cachedAt) < ttl {
		out := make(map[string]Hint, len(h.cached))
		for k, v := range h.cached {
			out[k] = v
		}
		h.mu.Unlock()
		return out
	}
	h.mu.Unlock()

	fresh := map[string]Hint{}
	loadDHCPLeases(leases, fresh)
	loadARPCommand(ctx, arpCmd, fresh)

	h.mu.Lock()
	h.cached = fresh
	h.cachedAt = time.Now()
	out := make(map[string]Hint, len(fresh))
	for k, v := range fresh {
		out[k] = v
	}
	h.mu.Unlock()
	return out
}

// invalidateCache 清除内部缓存, 主要用于测试。
func (h *DefaultHintSource) invalidateCache() {
	h.mu.Lock()
	h.cached = nil
	h.cachedAt = time.Time{}
	h.mu.Unlock()
}

// loadHints 是库内部调用入口, 委托给 Watcher 的 hintSource (或包级默认)。
// 保留未导出以维持原有调用路径不变。
func loadHints(ctx context.Context) map[string]Hint {
	return defaultHintInstance.Hints(ctx)
}

// invalidateHintsCache 保留作为测试辅助 (部分测试依赖此函数清除缓存)。
func invalidateHintsCache() {
	defaultHintInstance.invalidateCache()
}

// loadDHCPLeases 解析 dnsmasq 风格租约: <expire> <mac> <ip> <hostname> <client-id>
func loadDHCPLeases(path string, into map[string]Hint) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 4 {
			continue
		}
		mac := strings.ToLower(fields[1])
		host := fields[3]
		if host == "*" {
			host = ""
		}
		h := into[mac]
		if h.IP == "" {
			h.IP = fields[2]
		}
		if h.Hostname == "" {
			h.Hostname = host
		}
		into[mac] = h
	}
}

// loadARPCommand 跑给定 argv 命令 (默认 `ip neigh show`) 并解析结果,
// 跳过 IPv6 与 FAILED/INCOMPLETE 表项。
func loadARPCommand(ctx context.Context, argv []string, into map[string]Hint) {
	if len(argv) == 0 {
		return
	}
	out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).Output()
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		state := fields[len(fields)-1]
		if state == "FAILED" || state == "INCOMPLETE" {
			continue
		}
		ip := fields[0]
		if strings.Contains(ip, ":") || net.ParseIP(ip) == nil {
			continue
		}
		var mac string
		for i, f := range fields {
			if f == "lladdr" && i+1 < len(fields) {
				mac = strings.ToLower(fields[i+1])
				break
			}
		}
		if mac == "" {
			continue
		}
		h := into[mac]
		if h.IP == "" {
			h.IP = ip
		}
		into[mac] = h
	}
}

// loadARP 保留旧签名 (使用默认 argv), 供旧测试使用。
func loadARP(ctx context.Context, into map[string]Hint) {
	loadARPCommand(ctx, []string{"ip", "neigh", "show"}, into)
}

// arpState 存储从 `ip neigh show` 中解析出的每台设备的 ARP 状态。
type arpState struct {
	IP    string
	State string // REACHABLE, STALE, DELAY, PROBE, INCOMPLETE, FAILED
}

// loadARPStates 解析 `ip neigh show` 返回按小写 MAC 索引的 ARP 状态。
// 仅保留 IPv4 表项。
func loadARPStates(ctx context.Context) map[string]arpState {
	out, err := exec.CommandContext(ctx, "ip", "neigh", "show").Output()
	if err != nil {
		return nil
	}
	states := map[string]arpState{}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		ip := fields[0]
		if strings.Contains(ip, ":") {
			continue
		}
		state := fields[len(fields)-1]
		var mac string
		for i, f := range fields {
			if f == "lladdr" && i+1 < len(fields) {
				mac = strings.ToLower(fields[i+1])
				break
			}
		}
		// FAILED/INCOMPLETE 的表项没有 lladdr, 通过 IP 反查已知设备
		if mac == "" {
			states["_ip:"+ip] = arpState{IP: ip, State: state}
			continue
		}
		states[mac] = arpState{IP: ip, State: state}
	}
	return states
}

// applyHints 在 Device 字段为空时填入辅助信息, 不会覆盖已有值。
func applyHints(d Device, h Hint) Device {
	if d.IP == "" {
		d.IP = h.IP
	}
	if d.Hostname == "" {
		d.Hostname = h.Hostname
	}
	// Vendor: 如果 fetcher 没提供(hostapd / DHCP-only 数据源), 尝试从
	// MAC 的 OUI 前缀查询内置数据库。ahsapd 自带 staVendor 字段, 会在
	// fetcher 层已填充, 这里不会覆盖。
	if d.Vendor == "" {
		d.Vendor = LookupVendor(d.MAC)
	}
	return d
}
