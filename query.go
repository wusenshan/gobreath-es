package es

import (
	"encoding/json"
)

// leaf 一条 ES 查询子句（已渲染为 map，可直接嵌入 bool 查询）。
type leaf map[string]any

// sortClause 排序项。
type sortClause struct {
	field string
	asc   bool
}

// Query[T] 泛型 ES 查询/检索构造器，对标 MyBatis-Plus 的 LambdaQueryWrapper。
// 字段通过 Col[T](picker) 选择，条件方法链调用，最终 BuildQuery() 产出标准 ES bool DSL。
//
// 分组语义与 gobreath-orm 一致：每个条件默认自成一组（组间 AND）；
// 调用 Or() 后，下一个条件与上一条件归入同一组、组内 OR 连接。
type Query[T any] struct {
	must        [][]leaf
	should      [][]leaf
	mustNot     [][]leaf
	filter      [][]leaf
	cur         *[][]leaf // 当前写入的桶（must/should/mustNot/filter 之一）
	stack       []*[][]leaf
	orMode      bool
	orders      []sortClause
	from        int
	size        int
	trackAll    *bool        // nil 表示不设置 track_total_hits
	source      []string     // _source include；nil 表示返回全部
	aggs        *Aggregation // 聚合（可选）
	highlight   []string     // 高亮字段（可选）
	searchAfter []any        // search_after 游标（深分页）
	pitID       string       // Point In Time id（一致性翻页，与 SearchAfter 配合）
	pitKeep     string       // PIT keep_alive
	knn         *knnQuery[T] // 向量近邻检索（与 ES 自身 query 可同时生效，形成混合召回）
}

// knnQuery 向量近邻检索参数，渲染为 ES 顶层 "knn" 子句。
// field 为目标 dense_vector 字段；vector 为查询向量；k 为返回近邻数；
// numCandidates 为每分片候选数（ES 要求，未设置时取 k*10 且不少于 50）；
// filter 为 in-knn 预过滤（hybrid 预过滤模式：仅在该 filter 命中的文档里找近邻）。
type knnQuery[T any] struct {
	field         ColExpr
	vector        []float32
	k             int
	numCandidates int
	filter        *Query[T]
}

// NewQuery 创建针对类型 T 对应索引的查询构造器。
func NewQuery[T any]() *Query[T] {
	q := &Query[T]{}
	q.cur = &q.must
	q.stack = append(q.stack, &q.must)
	return q
}

// ---- 桶上下文（显式指定条件落到的 bool 子句）----

func (q *Query[T]) push(b *[][]leaf) {
	q.stack = append(q.stack, q.cur)
	q.cur = b
}

func (q *Query[T]) pop() {
	if len(q.stack) > 0 {
		q.cur = q.stack[len(q.stack)-1]
		q.stack = q.stack[:len(q.stack)-1]
	}
}

// Must 把 fn 内的条件放入 bool.must（默认计分过滤上下文）。
func (q *Query[T]) Must(fn func(*Query[T])) *Query[T] {
	q.push(&q.must)
	fn(q)
	q.pop()
	return q
}

// Filter 把 fn 内的条件放入 bool.filter（不计分，可利用缓存，性能更好）。
func (q *Query[T]) Filter(fn func(*Query[T])) *Query[T] {
	q.push(&q.filter)
	fn(q)
	q.pop()
	return q
}

// MustNot 把 fn 内的条件放入 bool.must_not。
func (q *Query[T]) MustNot(fn func(*Query[T])) *Query[T] {
	q.push(&q.mustNot)
	fn(q)
	q.pop()
	return q
}

// Should 把 fn 内的条件放入 bool.should（需配合最小匹配数，这里默认 1）。
func (q *Query[T]) Should(fn func(*Query[T])) *Query[T] {
	q.push(&q.should)
	fn(q)
	q.pop()
	return q
}

// ---- 条件追加 ----

