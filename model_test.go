package es

import (
	"encoding/json"
	"testing"
	"time"
)

type product struct {
	ID        string    `json:"id" es:"id"`
	Name      string    `json:"name"`
	Price     float64   `json:"price"`
	CatID     int64     `json:"cat_id"`
	Tags      []string  `json:"tags"`
	InStock   bool      `json:"in_stock"`
	CreatedAt time.Time `json:"created_at"`
	Secret    string    `json:"-" es:"-"`
}

func TestMetaIndexDerivation(t *testing.T) {
	m := getMeta[product]()
	if m.index != "products" {
		t.Fatalf("期望索引名 products，实际 %q", m.index)
	}
	if m.idField == nil || m.idField.goName != "ID" {
		t.Fatalf("期望 ID 被识别为 id 字段")
	}
	// 忽略字段不应进入字段列表
	for _, f := range m.fields {
		if f.ignore && f.goName != "Secret" {
			t.Fatalf("不应有非忽略字段被标记 ignore: %s", f.goName)
		}
	}
}

func TestColResolution(t *testing.T) {
	if got := Col[product](func(p *product) *string { return &p.Name }).name; got != "name" {
		t.Fatalf("Col(Name) 应为 name，实际 %q", got)
	}
	if got := Col[product](func(p *product) *float64 { return &p.Price }).name; got != "price" {
		t.Fatalf("Col(Price) 应为 price，实际 %q", got)
	}
}

func TestBuildMapping(t *testing.T) {
	m := BuildMapping[product](1, 1)
	b, _ := json.Marshal(m)
	var m2 map[string]any
	_ = json.Unmarshal(b, &m2)

	mappings, ok := m2["mappings"].(map[string]any)
	if !ok {
		t.Fatalf("mapping 缺少 mappings 包裹: %s", b)
	}
	props := mappings["properties"].(map[string]any)
	name, _ := props["name"].(map[string]any)
	if name["type"] != "text" {
		t.Fatalf("name 期望 text，实际 %v", name["type"])
	}
	if _, ok := name["fields"].(map[string]any)["keyword"]; !ok {
		t.Fatalf("name 缺少 keyword 子字段")
	}
	price := props["price"].(map[string]any)
	if price["type"] != "double" {
		t.Fatalf("price 期望 double，实际 %v", price["type"])
	}
	cat := props["cat_id"].(map[string]any)
	if cat["type"] != "long" {
		t.Fatalf("cat_id 期望 long，实际 %v", cat["type"])
	}
	created := props["created_at"].(map[string]any)
	if created["type"] != "date" {
		t.Fatalf("created_at 期望 date，实际 %v", created["type"])
	}
	if _, ok := props["secret"]; ok {
		t.Fatalf("secret 不应出现在 mapping")
	}
	if m2["settings"] == nil {
		t.Fatalf("应设置 settings")
	}
}
