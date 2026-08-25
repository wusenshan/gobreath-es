package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	es "github.com/wusenshan/gobreath-es"
)

// Product 业务文档模型。字段名以 json tag 为准（与 ES 存储的 JSON key 一致）。
type Product struct {
	ID        string    `json:"id" es:"id"`
	Name      string    `json:"name"`
	Price     float64   `json:"price"`
	CatID     int64     `json:"cat_id"`
	Tags      []string  `json:"tags"`
	InStock   bool      `json:"in_stock"`
	CreatedAt time.Time `json:"created_at"`
}

func main() {
	addr := os.Getenv("ES_ADDR")
	if addr == "" {
		fmt.Println("gobreath-es example")
		fmt.Println("未检测到 ES_ADDR 环境变量，跳过真实 ES 操作。")
		fmt.Println("用法：")
		fmt.Println("  ES_ADDR=http://localhost:9200 ES_USER=elastic ES_PASS=xxxx go run ./example")
		fmt.Println("\n本示例演示的 API（无需 ES 即可阅读）：")
		demoAPISurface()
		demoDSL()
		return
	}

	ctx := context.Background()
	var opts []es.ClientOption
	opts = append(opts, es.WithAddresses(strings.Split(addr, ",")...))
	if u := os.Getenv("ES_USER"); u != "" {
		opts = append(opts, es.WithBasicAuth(u, os.Getenv("ES_PASS")))
	}
	if k := os.Getenv("ES_API_KEY"); k != "" {
		opts = append(opts, es.WithAPIKey(k))
	}

	client, err := es.NewClient(opts...)
	if err != nil {
		log.Fatalf("连接 ES 失败: %v", err)
	}
	if _, err := client.Ping(ctx); err != nil {
		log.Fatalf("ES 不可达: %v", err)
	}
	fmt.Println("✓ 已连接 ES")

	repo := es.NewRepo[Product](client)

	// 1) 自动建索引（按模型推导 mapping）
	if err := repo.CreateIndex(ctx, 1, 1); err != nil {
		log.Fatalf("建索引失败: %v", err)
	}
	fmt.Println("✓ 索引 products 已就绪")

	// 2) 批量写入
	now := time.Now()
	docs := []Product{
		{ID: "p1", Name: "iPhone 15", Price: 5999, CatID: 1, Tags: []string{"phone", "apple"}, InStock: true, CreatedAt: now},
		{ID: "p2", Name: "MacBook Pro", Price: 14999, CatID: 1, Tags: []string{"laptop", "apple"}, InStock: true, CreatedAt: now},
		{ID: "p3", Name: "AirPods", Price: 999, CatID: 2, Tags: []string{"audio"}, InStock: false, CreatedAt: now},
	}
	if err := repo.BulkIndex(ctx, docs); err != nil {
		log.Fatalf("批量写入失败: %v", err)
	}
	fmt.Printf("✓ 写入 %d 条文档\n", len(docs))

	// 3) 条件检索：价格 >= 1000 且 有库存，按价格降序，取前 10
	price := es.Col[Product](func(p *Product) *float64 { return &p.Price })
	stock := es.Col[Product](func(p *Product) *bool { return &p.InStock })
	res, err := repo.Search(ctx, es.NewQuery[Product]().
		Ge(price, 1000).
		Filter(func(q *es.Query[Product]) { q.Eq(stock, true) }).
		OrderBy(price, false).
		Size(10))
	if err != nil {
		log.Fatalf("检索失败: %v", err)
	}
	fmt.Printf("== 检索命中 %d 条（took=%dms）\n", res.Total, res.Took)
	for _, p := range res.Hits {
		fmt.Printf("  - %s  ¥%.0f  cat=%d\n", p.Name, p.Price, p.CatID)
	}

	// 4) 聚合：按 cat_id 分桶，桶内算均价
	cat := es.Col[Product](func(p *Product) *int64 { return &p.CatID })
	byCat := es.NewAggregation().Terms("by_cat", cat, 10)
	byCat.Sub(es.NewAggregation().Avg("avg_price", price))
	aggs := es.NewAggregation()
	aggs.Add(byCat)
	aggRes, err := repo.Aggregate(ctx, es.NewQuery[Product]().Aggregate(aggs))
	if err != nil {
		log.Fatalf("聚合失败: %v", err)
	}
	fmt.Printf("== 聚合结果: %v\n", aggRes)

	// 5) 按 id 读取 + 更新 + 删除
	got, err := repo.Get(ctx, "p1")
	if err != nil {
		log.Fatalf("读取失败: %v", err)
	}
	fmt.Printf("== Get(p1) -> %s, price=%.0f\n", got.Name, got.Price)

	if err := repo.Update(ctx, "p1", map[string]any{"price": 5799}); err != nil {
		log.Fatalf("更新失败: %v", err)
	}
	fmt.Println("✓ 已将 p1 价格更新为 5799")

	if err := repo.Delete(ctx, "p3"); err != nil {
		log.Fatalf("删除失败: %v", err)
	}
	fmt.Println("✓ 已删除 p3")
}

