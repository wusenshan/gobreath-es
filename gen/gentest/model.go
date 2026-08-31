package gentest

import "time"

//go:generate go run ../../cmd/esgen -type Product -out product_cols.go -dir .

// Product 演示用文档模型，字段名以 json tag 为准（与 ES 存储的 JSON key 一致）。
type Product struct {
	ID        string    `json:"id" es:"id"`
	Name      string    `json:"name"`
	Price     float64   `json:"price"`
	CatID     int64     `json:"cat_id"`
	Tags      []string  `json:"tags"`
	InStock   bool      `json:"in_stock"`
	CreatedAt time.Time `json:"created_at"`
	Embedding []float32 `json:"embedding" es:"vector(3)"`
	secret    string    // 非导出字段，生成器自动跳过
	Ignored   string    `json:"-"` // 显式忽略，生成器自动跳过
}
