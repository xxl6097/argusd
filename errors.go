package argus

import "errors"

// Sentinel 错误, 可通过 errors.Is 判别。
//
// 使用方式:
//
//	if err := w.Run(ctx, onEvent, nil); err != nil {
//	    switch {
//	    case errors.Is(err, argus.ErrHandlerRequired):
//	        // 忘传回调
//	    case errors.Is(err, argus.ErrInvalidConfig):
//	        // Config 非法
//	    case errors.Is(err, argus.ErrNoFetcher):
//	        // ubus 上没有 ahsapd / hostapd 服务
//	    }
//	}
var (
	// ErrHandlerRequired 表示必填的回调 (典型: Run 的 onEvent) 为 nil。
	ErrHandlerRequired = errors.New("argus: handler required")
	// ErrInvalidConfig 表示 Config 字段取值非法 (由 Config.Validate 判定)。
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