func (q *Query[T]) appendLeaf(l leaf) {
	if q.cur == nil {
		q.cur = &q.must
	}
	if q.orMode && len(*q.cur) > 0 {
		(*q.cur)[len(*q.cur)-1] = append((*q.cur)[len(*q.cur)-1], l)
	} else {
		*q.cur = append(*q.cur, []leaf{l})
	}
	q.orMode = false
}

// Or 使下一个条件与上一个条件在同一组内 OR 连接（MyBatis-Plus .or() 语义）。
func (q *Query[T]) Or() *Query[T] {
	q.orMode = true
	return q
}

// If 当 cond 为 true 时才执行 apply，否则整段条件被忽略。
func (q *Query[T]) If(cond bool, apply func(*Query[T])) *Query[T] {
	if cond {
		apply(q)
	}
	return q
}

// Nested 在嵌套文档路径 path 上构造查询（nested 查询）。
func (q *Query[T]) Nested(path string, fn func(*Query[T])) *Query[T] {
	sub := &Query[T]{}
	sub.cur = &sub.must
	sub.stack = append(sub.stack, &sub.must)
	fn(sub)
	inner := sub.BuildQuery()
	q.appendLeaf(leaf{
		"nested": map[string]any{
			"path":  path,
			"query": inner,
		},
	})
	return q
}

// ---- 向量检索（kNN）----

// Nearest 发起向量近邻检索：在 field（须为 dense_vector 字段）上，以 vec 为查询向量，
// 召回最相似的 k 个文档。底层渲染为 ES 顶层 "knn" 子句。
//
// 与 ORM 的 Nearest/WithinDistance 同名，但 ES 的 kNN 天然支持与 ES 自身检索混合：
//   - 在调用 Nearest 的同时用 Eq/Range/Must 等设置条件，最终请求同时含 query 与 knn，
//     ES 会把「满足 query 的文档」与「k 近邻」合并召回（ES 8.4+ 原生混合检索）。
//   - 也可用 KnnFilter(fn) 设置 in-knn 预过滤（仅在 filter 命中的文档里找近邻），更适合
//     "向量 + 强条件" 的高精度召回场景。
//
// 用法：
//
//	q.Nearest(es.ColOf[Doc]("Embedding"), vec, 10)
//	  .KnnNumCandidates(100)            // 可选：每分片候选数
//	  .KnnFilter(func(q *es.Query[Doc]) { // 可选：in-knn 预过滤（混合召回）
//	      q.Eq(es.ColOf[Doc]("Category"), "book")
//	  })
func (q *Query[T]) Nearest(col ColExpr, vec []float32, k int) *Query[T] {
	q.knn = &knnQuery[T]{field: col, vector: vec, k: k}
	return q
}

// KnnNumCandidates 设置每分片候选数（ES 要求，影响召回质量与性能）。未设置时取 k*10（不少于 50）。
// 须在 Nearest 之后调用。
func (q *Query[T]) KnnNumCandidates(n int) *Query[T] {
	if q.knn == nil {
		q.knn = &knnQuery[T]{}
	}
	q.knn.numCandidates = n
	return q
}

// KnnFilter 设置 in-knn 预过滤（hybrid 预过滤）：仅在 fn 描述的文档集合内检索近邻。
// 这是「向量检索 + ES 条件过滤」的精准形态，区别于顶层 query+knn 的「合并召回」。
// 须在 Nearest 之后调用。
func (q *Query[T]) KnnFilter(fn func(*Query[T])) *Query[T] {
	if q.knn == nil {
		q.knn = &knnQuery[T]{}
	}
	sub := NewQuery[T]()
	fn(sub)
	q.knn.filter = sub
	return q
}

// toMap 渲染 knn 子句。
func (k *knnQuery[T]) toMap() map[string]any {
	nc := k.numCandidates
	if nc <= 0 {
		nc = k.k * 10
		if nc < 50 {
			nc = 50
		}
	}
	m := map[string]any{
		"field":         k.field.name,
		"query_vector":  k.vector,
		"k":             k.k,
		"num_candidates": nc,
	}
	if k.filter != nil {
		m["filter"] = k.filter.BuildQuery()
	}
	return m
}

