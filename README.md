# gobreath-es

[![Go Reference](https://pkg.go.dev/badge/github.com/wusenshan/gobreath-es.svg)](https://pkg.go.dev/github.com/wusenshan/gobreath-es)
[![Build](https://github.com/wusenshan/gobreath-es/actions/workflows/ci.yml/badge.svg)](https://github.com/wusenshan/gobreath-es/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/go-1.23%2B-00ADD8)](https://go.dev)

> 用此 ES 框架查数据像呼吸一样简单。

`gobreath-es` 是一个 **lambda 风格、类型安全** 的 Elasticsearch Go 框架，设计哲学与
[`gobreath-orm`](https://github.com/wusenshan/gobreath-orm) 完全一致：

- **调用点零字符串字段名**：通过 `es.Col[T](picker)` 闭包 + 反射从结构体 `json`/`es` tag 取字段名，
  写错字段在编译期（类型不匹配）或运行期（tag 拼错）立刻暴露，而不是生成一条字段名错误的查询。
- **参数化防注入**：查询条件全部以 DSL 结构传递，没有字符串拼接 SQL 那种注入面。
- **查询构造器产出标准 ES DSL**：`NewQuery[T]()` 链式拼条件，最后 `BuildBody()` 生成符合 ES 约定的请求体。
- **泛型仓储 `Repo[T]`**：把模型、索引、CRUD、聚合、检索绑定到一处，调用点无需重复写索引名与类型参数。

底层基于官方 [`go-elasticsearch/v8`](https://github.com/elastic/go-elasticsearch)，DSL 完全由本框架构造，对 ES 7.x / 8.x 通用。

---

## 安装

```bash
go get github.com/wusenshan/gobreath-es
```

要求 Go 1.23+。

---

## 请求日志

开启后，每次发给 ES 的**真实请求**（HTTP 方法、端点、请求体 DSL、响应状态码、耗时）都会通过回调输出，方便调试与排查——等价于 gobreath-orm 的 SQL 日志。

```go
client, _ := es.NewClient(es.WithAddresses("http://localhost:9200"))

// 方式一：内置默认日志器（输出到标准错误）
client = client.WithLogger(es.NewStdLogger())

// 方式二：自定义（可接 slog / zap）
client = client.WithLogger(func(e es.LogEntry) {
    fmt.Printf("%s %s -> %d (%s)\n", e.Method, e.Path, e.Status, e.Took)
    if e.Body != "" {
        fmt.Println(e.Body) // 即实际发给 ES 的 DSL / NDJSON
    }
    if e.Err != nil {
        fmt.Println("err:", e.Err)
    }
})

// 级别控制：LevelInfo(默认，全部) / LevelError(仅错误与 4xx) / LevelSilent(关闭)
client = client.WithLogLevel(es.LevelError)
```

开启 `LevelInfo` 后终端示例：

```
[gobreath-es] POST products/_search 200 12.3ms
[gobreath-es]   {"query":{"range":{"price":{"gte":1000}}},"sort":[{"price":"desc"}],"size":10}
[gobreath-es] POST products/_bulk 200 31.0ms
```

> 日志在请求发出、收到响应后回放；`e.Body` 就是直接贴进 Kibana Dev Tools / `curl` 即可复现的 DSL。

---

## 模型定义

字段名以 **`json` tag** 为准（ES 文档本质是 JSON，这样「查询条件」和「文档序列化」天然一致）。
`es` tag 用于框架专属元数据：

| tag | 含义 |
|---|---|
| `json:"name"` | 文档字段名（与序列化一致） |
| `es:"id"` | 该字段作为文档 `_id` 来源（写入时作为 `_id`，读取时回填） |
| `es:"-"` | 忽略该字段（不索引、不查询） |
| `es:"type:keyword"` | 显式指定 mapping 类型，覆盖自动推断 |
| `json:"-"` | 同时被框架视为忽略 |

索引名默认取类型名的复数蛇形（如 `Product` → `products`），实现 `IndexName() string` 接口可显式指定。

```go
type Product struct {
    ID        string    `json:"id" es:"id"`
    Name      string    `json:"name"`
    Price     float64   `json:"price"`
    CatID     int64     `json:"cat_id"`
    Tags      []string  `json:"tags"`
    InStock   bool      `json:"in_stock"`
    CreatedAt time.Time `json:"created_at"`
}
```

---

## 快速开始

```go
package main

import (
    "context"
    "log"
    "time"

    es "github.com/wusenshan/gobreath-es"
)

func main() {
    ctx := context.Background()

    // 1) 创建客户端
    client, err := es.NewClient(
        es.WithAddresses("http://localhost:9200"),
        es.WithBasicAuth("elastic", "password"),
    )
    if err != nil {
        log.Fatal(err)
    }
    if _, err := client.Ping(ctx); err != nil {
        log.Fatal("ES 不可达:", err)
    }

    // 2) 绑定仓储（自动按模型推导索引名 products）
    repo := es.NewRepo[Product](client)

    // 3) 自动建索引（按模型生成 mapping；已存在则幂等跳过）
    if err := repo.CreateIndex(ctx, 1, 1); err != nil {
        log.Fatal(err)
    }

    // 4) 写入 / 批量写入
    now := time.Now()
    if err := repo.BulkIndex(ctx, []Product{
        {ID: "p1", Name: "iPhone 15", Price: 5999, CatID: 1, Tags: []string{"phone"}, InStock: true, CreatedAt: now},
        {ID: "p2", Name: "MacBook Pro", Price: 14999, CatID: 1, Tags: []string{"laptop"}, InStock: true, CreatedAt: now},
    }); err != nil {
        log.Fatal(err)
    }

    // 5) 条件检索：价格 >= 1000 且 有库存，按价格降序取前 10
    price := es.Col[Product](func(p *Product) *float64 { return &p.Price })
    stock := es.Col[Product](func(p *Product) *bool { return &p.InStock })
    res, err := repo.Search(ctx, es.NewQuery[Product]().
        Ge(price, 1000).
        Filter(func(q *es.Query[Product]) { q.Eq(stock, true) }).
        OrderBy(price, false).
        Size(10))
    if err != nil {
        log.Fatal(err)
    }
    log.Printf("命中 %d 条", res.Total)
    for _, p := range res.Hits {
        log.Printf("%s ¥%.0f", p.Name, p.Price)
    }

    // 6) 聚合：按 cat_id 分桶，桶内算均价
    cat := es.Col[Product](func(p *Product) *int64 { return &p.CatID })
    byCat := es.NewAggregation().Terms("by_cat", cat, 10)
    byCat.Sub(es.NewAggregation().Avg("avg_price", price))
    aggs := es.NewAggregation()
    aggs.Add(byCat)
    aggResult, _ := repo.Aggregate(ctx, es.NewQuery[Product]().Aggregate(aggs))
    log.Printf("聚合: %v", aggResult)

    // 7) 按 id 读写删
    got, _ := repo.Get(ctx, "p1")
    _ = got
    repo.Update(ctx, "p1", map[string]any{"price": 5799})
    repo.Delete(ctx, "p2")
}
```

完整可运行示例见 [`example/main.go`](./example/main.go)：

```bash
# 需要本地/可达的 ES；未设置 ES_ADDR 时示例只打印 API 用法
ES_ADDR=http://localhost:9200 ES_USER=elastic ES_PASS=xxxx go run ./example
```

---

## 查询构造器

`es.NewQuery[T]()` 返回 `*Query[T]`，条件方法链调用，最终 `BuildQuery()` 产出 `bool` DSL、`BuildBody()` 产出完整 `_search` 请求体。

### 条件方法

| 方法 | 产生的 ES 查询 |
|---|---|
| `Eq(col, v)` | `term` |
| `Ne(col, v)` | `bool.must_not` + `term` |
| `In(col, vals)` | `terms` |
| `NotIn(col, vals)` | `bool.must_not` + `terms` |
| `Match(col, v)` / `Like(col, v)` | `match`（全文分词） |
| `MatchPhrase(col, v)` | `match_phrase` |
| `Wildcard(col, pattern)` | `wildcard`（支持 `*` `?`） |
| `Prefix(col, v)` | `prefix` |
| `Fuzzy(col, v)` | `fuzzy`（fuzziness=AUTO） |
| `Gt/Ge/Lt/Le(col, v)` | `range` |
| `Between(col, lo, hi)` | `range` 闭区间 |
| `Exists(col)` / `Missing(col)` | `exists` |
| `Ids(ids...)` | `ids` |
| `Nested(path, fn)` | `nested` 查询 |

### 组合语义

- 每个条件默认自成一组，组间 **AND**。
- `Or()`：下一个条件与上一个归入同一组、组内 **OR**。
- `If(cond, fn)`：仅当 `cond` 为真才追加条件块（对标 MyBatis-Plus 的三参条件）。
- `Must/Filter/MustNot/Should(fn)`：显式指定条件落到 `bool` 的哪个子句（`Filter` 不计分、可利用缓存，性能更好）。

### 排序 / 分页 / 其它

- `OrderBy(col, asc)` / `OrderByScore(asc)`
- `From(n)` / `Size(n)`（同时提供 `Offset`/`Limit` 别名）
- `SearchAfter(vals...)`：search_after 游标（深翻页，配合 PIT）
- `PIT(id, keepAlive)`：绑定 Point In Time，走一致性快照翻页（与 SearchAfter 配合）
- `TrackTotalHits(true)`：精确统计总命中数
- `Source(cols...)`：`_source` 字段白名单
- `Highlight(cols...)`：开启高亮
- `Aggregate(aggs)`：附加聚合
- `Build()` / `String()`：输出 JSON 字符串，便于调试日志

调试示例：

```go
q := es.NewQuery[Product]().Eq(es.Col[Product](func(p *Product) *string { return &p.Name }), "手机")
log.Println(q.Build()) // 打印 DSL
```

---

## 聚合

`es.NewAggregation()` 创建聚合容器，支持 `Terms` / `DateHistogram` / `Histogram` /
`Avg` / `Sum` / `Min` / `Max` / `ValueCount` / `Cardinality`，并可通过 `Sub()` 嵌套。

```go
cat := es.Col[Product](func(p *Product) *int64 { return &p.CatID })
price := es.Col[Product](func(p *Product) *float64 { return &p.Price })

byCat := es.NewAggregation().Terms("by_cat", cat, 10)
byCat.Sub(es.NewAggregation().Avg("avg_price", price))
byCat.Sub(es.NewAggregation().Max("max_price", price))

aggs := es.NewAggregation()
aggs.Add(byCat)

result, _ := repo.Aggregate(ctx, es.NewQuery[Product]().Aggregate(aggs))
// result 即 ES 返回的 "aggregations" 对象
```

---

## 突破 10000 上限（深翻页 & 超量聚合）

ES 有两个常见的「软上限」：`index.max_result_window`（默认 10000，限制 `from+size`）与
`search.max_buckets`（桶数上限，版本相关）。**它们不是硬天花板，官方提供了不调设置的突破方式**，
本框架把它们封装为类型安全 API，而不是偷偷改大配置（那是反模式，会抬升堆内存与集群风险）。

### 深翻页：SearchAfter + PIT（突破结果数上限）

`from+size` 超过 10000 会报错；正确姿势是 **SearchAfter**（游标）+ **PIT**（一致性快照）：

```go
price := es.Col[Product](func(p *Product) *float64 { return &p.Price })
id    := es.Col[Product](func(p *Product) *string { return &p.ID })

pitID, _ := repo.OpenPIT(ctx, "1m")           // 1) 开一个一致性快照
defer repo.ClosePIT(ctx, pitID)               // 翻完务必关闭

var lastPrice float64
var lastID string
for {
    q := es.NewQuery[Product]().
        OrderBy(price, true).                 // 排序里必须含唯一 tie-breaker
        OrderBy(id, true).                    // 用 id 兜底，避免副本间顺序抖动
        Size(1000).
        PIT(pitID, "")                        // 绑定 PIT
    if lastID != "" {
        q.SearchAfter(lastPrice, lastID)      // 2) 用上一页最后一条的 sort 值续接
    }
    res, _ := repo.Search(ctx, q)
    if len(res.Hits) == 0 {
        break
    }
    last := res.Hits[len(res.Hits)-1]
    lastPrice, lastID = last.Price, last.ID
}
```

> 要点：`SearchAfter(...)` 的字段、顺序、类型必须与 `OrderBy` 严格一致；排序务必包含唯一字段
> （如 `id`）作 tie-breaker，否则副本间顺序可能不一致。绑定 PIT 后 `from` 会被自动忽略。

### 超量聚合：Composite（突破桶数上限）

`search.max_buckets` 超限时，改用 **Composite 聚合**（官方唯一可翻页的聚合），用 `After(...)` 续接：

```go
cat  := es.Col[Product](func(p *Product) *int64 { return &p.CatID })
date := es.Col[Product](func(p *Product) *time.Time { return &p.CreatedAt })

comp := es.NewComposite("by_cat_date").
    Terms("cat", cat, "").                         // 分组源1
    DateHistogram("date", date, "day", "yyyy-MM-dd", "asc"). // 分组源2
    Size(1000)                                     // 每页桶数
comp.Sub(es.NewAgg("avg_price", "avg", map[string]any{"field": "price"}))

aggs := es.NewAggregation().Add(comp.Agg())
result, _ := repo.Aggregate(ctx, es.NewQuery[Product]().Aggregate(aggs))

// 翻页：取上一页最后一个 bucket 的 key 作为 After
lastKey := map[string]any{"cat": "1", "date": "2026-01-01"}
comp2 := es.NewComposite("by_cat_date").
    Terms("cat", cat, "").DateHistogram("date", date, "day", "yyyy-MM-dd", "asc").
    Size(1000).After(lastKey)
```

---

## 索引模板

用组合式索引模板（`_index_template`）让所有匹配 `index_patterns` 的新索引自动套用本模型的
mapping/settings，无需逐个 `CreateIndex`：

```go
body := es.BuildIndexTemplate[Product](es.TemplateOptions{
    Patterns: []string{"products-*"},   // 匹配 products-2026、products-log 等
    Priority: 100,                       // 覆盖同 pattern 的其他模板
    Shards:  1,
    Replicas: 1,
    Version:  1,
    Meta:     map[string]any{"owner": "search-team"},
})
repo.PutIndexTemplate(ctx, "products-template", body)

// 其它操作
repo.GetIndexTemplate(ctx, "products-template")
repo.DeleteIndexTemplate(ctx, "products-template")   // 不存在视为成功
exists, _ := repo.IndexTemplateExists(ctx, "products-template")
```

---

## 泛型仓储 `Repo[T]`

因 Go 不支持泛型方法，`Repo` 用 `NewRepo[T](client)` 函数式绑定（与 `gobreath-orm` 一致）：

```go
repo := es.NewRepo[Product](client)          // 索引名自动推导
repo := es.NewRepo[Product](client, "my_products") // 显式指定索引

repo.CreateIndex(ctx, shards, replicas)      // 按模型自动建索引
repo.Index(ctx, doc)                         // 单条写入（自动取 id 字段作 _id）
repo.BulkIndex(ctx, docs)                    // 批量写入
repo.Get(ctx, id) -> (*Product, error)       // 按 id 读取，缺失返回 es.ErrNotFound
repo.Exists(ctx, id) -> (bool, error)
repo.Update(ctx, id, partial)                // 局部更新（传 map 或结构体）
repo.Delete(ctx, id)                         // 按 id 删除（幂等）
repo.Search(ctx, q) -> (*SearchResult[T], error)
repo.Count(ctx, q) -> (int64, error)
repo.Aggregate(ctx, q) -> (map[string]any, error)

// 突破 10000 上限 & 索引模板
repo.OpenPIT(ctx, keepAlive) -> (pitID string, error)
repo.ClosePIT(ctx, pitID) -> error
repo.PutIndexTemplate(ctx, name, body) -> error
repo.GetIndexTemplate(ctx, name) -> ([]map[string]any, error)
repo.DeleteIndexTemplate(ctx, name) -> error
repo.IndexTemplateExists(ctx, name) -> (bool, error)
```

`SearchResult[T]` 携带 `Total` / `Took` / `Hits []T` / `Raw`，并可用 `Aggregations()` 取聚合结果。

---

## 客户端配置

```go
es.NewClient(
    es.WithAddresses("http://localhost:9200", "http://node2:9200"), // 多节点
    es.WithBasicAuth("elastic", "pwd"),
    es.WithAPIKey("base64key"),
    es.WithCloudID("cloud-id-string"),
)
```

实际使用时**需要 import 对应的 ES 驱动（官方 `go-elasticsearch/v8`）** —— 本框架已把它作为依赖，
正常 `go mod tidy` 即可，无需你手动导入；只需保证运行环境能连上配置的 ES 节点。

---

## 与 gobreath-orm 的关系

`gobreath-es` 与 `gobreath-orm` 共享同一套「lambda + 类型安全 + 泛型仓储」心智模型，区别仅在于：

- `gobreath-orm` 面向关系表，字段名来自 `db` tag，产出 SQL；
- `gobreath-es` 面向 JSON 文档，字段名来自 `json` tag，产出 ES DSL。

同一业务模型若需「关系库 + 搜索引擎」双写，可让实体同时带 `db` 与 `json` tag，分别用两个框架操作。

---

## 目录结构

```
gobreath-es/
├── column.go       # Col[T] 字段选择器（反射取字段名）
├── model.go        # 模型元数据、索引名推导、mapping 自动生成
├── query.go        # 类型安全 ES 查询构造器 Query[T]
├── aggregation.go   # 聚合构造器 Aggregation / Agg
├── client.go       # ES 客户端封装（连接、鉴权、探活）
├── crud.go         # 底层传输（Index/Bulk/Get/Delete/Update/Search/Count/CreateIndex）
├── repo.go         # 泛型仓储 Repo[T]
├── search.go       # SearchResult[T]
├── errors.go       # ErrNotFound 等
├── *_test.go       # 构造器/映射/聚合 DSL 单测（无需真实 ES）
└── example/        # 可运行示例
```
