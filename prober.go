package argus

import (
	"context"
	"net"
	"os/exec"
	"regexp"
	"strconv"
	"sync"
	"time"
)

// ipv4Re 仅放行合法 IPv4 地址, 防止命令注入。
var ipv4Re = regexp.MustCompile(`^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}$`)

// Prober 抽象设备活性探测; 用于识别"AP 关联表里仍然挂着, 但实际已经走出无线范围
// 或离开 LAN"的场景。Reachable 应在内部自行处理超时, 返回 true 表示可达。
type Prober interface {
	Reachable(ctx context.Context, ip string) bool
}

// ICMPProber 基于本机 `ping` 命令实现 Prober。
// Timeout 既是单次 ping 的等待秒数, 也是 Reachable 调用的最大耗时。
type ICMPProber struct {
	Timeout time.Duration
}

// Reachable 实现 Prober 接口。空 IP 视为不可探测, 直接返回 true (信任 AP 关联表)。
func (p ICMPProber) Reachable(ctx context.Context, ip string) bool {
	if ip == "" {
		return true
	}
	if !ipv4Re.MatchString(ip) || net.ParseIP(ip) == nil {
		return false
	}
	wait := int(p.Timeout.Seconds())
	if wait < 1 {
		wait = 1
	}
	cctx, cancel := context.WithTimeout(ctx, p.Timeout+500*time.Millisecond)
	defer cancel()
	return exec.CommandContext(cctx, "ping", "-c", "1", "-W", strconv.Itoa(wait), ip).Run() == nil
}

// filterAlive 并行探测每个设备的 IP, 返回可达设备的子集。
// 若 prober 为 nil 直接返回原 map。空 IP 设备无法探测, 按可达处理。
func filterAlive(ctx context.Context, devs map[string]Device, prober Prober) map[string]Device {
	if prober == nil {
		return devs
	}
	out := make(map[string]Device, len(devs))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for mac, d := range devs {
		if d.IP == "" {
			// 空 IP 的设备不走 goroutine, 但仍需加锁写 out 避免与并发 goroutine 竞争
			mu.Lock()
			out[mac] = d
			mu.Unlock()
			continue
		}
		wg.Add(1)
		go func(mac string, d Device) {
			defer wg.Done()
			if !prober.Reachable(ctx, d.IP) {
				return
			}
			mu.Lock()
			out[mac] = d
			mu.Unlock()
		}(mac, d)
	}
	wg.Wait()
	return out
}
