package argus

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// posixTZRe 匹配 OpenWrt 风格的 POSIX TZ 字符串, 例如:
//   "CST-8"          → UTC+8
//   "GMT0BST,..."    → UTC+0
//   "EST5EDT,..."    → UTC-5
var posixTZRe = regexp.MustCompile(`^([A-Z]+)([+-]?\d+(?:\.\d+)?)`)

// loadRouterLocation 尝试按 OpenWrt 习惯读取本机时区, 失败时返回 nil。
// 优先级:
//  1. /etc/TZ (POSIX 格式, 如 "CST-8")
//  2. TZ 环境变量
func loadRouterLocation() *time.Location {
	candidates := []string{}
	if data, err := os.ReadFile("/etc/TZ"); err == nil {
		candidates = append(candidates, strings.TrimSpace(string(data)))
	}
	if env := os.Getenv("TZ"); env != "" {
		candidates = append(candidates, env)
	}
	for _, tz := range candidates {
		if loc := parsePosixTZ(tz); loc != nil {
			return loc
		}
		if loc, err := time.LoadLocation(tz); err == nil {
			return loc
		}
	}
	return nil
}

// parsePosixTZ 解析 POSIX TZ 字符串中的标准时区部分。
// POSIX 偏移与 ISO 相反: "CST-8" 对应 UTC+8。
func parsePosixTZ(tz string) *time.Location {
	m := posixTZRe.FindStringSubmatch(tz)
	if m == nil {
		return nil
	}
	hours, err := strconv.ParseFloat(m[2], 64)
	if err != nil {
		return nil
	}
	// POSIX 符号反转: "CST-8" 表示 UTC+8 (偏移 +28800 秒)
	offsetSec := int(-hours * 3600)
	return time.FixedZone(m[1], offsetSec)
}

// DetectLocalLocation 按 OpenWrt 习惯探测路由器本机时区, 不修改全局状态。
// 优先级:
//  1. /etc/TZ (POSIX 格式, 如 "CST-8")
//  2. TZ 环境变量
// 探测失败时返回 nil。
func DetectLocalLocation() *time.Location {
	return loadRouterLocation()
}

// SetupLocalTimezone 显式将 time.Local 设置为路由器本机时区。
// **修改全局状态** time.Local, 影响整个进程。建议在 main 函数最早期调用一次。
//
// 注意: 此函数会写 time.Local, 在测试中调用会污染全局状态。
// 若库使用者已自行管理时区, 改用 DetectLocalLocation() 获取 *time.Location 即可。
//
// 在交叉编译且未引入 time/tzdata 的二进制中, 默认 time.Local 会回退到 UTC。
// 此函数能让 OpenWrt 路由器上的时间输出与系统时间一致。
//
// 返回选中的时区, 未识别时返回 UTC 不修改 time.Local。
func SetupLocalTimezone() *time.Location {
	if loc := loadRouterLocation(); loc != nil {
		time.Local = loc
		return loc
	}
	return time.UTC
}
