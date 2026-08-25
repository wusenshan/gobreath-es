package es

import (
	"log"
	"time"

	esapi "github.com/elastic/go-elasticsearch/v8/esapi"
)

// LogLevel 控制日志输出级别。
type LogLevel int

const (
	// LevelSilent 不输出任何日志（默认）。
	LevelSilent LogLevel = iota
	// LevelError 仅输出发生错误（或 HTTP 状态码 >= 400）的请求。
	LevelError
	// LevelInfo 输出全部请求（含成功）。
	LevelInfo
)

// LogEntry 一次 ES 请求的日志条目。
type LogEntry struct {
	Method string        // HTTP 方法，如 GET / POST / PUT / DELETE
	Path   string        // 请求的 ES 端点，如 products/_search
	Body   string        // 请求体（DSL / NDJSON），无则为空串
	Status int           // HTTP 响应状态码；请求未发出时为 0
	Took   time.Duration // 本次请求实际耗时（含网络往返）
	Err    error         // 请求级错误（网络 / 序列化等），nil 表示已收到响应
}

// LogFunc 日志回调。实现自己的日志器只需满足此签名，例如接入 zap / slog。
type LogFunc func(entry LogEntry)

// WithLogger 设置日志回调。设置后默认按 LevelInfo 输出（可用 WithLogLevel 调整为 LevelError）。
func (c *Client) WithLogger(f LogFunc) *Client {
	c.logger = f
	if c.logLevel == LevelSilent {
		c.logLevel = LevelInfo
	}
	return c
}

// WithLogLevel 设置日志级别（需在 WithLogger 之后或同时调用才有意义）。
func (c *Client) WithLogLevel(l LogLevel) *Client {
	c.logLevel = l
	return c
}

// doLog 包裹一次 esapi 调用：记录开始时间、执行 fn、按级别回传日志。
// fn 返回 (*esapi.Response, error)，与所有 esapi 高层方法签名一致。
func (c *Client) doLog(method, path string, body []byte, fn func() (*esapi.Response, error)) (*esapi.Response, error) {
	start := time.Now()
	res, err := fn()
	status := 0
	if res != nil {
		status = res.StatusCode
	}
	c.log(method, path, body, status, time.Since(start), err)
	return res, err
}

// log 根据级别决定是否调用 logger。
func (c *Client) log(method, path string, body []byte, status int, took time.Duration, err error) {
	if c.logger == nil || c.logLevel == LevelSilent {
		return
	}
	if c.logLevel == LevelError && err == nil && status < 400 {
		return
	}
	bodyStr := ""
	if len(body) > 0 {
		bodyStr = string(body)
	}
	c.logger(LogEntry{
		Method: method,
		Path:   path,
		Body:   bodyStr,
		Status: status,
		Took:   took,
		Err:    err,
	})
}

// NewStdLogger 返回一个打印到标准错误（log 包）的默认日志器。
// 输出格式：
//
//	[gobreath-es] POST products/_search 200 12.3ms
//	  {"query":{"match_all":{}}}
func NewStdLogger() LogFunc {
	return func(e LogEntry) {
		if e.Err != nil {
			log.Printf("[gobreath-es] %s %s ERROR status=%d %s: %v",
				e.Method, e.Path, e.Status, e.Took, e.Err)
			return
		}
		log.Printf("[gobreath-es] %s %s %d %s", e.Method, e.Path, e.Status, e.Took)
		if e.Body != "" {
			log.Printf("[gobreath-es]   %s", e.Body)
		}
	}
}
