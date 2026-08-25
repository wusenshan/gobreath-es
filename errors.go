package es

import "errors"

// ErrNotFound 表示文档不存在（ES 返回 404）。
var ErrNotFound = errors.New("gobreath-es: document not found")
