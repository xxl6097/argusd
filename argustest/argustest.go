// Package argustest 提供 argus 库使用者编写单元测试时可复用的测试替身。
//
// FixedFetcher / FakeProber 是 argus.Fetcher / argus.Prober 接口的最小实现,
// 对比内部 fixture 的优势是: 在你的业务代码里直接 import, 无需 fork 或复制。
//
// 典型用法:
//
//	import (
//	    argus "github.com/xxl6097/argusd"
//	    "github.com/xxl6097/argusd/argustest"
//	)
//
//	func TestMyBusinessLogic(t *testing.T) {
//	    w := argus.New(
//	        argus.WithFetcher(argustest.FixedFetcher{
//	            Devices: []argus.Device{
//	                {MAC: "aa:bb:cc:dd:ee:ff", IP: "192.168.1.10", RSSI: -50},
//	            },
//	        }),
//	        argus.WithProber(argustest.FakeProber{
//	            Reach: map[string]bool{"192.168.1.10": true},
//	        }),
//	    )
//	    // ... 你的业务测试 ...
//	}
package argustest

import (
	"context"
	"sync"

	argus "github.com/xxl6097/argusd"
)

// FixedFetcher 是一个返回固定设备列表的 argus.Fetcher 实现, 可选注入错误。
//
// 并发安全: 通过内部 mutex 保护 Devices / Err 的读取 (使用方无需加锁)。
// 但直接修改字段需要在无并发 Fetch 时执行。
type FixedFetcher struct {
	// Devices 每次 Fetch 返回的设备切片 (深拷贝给调用方, 避免修改共享状态)。
	Devices []argus.Device
	// Err 非 nil 时 Fetch 直接返回该错误, Devices 被忽略。
	Err error
	// Calls 原子计数 Fetch 被调用的次数, 便于断言交互行为。
	calls int
	mu    sync.Mutex
}

// Fetch 实现 argus.Fetcher 接口, 返回 Devices 的深拷贝或 Err。
func (f *FixedFetcher) Fetch(ctx context.Context) ([]argus.Device, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.Err != nil {
		return nil, f.Err
	}
	out := make([]argus.Device, len(f.Devices))
	copy(out, f.Devices)
	return out, nil
}

// Calls 返回 Fetch 被调用的累计次数 (并发安全)。
func (f *FixedFetcher) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// FakeProber 是一个根据 IP 映射返回可达性的 argus.Prober 实现。
//
// 未在 Reach 映射中的 IP 默认返回 false (不可达)。AllReachable=true 时
// 无条件返回 true, 用于"所有设备都在线"的简化测试场景。
//
// 并发安全: Reach map 在 NewFakeProber / 直接赋值之后不应再被修改;
// 若需要运行时调整, 使用 Set 方法。
type FakeProber struct {
	// Reach IP → 可达性的静态映射。
	Reach map[string]bool
	// AllReachable 置为 true 时忽略 Reach, Reachable 始终返回 true。
	AllReachable bool

	mu sync.Mutex
}

// Reachable 实现 argus.Prober 接口。
func (p *FakeProber) Reachable(ctx context.Context, ip string) bool {
	if p.AllReachable {
		return true
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.Reach[ip]
}

// Set 并发安全地更新某 IP 的可达性 (测试中模拟设备离开 / 恢复)。
func (p *FakeProber) Set(ip string, reachable bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.Reach == nil {
		p.Reach = map[string]bool{}
	}
	p.Reach[ip] = reachable
}
