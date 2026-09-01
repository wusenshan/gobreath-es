# gobreath-es

中文版本： [README.zh-CN.md](./README.zh-CN.md)

[![Go Reference](https://pkg.go.dev/badge/github.com/wusenshan/gobreath-es.svg)](https://pkg.go.dev/github.com/wusenshan/gobreath-es)
[![Build](https://github.com/wusenshan/gobreath-es/actions/workflows/ci.yml/badge.svg)](https://github.com/wusenshan/gobreath-es/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/go-1.23%2B-00ADD8)](https://go.dev)

A Go framework for Elasticsearch that keeps the API close to Elasticsearch itself while still giving you a safe and ergonomic Go experience.

The design goal is simple:

- zero raw string field names in query code
- model-driven index mapping
- typed, chainable query builders
- official ES naming for the primary API
- thin compatibility aliases for ORM-style callers
- strong support for hybrid vector search and AI/RAG use cases

## Why gobreath-es

The project follows the same design philosophy as `gobreath-orm`:

- `Col[T]` picks a field from a Go struct instead of hardcoding a string
- `Query[T]` composes ES bool queries in Go
- `Repo[T]` binds a model to an index and exposes ES-native CRUD APIs
- mapping is inferred from tags and model metadata
- vector search is first-class and can be combined with normal search filters

This means you can work with Elasticsearch without writing brittle JSON query strings everywhere.

## Core API naming

The primary API intentionally follows Elasticsearch semantics instead of forcing SQL/ORM naming onto ES.

| ES-native method | Meaning |
| --- | --- |
| `Index` | write or upsert a document |
| `Get` | fetch by document id |
| `Search` | execute search |
| `Count` | count hits |
| `Update` | partial update by id |
| `Delete` | delete by id |
| `BulkIndex` / `IndexMany` | bulk write |
| `CreateIndex` | create index / mapping |
| `PutMapping` | create/update mapping |
| `Nearest` | kNN vector search |

Compatibility aliases are also kept for callers coming from ORM or SQL-oriented habits:

| Compatibility alias | Native equivalent |
| --- | --- |
| `Save` | `Index` |
| `Insert` | `Index` |
| `GetByID` | `Get` |
| `DeleteByID` | `Delete` |
| `UpdateByID` | `Update` |
| `IndexOne` | `Index` |
| `IndexMany` | `BulkIndex` |

This keeps the main API ES-native while preserving a smooth migration path.

## Installation

```bash
go get github.com/wusenshan/gobreath-es
```

## Quick start

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

## Model metadata

Fields are mapped using Go tags and ES conventions.

```go
type Product struct {
    ID      string  `json:"id" es:"id"`
    Title   string  `json:"title"`
    Price   float64 `json:"price"`
    Tags    []string `json:"tags"`
    Embeds  []float32 `json:"embeds" es:"vector(1536)"`
    Source  string  `json:"source" es:"type:keyword"`
}
```

Supported tags:

- `json:"name"` maps to the document field name
- `es:"id"` marks the document `_id`
- `es:"-"` ignores the field
- `es:"type:keyword"` overrides the inferred mapping
- `es:"vector(1536)"` creates a dense_vector field for kNN search

If you do not provide an explicit index name, the framework derives a plural snake_case index name, such as:

- `Product` -> `products`
- `UserProfile` -> `user_profiles`

## Query builder

The main query interface is `Query[T]`.

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

Common conditions include:

- `Eq`, `Ne`, `In`
- `Gt`, `Gte`, `Lt`, `Lte`
- `Like`, `Exists`, `NotNull`, `Null`
- `Or`, `If`, `Nested`, `Filter`, `Must`, `MustNot`, `Should`

## Vector search

Vector search is a first-class feature.

```go
vec := []float32{0.12, 0.33, 0.99}

res, err := repo.Search(ctx, es.NewQuery[Product]().
    Nearest(es.Col[Product](func(p *Product) *[]float32 { return &p.Embeds }), vec, 10).
    KnnNumCandidates(50).
    Filter(func(q *es.Query[Product]) {
        q.Eq(es.Col[Product](func(p *Product) *string { return &p.Category }), "electronics")
    }))
```

This is especially useful for AI / RAG use cases such as semantic search and hybrid recall.

## Aggregations

```go
agg := es.NewAggregation().
    Terms("by_category", es.Col[Product](func(p *Product) *string { return &p.Category }), 10).
    Sub(es.NewAggregation().Avg("avg_price", es.Col[Product](func(p *Product) *float64 { return &p.Price })))

q := es.NewQuery[Product]().Aggregate(agg)

result, err := repo.Aggregate(ctx, q)
```

## Write safety & escape hatch

### Optimistic concurrency control (OCC)

Elasticsearch supports conflict detection via `_seq_no` / `_primary_term`. Read the
current version, modify the document, then write it back with `es.IfSeqNoPrimaryTerm`
so the write only succeeds if nobody changed the document in between (otherwise ES
returns a 409 conflict).

```go
doc, meta, err := repo.GetWithMeta(ctx, "p1") // meta.SeqNo / meta.PrimaryTerm
if err != nil {
    log.Fatal(err)
}
doc.Price = 999

// only succeeds if the document is still at this version
if err := repo.Index(ctx, doc, es.IfSeqNoPrimaryTerm(meta.SeqNo, meta.PrimaryTerm)); err != nil {
    log.Fatal(err)
}
```

`SearchResult` also exposes per-hit `SeqNos` / `PrimaryTerms` (and `HitMeta(i)`), so
you can apply OCC after a bulk search too. `Update` and `Delete` accept the same option.

### Upsert

Insert a document if it does not exist, otherwise merge it as a partial update.

```go
// single doc (needs an id field)
repo.Upsert(ctx, Product{ID: "p1", Name: "Phone"})

// bulk upsert
repo.BulkUpsert(ctx, []Product{{ID: "p1"}, {ID: "p2"}})
```

### Raw search escape hatch

When the query builder cannot express what you need (scripted sorting, scripted
metrics, exotic aggregations), send a raw DSL map and get the full ES response back.

```go
raw, err := repo.SearchRaw(ctx, map[string]any{
    "query": map[string]any{"match_all": map[string]any{}},
    "aggs":  map[string]any{"max_price": map[string]any{"max": map[string]any{"field": "price"}}},
})
```

## Compatibility with ORM-style naming

If you come from SQL or MyBatis-Plus habits, the convenience aliases are still available.

```go
if err := repo.Insert(ctx, doc); err != nil {
    log.Fatal(err)
}

if err := repo.UpdateByID(ctx, "id-123", map[string]any{"price": 999}); err != nil {
    log.Fatal(err)
}

obj, err := repo.GetByID(ctx, "id-123")
```

The important distinction is:

- Native ES naming is the primary design
- ORM-style wrappers are compatibility shims, not the main API

## Why not make SQL the main API?

Elasticsearch SQL is useful for migration and ad-hoc analysis, but it is not a perfect replacement for native ES query semantics.

This project keeps the mainstream API in Go-native ES terms because it is:

- more type-safe
- easier to compose in Go
- closer to actual Elasticsearch semantics
- more natural for vector / nested / kNN / filter-heavy workloads

If needed later, a SQL adapter can still be added as a thin optional layer without changing the primary design.

## Project status

The framework already includes:

- typed field selection
- query builder
- repo abstraction
- mapping generation
- index management
- aggregation builders
- hybrid vector search
- optimistic concurrency control (OCC)
- upsert (single & bulk)
- raw search escape hatch
- logging and request tracing

The design direction is to keep the canonical API close to Elasticsearch while retaining lightweight compatibility helpers for ORM-oriented users.

## License

MIT