// ---- 叶子条件 ----

// Eq 精确匹配（term）。
func (q *Query[T]) Eq(col ColExpr, val any) *Query[T] {
	q.appendLeaf(leaf{"term": map[string]any{col.name: val}})
	return q
}

// Ne 不相等（bool.must_not + term）。
func (q *Query[T]) Ne(col ColExpr, val any) *Query[T] {
	q.appendLeaf(leaf{"bool": map[string]any{"must_not": []any{
		map[string]any{"term": map[string]any{col.name: val}},
	}}})
	return q
}

// Terms 多值匹配（terms，任一命中即可）。
func (q *Query[T]) In(col ColExpr, vals []any) *Query[T] {
	q.appendLeaf(leaf{"terms": map[string]any{col.name: vals}})
	return q
}

// NotIn 多值都不匹配（bool.must_not + terms）。
func (q *Query[T]) NotIn(col ColExpr, vals []any) *Query[T] {
	q.appendLeaf(leaf{"bool": map[string]any{"must_not": []any{
		map[string]any{"terms": map[string]any{col.name: vals}},
	}}})
	return q
}

// Match 全文匹配（analyzed，分词后匹配）。
func (q *Query[T]) Match(col ColExpr, val any) *Query[T] {
	q.appendLeaf(leaf{"match": map[string]any{col.name: val}})
	return q
}

// MatchPhrase 短语匹配（分词后顺序、间距完全一致）。
func (q *Query[T]) MatchPhrase(col ColExpr, val any) *Query[T] {
	q.appendLeaf(leaf{"match_phrase": map[string]any{col.name: val}})
	return q
}

// Like 模糊包含（等价于 Match 全文匹配），对标 orm 的 Like 直觉；
// 若需要通配符式匹配请用 Wildcard / Prefix。
func (q *Query[T]) Like(col ColExpr, val string) *Query[T] {
	return q.Match(col, val)
}

// Wildcard 通配符匹配（* 任意，? 单字符）。val 直接作为通配模式，内部不自动加 %。
func (q *Query[T]) Wildcard(col ColExpr, pattern string) *Query[T] {
	q.appendLeaf(leaf{"wildcard": map[string]any{col.name: pattern}})
	return q
}

// Prefix 前缀匹配。
func (q *Query[T]) Prefix(col ColExpr, val string) *Query[T] {
	q.appendLeaf(leaf{"prefix": map[string]any{col.name: val}})
	return q
}

// Fuzzy 模糊匹配（fuzziness 默认 AUTO）。
func (q *Query[T]) Fuzzy(col ColExpr, val string) *Query[T] {
	q.appendLeaf(leaf{"fuzzy": map[string]any{col.name: map[string]any{
		"value":     val,
		"fuzziness": "AUTO",
	}}})
	return q
}

// Range 范围匹配，op 取 "gt"/"gte"/"lt"/"lte"，val 为边界值。
func (q *Query[T]) Range(col ColExpr, op string, val any) *Query[T] {
	q.appendLeaf(leaf{"range": map[string]any{col.name: map[string]any{op: val}}})
	return q
}

// Gt 大于。
func (q *Query[T]) Gt(col ColExpr, val any) *Query[T] { return q.Range(col, "gt", val) }

// Ge 大于等于。
func (q *Query[T]) Ge(col ColExpr, val any) *Query[T] { return q.Range(col, "gte", val) }

// Lt 小于。
func (q *Query[T]) Lt(col ColExpr, val any) *Query[T] { return q.Range(col, "lt", val) }

// Le 小于等于。
func (q *Query[T]) Le(col ColExpr, val any) *Query[T] { return q.Range(col, "lte", val) }

// Between 区间 [lo, hi]（含两端）。
func (q *Query[T]) Between(col ColExpr, lo, hi any) *Query[T] {
	q.appendLeaf(leaf{"range": map[string]any{col.name: map[string]any{
		"gte": lo,
		"lte": hi,
	}}})
	return q
}

