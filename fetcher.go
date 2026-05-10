package argus

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Fetcher 抽象一次性拉取当前接入设备列表的能力。
// 默认通过 DetectFetcher 自动从 ubus 上选择 AhsapdFetcher (厂商私有) 或
// HostapdFetcher (OpenWrt 官方 hostapd); 测试或对接其它服务时, 业务方可
// 自行实现该接口并通过 WithFetcher 注入。
type Fetcher interface {
	Fetch(ctx context.Context) ([]Device, error)
}

// AhsapdFetcher 调用厂商私有 ubus 服务 `ahsapd.sta getStaInfo` 拉取设备列表。
// 适用于带 ahsapd 的厂商固件 (例如 MediaTek 7981 平台)。
type AhsapdFetcher struct {
	// Timeout 限制单次 ubus 调用耗时; 0 表示不超时。
	Timeout time.Duration
}

// rawSta 对应 ubus 返回中的单条 station 记录。
type rawSta struct {
	IPAddress  string `json:"ipAddress"`
	HostName   string `json:"hostName"`
	MACAddress string `json:"macAddress"`
	StaVendor  string `json:"staVendor"`
	UpTime     string `json:"upTime"`
	AccessTime string `json:"accessTime"`
	RSSI       string `json:"rssi"`
	StaType    string `json:"staType"`
	Radio      string `json:"radio"`
	Channel    string `json:"channel"`
	SSID       string `json:"ssid"`
}

type rawResp struct {
	Ahsapd struct {
		StaDevices []rawSta `json:"staDevices"`
	} `json:"ahsapd.sta"`
}

// Fetch 实现 Fetcher 接口。
func (f AhsapdFetcher) Fetch(ctx context.Context) ([]Device, error) {
	if f.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, f.Timeout)
		defer cancel()
	}
	out, err := exec.CommandContext(ctx, "ubus", "call", "ahsapd.sta", "getStaInfo").Output()
	if err != nil {
		return nil, fmt.Errorf("ubus call ahsapd.sta getStaInfo: %w", err)
	}
	var r rawResp
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, fmt.Errorf("parse ubus ahsapd.sta JSON: %w", err)
	}

	now := time.Now()
	hints := loadHints(ctx)
	devs := make([]Device, 0, len(r.Ahsapd.StaDevices))
	for _, s := range r.Ahsapd.StaDevices {
		mac := normalizeMAC(s.MACAddress)
		if mac == "" {
			continue
		}
		d := Device{
			MAC:        mac,
			IP:         s.IPAddress,
			Hostname:   s.HostName,
			Vendor:     s.StaVendor,
			Type:       s.StaType,
			Radio:      s.Radio,
			SSID:       s.SSID,
			Channel:    parseInt(s.Channel),
			RSSI:       parseInt(s.RSSI),
			UpTime:     time.Duration(parseInt(s.UpTime)) * time.Second,
			AccessTime: parseAccessTime(s.AccessTime),
			LastSeen:   now,
		}
		devs = append(devs, applyHints(d, hints[mac]))
	}
	return devs, nil
}

// normalizeMAC 把 "B0FC36329461" 这种紧凑大写形式转为 "b0:fc:36:32:94:61"。
// 已是冒号分隔的输入也会被规范化为小写。
func normalizeMAC(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, ":", "")
	if len(s) != 12 {
		return ""
	}
	var b strings.Builder
	for i := 0; i < 12; i += 2 {
		if i > 0 {
			b.WriteByte(':')
		}
		b.WriteString(s[i : i+2])
	}
	return b.String()
}

// parseInt 尝试解析字符串为整数, 失败或空串统一返回 0。
//
// 语义注意: 对 RSSI/Channel/UpTime 等字段, 0 既表示"字段缺失/解析失败"也表示
// 原始值就是 0。实践中:
//   - RSSI=0 dBm 在 WiFi 中极罕见 (需要天线贴脸), 安全地当作"未知"
//   - Channel=0 是非法信道, 也视为"未知"
//   - UpTime=0s 视为"刚接入"或"未知", 二者都不影响判断
func parseInt(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}

func parseAccessTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.ParseInLocation("2006-01-02 15:04:05", s, time.Local)
	if err != nil {
		return time.Time{}
	}
	return t
}
