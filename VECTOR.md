# 向量检索（gobreath-es · AI / RAG 原生支持）

`gobreath-es` 内置 **向量近邻检索（kNN）**：用 `es:"vector(N)"` 声明向量字段，`Nearest` 做 k 近邻，
`CreateIndex` 自动生成 `dense_vector` mapping。它和 [`gobreath-orm`](https://github.com/wusenshan/gobreath-orm)
的 `Nearest/WithinDistance` 同源，但 **ES 的 kNN 天然支持与 ES 自身检索混合召回**——一个请求里
`knn` 与 `query` 同时生效。这是纯向量库（pgvector / Milvus）给不了的：你既能做「语义近邻」，
又能用 ES 的 `term/range/bool` 做「关键词 + 过滤」精确约束，二者在检索阶段就合并。

> 依赖 **Elasticsearch 8.4+**（顶层 `knn` 参数）。

---

## 0. 这篇文档讲什么

- **ES 专属**：`dense_vector` 字段类型、`similarity` / `element_type`、mapping 生成、kNN 查询与**混合召回**（§2–§4）。
- **通用部分（向量概念、四种距离度量的数学直觉、如何接入 OpenAI / Ollama 等 embedding 模型）**：
  `gobreath-orm` 的 [VECTOR.md](https://github.com/wusenshan/gobreath-orm/blob/main/VECTOR.md) 讲得更系统、更完整——
  因为「文本 → 向量」「归一化」「度量选型」与具体存储无关。本篇在 §5 直接链接过去，避免重复造轮子。

---

## 1. 它解决什么问题（一句话）

把文本 / 图片 / 任意对象通过 embedding 模型变成一串浮点数（向量），存进 ES，再用「查询向量」找回
**语义最相近**的文档。典型场景：知识库问答（RAG）、语义搜索、相似推荐、文本去重。

> 想看「效果 / 用途 / 为什么是向量而不是关键词」的详细展开，见
> [orm VECTOR.md §1 它解决什么问题](https://github.com/wusenshan/gobreath-orm/blob/main/VECTOR.md#1-它解决什么问题效果--用途)。

---

## 2. ES 的向量字段类型：`dense_vector`

ES 用 **`dense_vector`** 类型存定长浮点向量。一个字段存一个向量（如一条文档的 embedding）。

### 2.1 维度 `dims`（必填）

ES 8.x 要求 `dense_vector` **必须声明维度** `dims`（如 1536、768、384），且所有文档写入的向量长度必须一致，
否则写入报错。维度由你的 embedding 模型决定（OpenAI `text-embedding-3-small`=1536、`text-embedding-3-large`=3072、
`bge-m3`=1024、Ollama `nomic-embed-text`=768 等）。

### 2.2 相似度 `similarity`（kNN 必需）

做 **近似 kNN 检索**时，`dense_vector` 必须声明 `similarity`，否则运行期报错：

> `Field [embedding] of type [dense_vector] doesn't support kNN search because it doesn't have a [similarity]`

可选值（与 ORM 的度量一一对应）：

| similarity | 含义 | 对应 orm 度量 | 注意 |
|---|---|---|---|
| `cosine` | 余弦相似度 | `Cosine` | **默认**；向量无需归一化 |
| `l2_norm` | 欧氏距离 | `L2` | 值越小越近 |
| `dot_product` | 点积 | `InnerProduct` | **向量必须归一化到单位长度** |
| `max_inner_product` | 最大内积 | `InnerProduct`（异名） | 向量必须归一化；适合已归一化的内积场景 |

> `gobreath-es` 默认给向量字段加 `similarity: cosine` + `index: true`。要换度量，用 `es:"sim:l2"` 等
> （见 §3）。

### 2.3 元素类型 `element_type`（存储体积）

- `float`（默认）：每个分量 4 字节。
- `byte`：每个分量 1 字节（int8，值域 -128~127），体积约为 float 的 **1/4**，适合大模型降精度无损检索。
- `bit`：1 比特/分量，用于二值向量。

本框架当前按 `float` 生成 mapping（最通用、精度最高）；如要 `byte`/`bit`，可在建索引后用原生
mapping PUT 覆盖，或等后续 tag 支持 `es:"type:dense_vector;element_type:byte"`。

### 2.4 索引：HNSW（百万级近邻才需要调）

`similarity` + `index: true` 后，ES 自动为字段建 **HNSW** 近似最近邻索引（`m` 默认 16、`ef_construction`
默认 100）。文档量小（几千~几万）时暴力精确检索也很快，不用额外管；上百万级再按业务调 `m` / `ef_construction`
（通过原生 mapping / 模板覆盖即可，框架默认值够用）。

---

## 3. 存储：声明向量字段 + 自动建索引

### 3.1 模型声明

```go
type Product struct {
    ID        string    `json:"id" es:"id"`
    Name      string    `json:"name"`
    CatID     int64     `json:"cat_id"`
    Embedding []float32 `json:"embedding" es:"vector(1536)"` // 1536 维；默认 cosine 相似度
    // 换度量：
    //   es:"vector(1536);sim:l2"          欧氏距离
    //   es:"vector(1536);sim:dot"         点积（需归一化向量）
    //   es:"vector(1536);sim:max_inner"   最大内积（需归一化向量）
}
```

- `[]float32` 是向量字段的标准 Go 类型（与 orm 一致），直接写入即可，无需手动序列化。
- 维度务必对齐你的 embedding 模型，否则写入 / 检索报错。

### 3.2 自动建索引（mapping 自动带 `dense_vector`）

```go
repo := es.NewRepo[Product](client)
if err := repo.CreateIndex(ctx, 1, 1); err != nil { /* ... */ }
```

`CreateIndex` 生成的 `embedding` mapping 形如：

```json
{
  "embedding": {
    "type": "dense_vector",
    "dims": 1536,
    "index": true,
    "similarity": "cosine"
  }
}
```

组合式索引模板（`BuildIndexTemplate`）同样会带上 `dense_vector` 定义，匹配 `index_patterns` 的新索引自动套用。

### 3.3 写入向量

```go
vec := embed("iPhone 15 手机") // []float32，来自 embedding 模型（见 §5）
repo.Index(ctx, Product{ID: "p1", Name: "iPhone 15", CatID: 1, Embedding: vec})
// 批量同理：repo.BulkIndex(ctx, []Product{...})
```

---

## 4. 查询：纯 kNN、混合召回、in-knn 预过滤

`Query[T]` 用 `Nearest(col, vec, k)` 挂向量近邻；`BuildBody()` 输出 ES 顶层 `knn` 子句。

### 4.1 纯向量近邻

```go
emb := es.ColOf[Product]("Embedding") // 短写法，等价于 Col 闭包
qvec := embed("想要一台拍照好的手机")

res, _ := repo.Search(ctx, es.NewQuery[Product]().Nearest(emb, qvec, 10)) // 召回最相似的 10 条
for i, p := range res.Hits {
    fmt.Printf("%s  score=%.4f\n", p.Name, res.Scores[i]) // Scores 与 Hits 对齐，是 _score（相似度）
}
```

`res.Scores[i]` 是与 `res.Hits[i]` 对应的相似度得分（`_score`）。`cosine` 下分数越接近 1 越相似。

### 4.2 混合召回：向量 + ES 自身条件（ES 的杀手锏）

**同时设置 `Eq/Range/Must` 等条件与 `Nearest`**，最终请求**同时含 `query` 与 `knn`**，
ES 把「满足条件」与「k 近邻」**合并召回**（默认线性加权合并两路 `_score`）。这是 pgvector / Milvus
做不到的——语义检索与关键词 / 过滤在同一次检索里协同：

```go
res, _ := repo.Search(ctx, es.NewQuery[Product]().
    Eq(es.ColOf[Product]("CatID"), int64(1)). // 只在 cat_id=1 类目里
    Nearest(emb, qvec, 10))                   // 找向量近邻
```

生成的 DSL（节选）：

```json
{
  "query": { "term": { "cat_id": 1 } },
  "knn":  { "field": "embedding", "query_vector": [/* qvec */], "k": 10, "num_candidates": 100 }
}
```

### 4.3 in-knn 预过滤（hybrid 的精准形态）

`KnnFilter` 把条件作为 **in-knn 预过滤**：仅在 filter 命中的文档里检索近邻（候选集先被条件缩小，
精度更高、候选更少），区别于 §4.2 的「合并召回」：

```go
res, _ := repo.Search(ctx, es.NewQuery[Product]().
    Nearest(emb, qvec, 10).
    KnnFilter(func(q *es.Query[Product]) {
        q.Eq(es.ColOf[Product]("CatID"), int64(1))
    }))
```

### 4.4 候选数 `num_candidates`

每分片候选数，影响召回质量与性能，ES 要求 `num_candidates >= k`。未设置时本框架取 `max(k*10, 50)`，
可用 `KnnNumCandidates(n)` 显式指定：

```go
es.NewQuery[Product]().Nearest(emb, qvec, 10).KnnNumCandidates(200)
```

---

## 5. 接入 AI 向量模型（把文本变成 `[]float32`）

「文本 → 向量」这一步与用什么存储无关，`gobreath-orm` 的
[VECTOR.md §5 接入 AI 向量模型](https://github.com/wusenshan/gobreath-orm/blob/main/VECTOR.md#5-接入-ai-向量模型简单布置)
把**选模型、OpenAI 兼容接口、本地 Ollama、端到端建库→入库→检索**都写得很全，直接照抄即可——
生产出的 `[]float32` 直接喂给 `gobreath-es` 的向量字段。下面只给一个 ES 视角的端到端骨架：

```go
// embed 返回 []float32；实现见 orm VECTOR.md §5.2（OpenAI 兼容）/ §5.3（本地 Ollama）。
// 关键：embedding 维度必须等于 es:"vector(N)" 里的 N。
func embed(text string) []float32 { /* ... 调 /v1/embeddings ... */ }

func main() {
    ctx := context.Background()
    client, _ := es.NewClient(es.WithAddresses("http://localhost:9200"))
    repo := es.NewRepo[Product](client)
    repo.CreateIndex(ctx, 1, 1) // embedding 自动成为 dense_vector(cosine)

    // 入库：把商品标题/描述变成向量
    repo.BulkIndex(ctx, []Product{
        {ID: "p1", Name: "iPhone 15", CatID: 1, Embedding: embed("iPhone 15 智能手机")},
        {ID: "p2", Name: "MacBook Pro", CatID: 1, Embedding: embed("MacBook Pro 笔记本电脑")},
    })

    // 检索：用语义向量召回 + ES 条件约束
    res, _ := repo.Search(ctx, es.NewQuery[Product]().
        Eq(es.ColOf[Product]("CatID"), int64(1)).
        Nearest(es.ColOf[Product]("Embedding"), embed("想要一台苹果手机"), 10))
    for i, p := range res.Hits {
        fmt.Printf("%.4f  %s\n", res.Scores[i], p.Name) // 相似度 + 文档
    }
}
```

> 中文场景推荐国内可达的 embedding（详见 orm VECTOR.md）：阿里云百炼 `text-embedding-v3`（默认 1024 维）、
> 本地 Ollama `nomic-embed-text`（768 维）等。维度定好后把 `es:"vector(N)"` 的 `N` 对齐即可。

---

## 6. 限制与注意

- **ES 版本**：顶层 `knn` 需要 **ES 8.4+**；更早版本只能用 `script_score` 精确检索（本框架不直接封装）。
- **维度一致**：同一索引内 `dense_vector` 维度必须固定，写入向量长度要等于 `dims`，否则报错。
- **归一化**：用 `dot_product` / `max_inner_product` 时，**向量必须归一化到单位长度**，否则分数失真。
  `cosine` 不用归一化（ES 内部处理）。
- **混合召回的评分**：`knn` 与 `query` 的 `_score` 由 ES 线性合并；若两路分数量级差异大，必要时用
  `KnnFilter`（§4.3）先做硬过滤，而不是合并。
- **规模**：百万级向量务必保留 `index: true`（默认开），HNSW 索引让近邻检索从 O(N) 降到近似对数级；
  调参（`m` / `ef_construction`）通过原生 mapping / 模板覆盖。
- ** Scores 含义**：`res.Scores` 是 ES 原始 `_score`；`cosine` 类越接近 1 越相似，`l2_norm` 类越接近 0 越近。

---

## 7. 相关链接

- [`gobreath-orm` VECTOR.md](https://github.com/wusenshan/gobreath-orm/blob/main/VECTOR.md) —
  **向量概念、四种距离度量数学直觉、接入 OpenAI / Ollama embedding 模型的完整端到端布置**（更全，优先读）。
- [Elasticsearch 官方 `dense_vector` 文档](https://www.elastic.co/guide/en/elasticsearch/reference/current/dense-vector.html)
- [Elasticsearch 官方 kNN 搜索文档](https://www.elastic.co/guide/en/elasticsearch/reference/current/knn-search.html)
- 本仓库 `example/main.go`：含向量检索的离线 DSL 演示与真实 ES 检索步骤。