// Exists 字段存在且非 null。
func (q *Query[T]) Exists(col ColExpr) *Query[T] {
	q.appendLeaf(leaf{"exists": map[string]any{"field": col.name}})
	return q
}

// Missing 字段不存在或为 null（bool.must_not + exists）。
func (q *Query[T]) Missing(col ColExpr) *Query[T] {
	q.appendLeaf(leaf{"bool": map[string]any{"must_not": []any{
		map[string]any{"exists": map[string]any{"field": col.name}},
	}}})
	return q
}

// Ids 按文档 _id 列表匹配。
func (q *Query[T]) Ids(ids ...string) *Query[T] {
	q.appendLeaf(leaf{"ids": map[string]any{"values": ids}})
	return q
}

// ---- 排序/分页/其它 ----

// OrderBy 按字段排序；asc=true 升序，false 降序。
func (q *Query[T]) OrderBy(col ColExpr, asc bool) *Query[T] {
	q.orders = append(q.orders, sortClause{field: col.name, asc: asc})
	return q
}

// OrderByScore 按相关度评分排序。
func (q *Query[T]) OrderByScore(asc bool) *Query[T] {
	q.orders = append(q.orders, sortClause{field: "_score", asc: asc})
	return q
}

// From 设置 from（跳过的文档数）。
func (q *Query[T]) From(n int) *Query[T] { q.from = n; return q }

// Size 设置 size（返回文档数）。
func (q *Query[T]) Size(n int) *Query[T] { q.size = n; return q }

// Limit 同 Size，命名习惯对齐 orm。
func (q *Query[T]) Limit(n int) *Query[T] { q.size = n; return q }

// Offset 同 From，命名习惯对齐 orm。
func (q *Query[T]) Offset(n int) *Query[T] { q.from = n; return q }

// TrackTotalHits 控制是否精确统计总命中数（true 精确、false 仅上限）。
func (q *Query[T]) TrackTotalHits(on bool) *Query[T] {
	q.trackAll = &on
	return q
}

// Source 限定返回 _source 字段（白名单）。
func (q *Query[T]) Source(cols ...ColExpr) *Query[T] {
	q.source = nil
	for _, c := range cols {
		q.source = append(q.source, c.name)
	}
	return q
}

// Aggregate 附加聚合（可链式调用多次，或一次传入多个）。
func (q *Query[T]) Aggregate(a *Aggregation) *Query[T] {
	q.aggs = a
	return q
}

// Highlight 对指定字段开启高亮。
func (q *Query[T]) Highlight(cols ...ColExpr) *Query[T] {
	q.highlight = nil
	for _, c := range cols {
		q.highlight = append(q.highlight, c.name)
	}
	return q
}

// SearchAfter 设置 search_after 游标，是 ES 官方推荐的"突破 10000 条 from+size 上限"的深分页方式。
// vals 必须与 OrderBy 的排序字段顺序、类型严格对应；为获得稳定分页，排序里务必包含唯一字段（如 id）作为 tie-breaker。
// 注意：使用 SearchAfter 时不应再设置 From（会失效）。
func (q *Query[T]) SearchAfter(vals ...any) *Query[T] {
	q.searchAfter = vals
	return q
}

// PIT 绑定一个已开启的 Point In Time（一致性翻页快照）。与 SearchAfter 配合可安全、无重复的翻全量数据，
// 且不受 index.max_result_window 限制。keepAlive 为空时默认 "1m"。
// 开启/关闭请使用 Client.OpenPIT / Client.ClosePIT（或 Repo 同名方法）。
func (q *Query[T]) PIT(id, keepAlive string) *Query[T] {
	q.pitID = id
	if keepAlive == "" {
		keepAlive = "1m"
	}
	q.pitKeep = keepAlive
	return q
}

// HasPIT 是否绑定了 PIT。检索时据此决定是否改用全局 _search（省略索引名）。
func (q *Query[T]) HasPIT() bool { return q.pitID != "" }

