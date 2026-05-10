package argustest_test

import (
	"context"
	"fmt"

	argus "github.com/xxl6097/argus"
	"github.com/xxl6097/argus/argustest"
)

// ExampleFixedFetcher shows how library consumers use argustest.FixedFetcher
// to build deterministic unit tests for business logic that runs on top of
// a *argus.Watcher — without shelling out to ubus / ping.
func ExampleFixedFetcher() {
	f := &argustest.FixedFetcher{
		Devices: []argus.Device{
			{MAC: "aa:bb:cc:dd:ee:ff", IP: "192.168.1.10", Hostname: "laptop", RSSI: -50, Radio: "5G"},
		},
	}
	p := &argustest.FakeProber{Reach: map[string]bool{"192.168.1.10": true}}

	w := argus.New(
		argus.WithFetcher(f),
		argus.WithProber(p),
	)
	devs, _ := w.List(context.Background())
	fmt.Println(devs[0].MAC, devs[0].Hostname)
	// Output: aa:bb:cc:dd:ee:ff laptop
}