// demoAPISurface 在缺少 ES 时，打印本框架暴露的核心 API 形状，便于离线阅读。
func demoAPISurface() {
	fmt.Println("  es.NewClient(es.WithAddresses(...), es.WithBasicAuth(...))")
	fmt.Println("  es.NewRepo[Product](client)")
	fmt.Println("  repo.CreateIndex(ctx, shards, replicas)")
	fmt.Println("  repo.Index(ctx, doc) / repo.BulkIndex(ctx, docs)")
	fmt.Println("  repo.Get(ctx, id) / repo.Exists / repo.Delete / repo.Update")
	fmt.Println("  repo.Search(ctx, es.NewQuery[Product]().Eq(Col, v).Ge(Col, n).OrderBy(Col, false).Size(n))")
	fmt.Println("  repo.Count(ctx, q) / repo.Aggregate(ctx, q)")
	fmt.Println("  es.NewAggregation().Terms(...).Avg(...)")
	fmt.Println("  -- 突破 10000 上限 --")
	fmt.Println("  pitID, _ := repo.OpenPIT(ctx, \"1m\")")
	fmt.Println("  repo.Search(ctx, es.NewQuery[Product]().OrderBy(priceCol,true).PIT(pitID,\"\").SearchAfter(1000.0))")
	fmt.Println("  repo.ClosePIT(ctx, pitID)")
	fmt.Println("  es.NewComposite(\"by_cat\").Terms(\"cat\",catCol,\"\").Size(1000) // 突破 search.max_buckets")
	fmt.Println("  repo.PutIndexTemplate(ctx, name, es.BuildIndexTemplate[Product](es.TemplateOptions{...}))")
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// demoDSL 在缺少 ES 时，直接打印新特性的查询 DSL，便于离线核对结构。
func demoDSL() {
	price := es.Col[Product](func(p *Product) *float64 { return &p.Price })
	id := es.Col[Product](func(p *Product) *string { return &p.ID })
	cat := es.Col[Product](func(p *Product) *int64 { return &p.CatID })

	fmt.Println("\n[demo] search_after + PIT 深翻页 DSL:")
	sa := es.NewQuery[Product]().
		Ge(price, 1000).
		OrderBy(price, true).
		OrderBy(id, true).
		Size(50).
		SearchAfter(1000.0, "p999")
	fmt.Println("  " + sa.Build())

	fmt.Println("[demo] composite 聚合 DSL（突破 search.max_buckets）:")
	comp := es.NewComposite("by_cat").Terms("cat", cat, "")
	aggs := es.NewAggregation()
	aggs.Add(comp.Agg())
	fmt.Println("  " + mustJSON(aggs.Build()))

	fmt.Println("[demo] 索引模板 DSL:")
	tmpl := es.BuildIndexTemplate[Product](es.TemplateOptions{
		Patterns: []string{"products-*"},
		Priority: 100,
		Shards:   1,
		Replicas: 1,
		Meta:     map[string]any{"owner": "demo"},
	})
	fmt.Println("  " + mustJSON(tmpl))
}
