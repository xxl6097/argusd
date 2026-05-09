// Command argusd is the reference consumer of the argus library.
// 启动时打印一次设备列表, 之后通过回调实时打印上线 / 离线 / 状态变更事件。
// 同时监听 OpenWrt 系统日志, 捕获 WiFi 关联 / 断开 / DHCP 分配等底层事件。
package main

import (
	"context"
	"fmt"
	"log"
	"os/signal"
	"strings"
	"syscall"

	owrt "github.com/xxl6097/argus"
)

func main() {
	log.SetFlags(log.LstdFlags)
	owrt.SetupLocalTimezone()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	w := owrt.New(
		owrt.OnFetcherDetected(func(k owrt.FetcherKind) {
			log.Printf("已选择数据源: %s", k)
		}),
		owrt.WithDecisionHandler(onDecision),
	)

	devices, err := w.List(ctx)
	if err != nil {
		log.Fatalf("初始拉取失败: %v", err)
	}
	fmt.Println(owrt.RenderTable(devices))

	// 启动外部系统日志监听用于展示 (库内部的 syslog 已自动纳入离线判断)
	go func() {
		if err := owrt.WatchSyslog(ctx, onSyslog, onError); err != nil {
			log.Printf("系统日志监听异常退出: %v", err)
		}
	}()

	log.Println("开始监听设备状态变化, Ctrl+C 退出 ...")
	if err := w.Run(ctx, onEvent, onError); err != nil {
		log.Fatalf("监听异常退出: %v", err)
	}
	log.Println("收到退出信号, 程序结束")
}

// onEvent 是库回调的事件处理函数, 演示如何按事件类型展示信息。
// 库返回的 e.Kind 是英文状态码 (ONLINE/OFFLINE/CHANGE), 通过 Label() 拿中文文案打印。
func onEvent(e owrt.Event) {
	ts := e.Time.Format("2006-01-02 15:04:05")
	switch e.Kind {
	case owrt.EventOnline, owrt.EventOffline:
		fmt.Printf("[%s] %s %s\n", ts, e.Kind.Label(), e.Device)
	case owrt.EventChange:
		parts := make([]string, 0, len(e.Changes))
		for _, c := range e.Changes {
			parts = append(parts, fmt.Sprintf("%s %q→%q", c.Field, c.Old, c.New))
		}
		fmt.Printf("[%s] %s %s (%s)\n", ts, e.Kind.Label(), strings.ToUpper(e.Device.MAC), strings.Join(parts, ", "))
	}
}

// onSyslog 处理系统日志事件。
func onSyslog(e owrt.SyslogEvent) {
	ts := e.Time.Format("2006-01-02 15:04:05")
	switch e.Kind {
	case owrt.SyslogDHCPAck:
		fmt.Printf("[%s] [系统日志] %s %s IP=%s\n", ts, e.Kind.Label(), strings.ToUpper(e.MAC), e.IP)
	default:
		iface := ""
		if e.Iface != "" {
			iface = " 接口=" + e.Iface
		}
		fmt.Printf("[%s] [系统日志] %s %s%s\n", ts, e.Kind.Label(), strings.ToUpper(e.MAC), iface)
	}
}

// onDecision 打印 Watcher 内部判定链路, 用于调试和观测。
// 业务消费者通常不需要关心, 本示例打印是为了演示决策过程可见性。
func onDecision(d owrt.Decision) {
	ts := d.Time.Format("2006-01-02 15:04:05")
	mac := strings.ToUpper(d.MAC)
	if d.Detail == "" {
		fmt.Printf("[%s] [决策] %s %s\n", ts, d.Kind.Label(), mac)
	} else {
		fmt.Printf("[%s] [决策] %s %s (%s)\n", ts, d.Kind.Label(), mac, d.Detail)
	}
}

// onError 处理库上报的非致命拉取错误。
func onError(err error) {
	log.Printf("拉取设备列表失败: %v", err)
}
