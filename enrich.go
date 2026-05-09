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

// hint 是从系统 ARP / DHCP 表中拿到的辅助信息, 用于在 ahsapd 数据缺失时
// 填补 IP / 主机名。
type hint struct {
	IP       string
	Hostname string
}

// 缓存 loadHints 的结果, 避免每秒多次 fork `ip neigh show` 子进程和读取 dhcp.leases。
// TTL 5 秒是 DHCP 租约 / ARP 表更新频率之内的合理折衷。
var (
	hintsCacheMu  sync.Mutex
	hintsCache    map[string]hint
	hintsCacheAt  time.Time
	hintsCacheTTL = 5 * time.Second
)

// loadHints 合并 /tmp/dhcp.leases 和 `ip neigh` 的输出, 按小写 MAC 索引。
// 带 TTL 缓存, 相邻调用在缓存有效期内直接返回副本。
func loadHints(ctx context.Context) map[string]hint {
	hintsCacheMu.Lock()
	if hintsCache != nil && time.Since(hintsCacheAt) < hintsCacheTTL {
		out := make(map[string]hint, len(hintsCache))
		for k, v := range hintsCache {
			out[k] = v
		}
		hintsCacheMu.Unlock()
		return out
	}
	hintsCacheMu.Unlock()

	fresh := map[string]hint{}
	loadDHCPLeases("/tmp/dhcp.leases", fresh)
	loadARP(ctx, fresh)

	hintsCacheMu.Lock()
	hintsCache = fresh
	hintsCacheAt = time.Now()
	// 返回给调用方独立的副本, 避免调用方修改影响缓存
	out := make(map[string]hint, len(fresh))
	for k, v := range fresh {
		out[k] = v
	}
	hintsCacheMu.Unlock()
	return out
}

// invalidateHintsCache 清除缓存, 主要用于测试或强制刷新。
func invalidateHintsCache() {
	hintsCacheMu.Lock()
	hintsCache = nil
	hintsCacheAt = time.Time{}
	hintsCacheMu.Unlock()
}

// loadDHCPLeases 解析 dnsmasq 风格租约: <expire> <mac> <ip> <hostname> <client-id>
func loadDHCPLeases(path string, into map[string]hint) {
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

// loadARP 解析 `ip neigh show`, 跳过 IPv6 链路本地与 FAILED/INCOMPLETE 表项。
func loadARP(ctx context.Context, into map[string]hint) {
	out, err := exec.CommandContext(ctx, "ip", "neigh", "show").Output()
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
func applyHints(d Device, h hint) Device {
	if d.IP == "" {
		d.IP = h.IP
	}
	if d.Hostname == "" {
		d.Hostname = h.Hostname
	}
	return d
}
