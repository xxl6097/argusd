package argus

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// FetcherKind 标识自动选择的 Fetcher 类型, 便于打日志或上报。
type FetcherKind string

const (
	FetcherAhsapd  FetcherKind = "ahsapd"
	FetcherHostapd FetcherKind = "hostapd"
)

// DetectFetcher 探测路由器本机 ubus 上可用的接入设备数据源, 优先 ahsapd,
// 回退到 hostapd; 都不存在时返回错误。timeout 限制单次 ubus 探测耗时。
//
// 同时返回选中的 FetcherKind, 调用方可据此打日志。
func DetectFetcher(ctx context.Context, timeout time.Duration) (Fetcher, FetcherKind, error) {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	services, err := listUbusServices(ctx, timeout)
	if err != nil {
		return nil, "", fmt.Errorf("无法读取 ubus 服务列表: %w", err)
	}

	if contains(services, "ahsapd.sta") {
		return AhsapdFetcher{Timeout: timeout}, FetcherAhsapd, nil
	}

	var hostapdIfaces []string
	for _, s := range services {
		if strings.HasPrefix(s, "hostapd.") && s != "hostapd" {
			hostapdIfaces = append(hostapdIfaces, s)
		}
	}
	if len(hostapdIfaces) > 0 {
		return HostapdFetcher{Interfaces: hostapdIfaces, Timeout: timeout}, FetcherHostapd, nil
	}

	return nil, "", fmt.Errorf("未在 ubus 上找到 ahsapd.sta 或 hostapd.* 服务")
}

func listUbusServices(ctx context.Context, timeout time.Duration) ([]string, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := exec.CommandContext(cctx, "ubus", "list").Output()
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(out)), nil
}

func contains(ss []string, target string) bool {
	for _, s := range ss {
		if s == target {
			return true
		}
	}
	return false
}