// ---- 渲染 ----

// BuildQuery 产出 bool 查询 DSL（map）。
func (q *Query[T]) BuildQuery() map[string]any {
	boolQ := map[string]any{}
	add := func(key string, groups [][]leaf) {
		if len(groups) == 0 {
			return
		}
		clauses := renderGroups(groups)
		boolQ[key] = clauses
	}
	add("must", q.must)
	add("filter", q.filter)
	add("must_not", q.mustNot)
	if len(q.should) > 0 {
		boolQ["should"] = renderGroups(q.should)
		boolQ["minimum_should_match"] = 1
	}
	if len(boolQ) == 0 {
		return map[string]any{"match_all": map[string]any{}}
	}
	return map[string]any{"bool": boolQ}
}

// renderGroups 把条件组渲染为子句数组：单条组直接取叶子，多条组包成 should + minimum_should_match=1。
func renderGroups(groups [][]leaf) []any {
	var clauses []any
	for _, g := range groups {
		if len(g) == 1 {
			clauses = append(clauses, g[0])
			continue
		}
		sh := make([]any, len(g))
		for i, l := range g {
			sh[i] = l
		}
		clauses = append(clauses, map[string]any{
			"bool": map[string]any{
				"should":               sh,
				"minimum_should_match": 1,
			},
		})
	}
	return clauses
}

// isEmptyMatchAll 判断查询是否为空的 match_all（即未设置任何条件）。
func isEmptyMatchAll(q map[string]any) bool {
	if m, ok := q["match_all"]; ok {
		if mm, ok := m.(map[string]any); ok && len(mm) == 0 {
			return true
		}
	}
	return false
}

// BuildBody 产出完整的 _search 请求体（query + sort + from + size + _source + aggs + track_total_hits + highlight）。
func (q *Query[T]) BuildBody() map[string]any {
	body := map[string]any{}
	qb := q.BuildQuery()
	// 当只做向量检索（无其它查询条件）时，不输出无意义的 match_all，保持请求干净；
	// 一旦同时设置了 bool 条件，query 与 knn 并存即形成 ES 原生混合召回。
	if q.knn == nil || !isEmptyMatchAll(qb) {
		body["query"] = qb
	}

	if len(q.orders) > 0 {
		sorts := make([]any, 0, len(q.orders))
		for _, o := range q.orders {
			order := "desc"
			if o.asc {
				order = "asc"
			}
			sorts = append(sorts, map[string]any{o.field: order})
		}
		body["sort"] = sorts
	}
	// PIT 与 from 互斥（ES 不允许同时使用），绑定 PIT 时丢弃 from。
	if q.from > 0 && q.pitID == "" {
		body["from"] = q.from
	}
	if q.size > 0 {
		body["size"] = q.size
	}
	if q.pitID != "" {
		body["pit"] = map[string]any{"id": q.pitID, "keep_alive": q.pitKeep}
	}
	if len(q.searchAfter) > 0 {
		body["search_after"] = q.searchAfter
	}
	if q.source != nil {
		body["_source"] = q.source
	}
	if q.aggs != nil {
		body["aggs"] = q.aggs.Build()
	}
	if q.trackAll != nil {
		body["track_total_hits"] = *q.trackAll
	}
	if len(q.highlight) > 0 {
		fields := map[string]any{}
		for _, f := range q.highlight {
			fields[f] = map[string]any{}
		}
		body["highlight"] = map[string]any{"fields": fields}
	}
	// 向量近邻检索：顶层 knn 子句。与上面的 query 同时存在即形成 ES 原生混合召回。
	if q.knn != nil {
		body["knn"] = q.knn.toMap()
	}
	return body
}

// Build 返回 JSON 化的查询体（便于调试/日志）。
func (q *Query[T]) Build() string {
	b, _ := json.Marshal(q.BuildBody())
	return string(b)
}

func (q *Query[T]) String() string { return q.Build() }
