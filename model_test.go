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
	Embedding []float32 `json:"embedding" es:"vector(1536)"`
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

// TestColOf 验证 ColOf[T](字段名) 与 Col 闭包等价，且调用点更短。
func TestColOf(t *testing.T) {
	if got := ColOf[product]("Name").name; got != "name" {
		t.Fatalf("ColOf(Name) 应为 name，实际 %q", got)
	}
	// 也接受直接传 ES 字段名
	if got := ColOf[product]("price").name; got != "price" {
		t.Fatalf("ColOf(price) 应为 price，实际 %q", got)
	}
	// 与闭包结果一致
	if ColOf[product]("CatID").name != Col[product](func(p *product) *int64 { return &p.CatID }).name {
		t.Fatalf("ColOf 与 Col 解析结果应一致")
	}
	// 不存在的字段应 panic
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("ColOf 不存在字段时应 panic")
		}
	}()
	_ = ColOf[product]("NoSuchField")
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

// TestBuildMappingVector 验证 es:"vector(N)" 字段生成 dense_vector mapping。
func TestBuildMappingVector(t *testing.T) {
	m := BuildMapping[product](0, 0)
	b, _ := json.Marshal(m)
	var m2 map[string]any
	_ = json.Unmarshal(b, &m2)
	props := m2["mappings"].(map[string]any)["properties"].(map[string]any)
	emb, ok := props["embedding"].(map[string]any)
	if !ok {
		t.Fatalf("embedding 字段缺失: %s", b)
	}
	if emb["type"] != "dense_vector" {
		t.Fatalf("embedding 期望 dense_vector，实际 %v", emb["type"])
	}
	if emb["dims"] != float64(1536) {
		t.Fatalf("embedding dims 期望 1536，实际 %v", emb["dims"])
	}
	if emb["index"] != true {
		t.Fatalf("embedding 期望开启 index（kNN 必需），实际 %v", emb["index"])
	}
	if emb["similarity"] != "cosine" {
		t.Fatalf("embedding similarity 期望 cosine（默认），实际 %v", emb["similarity"])
	}
}

func TestBuildIndexTemplate(t *testing.T) {
	body := BuildIndexTemplate[product](TemplateOptions{
		Patterns: []string{"products-*"},
		Priority: 200,
		Shards:   2,
		Replicas: 1,
		Version:  1,
		Meta:     map[string]any{"owner": "search-team"},
	})
	b, _ := json.Marshal(body)
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("模板非合法 JSON: %v", err)
	}
	if pats, ok := m["index_patterns"].([]any); !ok || len(pats) == 0 || pats[0] != "products-*" {
		t.Fatalf("index_patterns 错误: %v", m["index_patterns"])
	}
	if m["priority"] != float64(200) {
		t.Fatalf("priority 错误: %v", m["priority"])
	}
	tmpl, ok := m["template"].(map[string]any)
	if !ok {
		t.Fatalf("缺少 template: %s", b)
	}
	if _, ok := tmpl["mappings"].(map[string]any)["properties"]; !ok {
		t.Fatalf("template.mappings.properties 缺失: %s", b)
	}
	if tmpl["settings"].(map[string]any)["number_of_shards"] != float64(2) {
		t.Fatalf("settings.shards 错误: %s", b)
	}
	if m["version"] != float64(1) {
		t.Fatalf("version 错误")
	}
	if m["_meta"].(map[string]any)["owner"] != "search-team" {
		t.Fatalf("meta 错误")
	}
}
