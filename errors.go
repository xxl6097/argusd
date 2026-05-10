package argus

import (
	"errors"
	"fmt"
)

// Sentinel 错误, 可通过 errors.Is 判别。
//
// 使用方式:
//
//	if err := w.Run(ctx, onEvent, nil); err != nil {
//	    switch {
//	    case errors.Is(err, argus.ErrHandlerRequired):
//	        // 忘传回调
//	    case errors.Is(err, argus.ErrInvalidConfig):
//	        // Config 非法 (可用 argus.AsConfigError 取出具体字段)
//	    case errors.Is(err, argus.ErrNoFetcher):
//	        // ubus 上没有 ahsapd / hostapd 服务
//	    }
//	}
var (
	// ErrHandlerRequired 表示必填的回调 (典型: Run 的 onEvent) 为 nil。
	ErrHandlerRequired = errors.New("argus: handler required")
	// ErrInvalidConfig 表示 Config 字段取值非法 (由 Config.Validate 判定)。
	// Config.Validate 返回的具体错误会 wrap 这个 sentinel 且实现为 *ConfigError,
	// 允许 errors.As 拆出字段级详情。
	ErrInvalidConfig = errors.New("argus: invalid config")
	// ErrNoFetcher 表示未显式提供 Fetcher 且自动探测 ubus 时未找到可用数据源。
	ErrNoFetcher = errors.New("argus: no fetcher available")
	// ErrFetchFailed 包裹 Fetcher.Fetch 的失败, 便于上层通过 errors.Is 过滤拉取类错误。
	ErrFetchFailed = errors.New("argus: fetch failed")
	// ErrAlreadyRunning 表示 Watcher 已有一个 Run 正在执行。同一 Watcher 在任何时刻
	// 只能有一个 Run 活跃; 并发调用 Run 的后来者会立刻返回此错误, 避免共享状态被破坏。
	// 允许的模式是先 Stop 再 Run (重启语义), 参见 (*Watcher).Stop。
	ErrAlreadyRunning = errors.New("argus: watcher already running")
)

// ConfigError describes a single invalid Config field. Returned by
// Config.Validate wrapped under ErrInvalidConfig.
//
// Typical consumer (e.g. a web config UI) uses errors.As to extract the
// offending field for form-level feedback:
//
//	var ce *argus.ConfigError
//	if errors.As(err, &ce) {
//	    formErrors[ce.Field] = ce.Error()
//	}
//
// The Unwrap chain matches ErrInvalidConfig so errors.Is(err, ErrInvalidConfig)
// still works for coarse-grained matching.
type ConfigError struct {
	// Field is the Go-level struct field name (e.g. "OfflineMisses"),
	// stable across 0.x releases for any field in the Stable JSON list.
	Field string
	// Value is the offending value as passed by the caller (nil-safe —
	// zero values are included verbatim).
	Value any
	// Reason is a short human-readable description of the constraint
	// that was violated (e.g. "must be >= 1").
	Reason string
}

func (e *ConfigError) Error() string {
	return fmt.Sprintf("%s: %s (field=%q value=%v)", ErrInvalidConfig.Error(), e.Reason, e.Field, e.Value)
}

// Unwrap lets errors.Is(err, ErrInvalidConfig) succeed.
func (e *ConfigError) Unwrap() error { return ErrInvalidConfig }
