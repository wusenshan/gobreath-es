# gobreath-es

[![Go Reference](https://pkg.go.dev/badge/github.com/wusenshan/gobreath-es.svg)](https://pkg.go.dev/github.com/wusenshan/gobreath-es)
[![Build](https://github.com/wusenshan/gobreath-es/actions/workflows/ci.yml/badge.svg)](https://github.com/wusenshan/gobreath-es/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/go-1.23%2B-00ADD8)](https://go.dev)

这是一个面向 Elasticsearch 的 Go 框架，主 API 尽量贴近 Elasticsearch 自身语义，同时保留类型安全和 Go 友好的查询体验。

设计目标：

- 查询代码里尽量不出现原始字符串字段名
- 模型驱动的索引映射
- 类型安全的链式查询构造器
- 主 API 使用 ES 官方命名
- 为 ORM / SQL 用户保留薄兼容层
- 原生支持混合向量检索和 AI / RAG 场景

## 为什么选择 gobreath-es

它和 `gobreath-orm` 有相同的设计哲学：

- `Col[T]` 从结构体中提取字段，而不是手写字符串
- `Query[T]` 以 Go 代码拼装 ES bool 查询
- `Repo[T]` 把模型和索引绑定在一起，提供 ES 语义的 CRUD 能力
- mapping 能从 tag 和元数据自动推导
- 向量搜索是一级能力，并且可与正常过滤条件混合召回

这样做可以避免在项目里大量手写 JSON DSL 字符串。

## 主 API 命名风格

主 API 采用 Elasticsearch 自身的语义命名，而不是强行套 SQL / ORM 风格：

| ES 原生方法 | 含义 |
| --- | --- |
| `Index` | 写入或覆盖单个文档 |
| `Get` | 按文档 id 读取 |
| `Search` | 执行查询 |
| `Count` | 统计命中数 |
| `Update` | 按 id 局部更新 |
| `Delete` | 按 id 删除 |
| `BulkIndex` / `IndexMany` | 批量写入 |
| `CreateIndex` | 创建索引 / 映射 |
| `PutMapping` | 创建/更新映射 |
| `Nearest` | kNN 向量检索 |

同时保留一个薄的兼容层，方便 SQL / ORM 风格开发者迁移：

| 兼容别名 | 对应原生方法 |
| --- | --- |
| `Save` | `Index` |
| `Insert` | `Index` |
| `GetByID` | `Get` |
| `DeleteByID` | `Delete` |
| `UpdateByID` | `Update` |
| `IndexOne` | `Index` |
| `IndexMany` | `BulkIndex` |

这样主线是 ES 原生语义，兼容层只是过渡方案。

## 安装

```bash
go get github.com/wusenshan/gobreath-es
```

## 快速开始

```go
package main

import (
    "context"
    "log"
    "time"

    es "github.com/wusenshan/gobreath-es"
)

type Product struct {
    ID        string    `json:"id" es:"id"`
    Name      string    `json:"name"`
    Price     float64   `json:"price"`
    Category  string    `json:"category"`
    InStock   bool      `json:"in_stock"`
    CreatedAt time.Time `json:"created_at"`
}

func (Product) IndexName() string { return "products" }

func main() {
    ctx := context.Background()

    client, err := es.NewClient(
        es.WithAddresses("http://localhost:9200"),
    )
    if err != nil {
        log.Fatal(err)
    }

    if _, err := client.Ping(ctx); err != nil {
        log.Fatal(err)
    }

    repo := es.NewRepo[Product](client)

    if err := repo.CreateIndex(ctx, 1, 1); err != nil {
        log.Fatal(err)
    }

    docs := []Product{
        {ID: "p1", Name: "Phone", Price: 699, Category: "electronics", InStock: true, CreatedAt: time.Now()},
        {ID: "p2", Name: "Laptop", Price: 1299, Category: "electronics", InStock: true, CreatedAt: time.Now()},
    }

    if err := repo.IndexMany(ctx, docs); err != nil {
        log.Fatal(err)
    }

    q := es.NewQuery[Product]().
        Filter(func(q *es.Query[Product]) {
            q.Eq(es.Col[Product](func(p *Product) *string { return &p.Category }), "electronics")
        }).
        OrderBy(es.Col[Product](func(p *Product) *float64 { return &p.Price }), false).
        Size(10)

    result, err := repo.Search(ctx, q)
    if err != nil {
        log.Fatal(err)
    }

    log.Printf("hits=%d", result.Total)
}
```

## 模型映射与元数据

字段映射依赖 Go 的 tag 以及 ES 语义：

```go
type Product struct {
    ID      string    `json:"id" es:"id"`
    Title   string    `json:"title"`
    Price   float64   `json:"price"`
    Tags    []string  `json:"tags"`
    Embeds  []float32 `json:"embeds" es:"vector(1536)"`
    Source  string    `json:"source" es:"type:keyword"`
}
```

支持的 tag：

- `json:"name"` 表示文档字段名
- `es:"id"` 表示该字段作为 `_id`
- `es:"-"` 忽略字段
- `es:"type:keyword"` 强制映射类型
- `es:"vector(1536)"` 创建 `dense_vector` 字段，用于 kNN 向量搜索

如果没有显式索引名，框架会按类型名推导：

- `Product` -> `products`
- `UserProfile` -> `user_profiles`

## Query 构造器

核心查询对象是 `Query[T]`。

```go
price := es.Col[Product](func(p *Product) *float64 { return &p.Price })
category := es.Col[Product](func(p *Product) *string { return &p.Category })

q := es.NewQuery[Product]().
    Filter(func(q *es.Query[Product]) {
        q.Gte(price, 100)
        q.Eq(category, "electronics")
    }).
    OrderBy(price, false).
    Size(20)
```

常见条件：

- `Eq`, `Ne`, `In`
- `Gt`, `Gte`, `Lt`, `Lte`
- `Like`, `Exists`, `NotNull`, `Null`
- `Or`, `If`, `Nested`, `Filter`, `Must`, `MustNot`, `Should`

## 向量检索

向量搜索是框架的一等能力：

```go
vec := []float32{0.12, 0.33, 0.99}

res, err := repo.Search(ctx, es.NewQuery[Product]().
    Nearest(es.Col[Product](func(p *Product) *[]float32 { return &p.Embeds }), vec, 10).
    KnnNumCandidates(50).
    Filter(func(q *es.Query[Product]) {
        q.Eq(es.Col[Product](func(p *Product) *string { return &p.Category }), "electronics")
    }))
```

这类能力非常适合 AI / RAG / 语义搜索 / 混合召回场景。

## 聚合查询

```go
agg := es.NewAggregation().
    Terms("by_category", es.Col[Product](func(p *Product) *string { return &p.Category }), 10).
    Sub(es.NewAggregation().Avg("avg_price", es.Col[Product](func(p *Product) *float64 { return &p.Price })))

q := es.NewQuery[Product]().Aggregate(agg)

result, err := repo.Aggregate(ctx, q)
```

## 写入安全与逃生舱

### 乐观并发控制（OCC）

Elasticsearch 通过 `_seq_no` / `_primary_term` 支持冲突检测。先读取文档当前版本，
修改后再带上 `es.IfSeqNoPrimaryTerm` 写回：只有当文档版本未被其他人改动时才成功，
否则 ES 返回 409 冲突。

```go
doc, meta, err := repo.GetWithMeta(ctx, "p1") // meta.SeqNo / meta.PrimaryTerm
if err != nil {
    log.Fatal(err)
}
doc.Price = 999

// 仅当文档仍处于该版本时才写入成功
if err := repo.Index(ctx, doc, es.IfSeqNoPrimaryTerm(meta.SeqNo, meta.PrimaryTerm)); err != nil {
    log.Fatal(err)
}
```

`SearchResult` 同样按命中暴露 `SeqNos` / `PrimaryTerms`（以及 `HitMeta(i)` 方法），
因此批量检索后也能直接做乐观并发。`Update` 与 `Delete` 也接受同一选项。

### Upsert

文档不存在则插入，存在则作为局部更新合并。

```go
// 单文档（需声明 id 字段）
repo.Upsert(ctx, Product{ID: "p1", Name: "Phone"})

// 批量 upsert
repo.BulkUpsert(ctx, []Product{{ID: "p1"}, {ID: "p2"}})
```

### 原始检索逃生舱

当查询构造器覆盖不到（脚本排序、脚本指标聚合、冷门聚合等）时，直接以原始 DSL
（map）发起检索并拿回完整 ES 响应。

```go
raw, err := repo.SearchRaw(ctx, map[string]any{
    "query": map[string]any{"match_all": map[string]any{}},
    "aggs":  map[string]any{"max_price": map[string]any{"max": map[string]any{"field": "price"}}},
})
```

## ORM 风格兼容层

如果你更习惯 SQL / ORM 的命名，也可以直接用兼容别名：

```go
if err := repo.Insert(ctx, doc); err != nil {
    log.Fatal(err)
}

if err := repo.UpdateByID(ctx, "id-123", map[string]any{"price": 999}); err != nil {
    log.Fatal(err)
}

obj, err := repo.GetByID(ctx, "id-123")
```

但要注意：

- 原生 ES 命名是主 API
- ORM 风格只是兼容层
- 这是为了减少迁移成本，而不是为了把 ES 当成 SQL 数据库

## 为什么不把 SQL 当主 API

Elasticsearch SQL 对迁移和临时查询确实有用，但它并不是 ES 的本质查询模型，也不能完全等价 SQL 数据库的语义。

这个框架的主方向是：

- Go-native 的 ES 语义
- 类型安全
- 可组装的查询
- 更自然的 AI / kNN / filter 工作流

如果后续需要，还可以再增加一个 SQL 适配层作为辅助能力，而不是把它作为主入口。

## 项目现状

框架已经具备：

- 类型安全字段选择
- 查询构造器
- Repo 抽象层
- 映射生成
- 索引管理
- 聚合构造器
- 混合向量检索
- 乐观并发控制（OCC）
- Upsert（单文档与批量）
- 原始检索逃生舱
- 日志与请求跟踪

当前路线是：在保持 ES 原生语义主线的前提下，继续强化工程能力和可观测性。

## License

MIT
