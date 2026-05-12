package argustest_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	argus "github.com/xxl6097/argusd"
	"github.com/xxl6097/argusd/argustest"
)

func TestFixedFetcherReturnsDevicesCopy(t *testing.T) {
	src := []argus.Device{
		{MAC: "aa:bb:cc:dd:ee:01", IP: "192.168.1.1"},
		{MAC: "aa:bb:cc:dd:ee:02", IP: "192.168.1.2"},
	}
	f := &argustest.FixedFetcher{Devices: src}

	got, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch err=%v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d want 2", len(got))
	}

	// 修改返回值不应影响内部
	got[0].MAC = "aa:bb:cc:dd:ee:FF"
	got2, _ := f.Fetch(context.Background())
	if got2[0].MAC != "aa:bb:cc:dd:ee:01" {
		t.Errorf("Fetch 应返回副本, 外部修改不应污染: %+v", got2[0])
	}
	if f.Calls() != 2 {
		t.Errorf("Calls=%d want 2", f.Calls())
	}
}

func TestFixedFetcherReturnsError(t *testing.T) {
	sentinel := errors.New("boom")
	f := &argustest.FixedFetcher{Err: sentinel}

	_, err := f.Fetch(context.Background())
	if !errors.Is(err, sentinel) {
		t.Errorf("Fetch 应返回注入的错误, got %v", err)
	}
}

func TestFakeProberReachability(t *testing.T) {
	p := &argustest.FakeProber{
		Reach: map[string]bool{"1.1.1.1": true},
	}
	if !p.Reachable(context.Background(), "1.1.1.1") {
		t.Error("1.1.1.1 应可达")
	}
	if p.Reachable(context.Background(), "2.2.2.2") {
		t.Error("2.2.2.2 不在 Reach 中, 应不可达")
	}
}

func TestFakeProberAllReachable(t *testing.T) {
	p := &argustest.FakeProber{AllReachable: true}
	if !p.Reachable(context.Background(), "any-ip") {
		t.Error("AllReachable=true 应使所有 IP 可达")
	}
}

func TestFakeProberSetConcurrent(t *testing.T) {
	p := &argustest.FakeProber{}
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			p.Set("1.1.1.1", i%2 == 0)
		}(i)
		go func() {
			defer wg.Done()
			_ = p.Reachable(context.Background(), "1.1.1.1")
		}()
	}
	wg.Wait()
}

// 集成测试: 用 argustest 构造 Watcher, 验证 List 返回注入的设备。
func TestIntegrationWithWatcher(t *testing.T) {
	f := &argustest.FixedFetcher{
		Devices: []argus.Device{
			{MAC: "aa:bb:cc:dd:ee:01", IP: "192.168.1.1"},
		},
	}
	p := &argustest.FakeProber{Reach: map[string]bool{"192.168.1.1": true}}

	w := argus.New(
		argus.WithFetcher(f),
		argus.WithProber(p),
	)
	devs, err := w.List(context.Background())
	if err != nil {
		t.Fatalf("List err=%v", err)
	}
	if len(devs) != 1 || devs[0].MAC != "aa:bb:cc:dd:ee:01" {
		t.Errorf("List 未返回注入的设备: %+v", devs)
	}
}
