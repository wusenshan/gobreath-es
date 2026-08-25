package es

import (
	"errors"
	"testing"
	"time"
)

func TestLogGating(t *testing.T) {
	// LevelInfo：成功请求也记录
	var got []LogEntry
	c := &Client{logger: func(e LogEntry) { got = append(got, e) }, logLevel: LevelInfo}
	c.log("POST", "idx/_search", []byte(`{}`), 200, time.Millisecond, nil)
	if len(got) != 1 {
		t.Fatalf("LevelInfo 应记录成功请求, got=%d", len(got))
	}

	// LevelSilent：完全不记录
	c = &Client{logger: func(e LogEntry) { got = append(got, e) }, logLevel: LevelSilent}
	got = nil
	c.log("POST", "idx/_search", []byte(`{}`), 200, time.Millisecond, nil)
	if len(got) != 0 {
		t.Fatalf("LevelSilent 不应记录, got=%d", len(got))
	}

	// LevelError：仅记录错误/4xx
	c = &Client{logger: func(e LogEntry) { got = append(got, e) }, logLevel: LevelError}
	got = nil
	c.log("POST", "idx/_search", []byte(`{}`), 200, time.Millisecond, nil) // 成功 -> 不记录
	if len(got) != 0 {
		t.Fatalf("LevelError 应跳过 200, got=%d", len(got))
	}
	c.log("POST", "idx/_search", []byte(`{}`), 400, time.Millisecond, nil) // 4xx -> 记录
	if len(got) != 1 {
		t.Fatalf("LevelError 应记录 400, got=%d", len(got))
	}
	c.log("GET", "idx/_doc/1", nil, 0, time.Millisecond, errors.New("boom")) // err -> 记录
	if len(got) != 2 {
		t.Fatalf("LevelError 应记录 err, got=%d", len(got))
	}
}

func TestStdLoggerNoPanic(t *testing.T) {
	f := NewStdLogger()
	f(LogEntry{Method: "POST", Path: "idx/_search", Body: `{"a":1}`, Status: 200, Took: time.Millisecond})
	f(LogEntry{Method: "GET", Path: "idx/_doc/1", Status: 0, Took: time.Millisecond, Err: errors.New("x")})
}
