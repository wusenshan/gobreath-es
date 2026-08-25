package es

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
)

// Repo[T] 绑定到类型 T 对应索引的泛型仓储，把类型安全的查询构造器与 CRUD 组合起来。
// 因 Go 不支持泛型方法，故用 NewRepo[T](client) 函数式绑定（同 gobreath-orm 的 Repo 设计）。
type Repo[T any] struct {
	cli   *Client
	index string
	meta  *modelMeta
}

// NewRepo 为类型 T 创建仓储；可选传入显式索引名（不传则按模型推导）。
func NewRepo[T any](cli *Client, indexOverride ...string) *Repo[T] {
	meta := getMeta[T]()
	idx := meta.index
	if len(indexOverride) > 0 && indexOverride[0] != "" {
		idx = indexOverride[0]
	}
	return &Repo[T]{cli: cli, index: idx, meta: meta}
}

// IndexName 返回该仓储绑定的索引名。
func (r *Repo[T]) IndexName() string { return r.index }

// extractID 从文档中提取 id 字段的字符串值（用于文档 _id）。
func extractID(meta *modelMeta, doc any) string {
	if meta.idField == nil {
		return ""
	}
	rv := reflect.ValueOf(doc)
	for rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}
	fv := rv.FieldByName(meta.idField.goName)
	if !fv.IsValid() {
		return ""
	}
	return toString(fv)
}

// setID 把字符串 id 回填到文档的 id 字段。
func setID[T any](meta *modelMeta, doc *T, id string) {
	if meta.idField == nil {
		return
	}
	rv := reflect.ValueOf(doc).Elem()
	fv := rv.FieldByName(meta.idField.goName)
	if !fv.IsValid() || !fv.CanSet() {
		return
	}
	setStringValue(fv, id)
}

func toString(v reflect.Value) string {
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return ""
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.String:
		return v.String()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(v.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(v.Uint(), 10)
	default:
		return fmt.Sprintf("%v", v.Interface())
	}
}

func setStringValue(fv reflect.Value, s string) {
	switch fv.Kind() {
	case reflect.String:
		fv.SetString(s)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			fv.SetInt(n)
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if n, err := strconv.ParseUint(s, 10, 64); err == nil {
			fv.SetUint(n)
		}
	}
}

// ---- 仓储方法 ----

// Index 写入单个文档（自动取 id 字段作为 _id，无 id 则让 ES 自动生成）。
func (r *Repo[T]) Index(ctx context.Context, doc T) error {
	return r.cli.IndexDoc(ctx, r.index, extractID(r.meta, doc), doc)
}

// BulkIndex 批量写入文档。
func (r *Repo[T]) BulkIndex(ctx context.Context, docs []T) error {
	if len(docs) == 0 {
		return nil
	}
	anyDocs := make([]any, len(docs))
	ids := make([]string, len(docs))
	for i, d := range docs {
		anyDocs[i] = d
		ids[i] = extractID(r.meta, d)
	}
	return r.cli.BulkIndexDocs(ctx, r.index, anyDocs, ids)
}

// Get 按 id 读取文档（自动回填 id 字段）。不存在返回 ErrNotFound。
func (r *Repo[T]) Get(ctx context.Context, id string) (*T, error) {
	var t T
	if err := r.cli.GetDoc(ctx, r.index, id, &t); err != nil {
		return nil, err
	}
	setID(r.meta, &t, id)
	return &t, nil
}

// Exists 判断文档是否存在。
func (r *Repo[T]) Exists(ctx context.Context, id string) (bool, error) {
	return r.cli.DocExists(ctx, r.index, id)
}

// Delete 按 id 删除文档（不存在视为成功）。
func (r *Repo[T]) Delete(ctx context.Context, id string) error {
	return r.cli.DeleteDoc(ctx, r.index, id)
}

// Update 按 id 局部更新（partial 可为 map 或结构体，仅其非空字段参与更新）。
func (r *Repo[T]) Update(ctx context.Context, id string, partial any) error {
	return r.cli.UpdateDoc(ctx, r.index, id, partial)
}

// Search 执行检索，返回泛型结果（含文档列表与聚合）。
func (r *Repo[T]) Search(ctx context.Context, q *Query[T]) (*SearchResult[T], error) {
	raw, hits, total, took, err := r.cli.searchRaw(ctx, r.index, q.BuildBody(), q.HasPIT())
	if err != nil {
		return nil, err
	}
	docs, err := unmarshalHits[T](hits)
	if err != nil {
		return nil, err
	}
	return &SearchResult[T]{Total: total, Took: took, Hits: docs, Raw: raw}, nil
}

// Count 统计满足查询条件的文档数。
func (r *Repo[T]) Count(ctx context.Context, q *Query[T]) (int64, error) {
	return r.cli.CountRaw(ctx, r.index, q.BuildQuery())
}

// Aggregate 执行带聚合的检索，仅返回聚合结果（不返回文档）。
func (r *Repo[T]) Aggregate(ctx context.Context, q *Query[T]) (map[string]any, error) {
	body := map[string]any{"query": q.BuildQuery()}
	if q.aggs != nil {
		body["aggs"] = q.aggs.Build()
	}
	body["size"] = 0
	raw, _, _, _, err := r.cli.searchRaw(ctx, r.index, body, q.HasPIT())
	if err != nil {
		return nil, err
	}
	if agg, ok := raw["aggregations"].(map[string]any); ok {
		return agg, nil
	}
	return map[string]any{}, nil
}

// CreateIndex 根据模型 T 自动建索引（含 mapping 与可选 settings）。
func (r *Repo[T]) CreateIndex(ctx context.Context, shards, replicas int) error {
	return r.cli.CreateIndexRaw(ctx, r.index, BuildMapping[T](shards, replicas))
}

// ---- Point In Time & 索引模板 ----

// OpenPIT 开启本仓储索引的 Point In Time（一致性翻页快照）。
func (r *Repo[T]) OpenPIT(ctx context.Context, keepAlive string) (string, error) {
	return r.cli.OpenPIT(ctx, r.index, keepAlive)
}

// ClosePIT 关闭一个 Point In Time，释放集群资源。
func (r *Repo[T]) ClosePIT(ctx context.Context, id string) error {
	return r.cli.ClosePIT(ctx, id)
}

// PutIndexTemplate 创建/更新组合式索引模板（body 由 BuildIndexTemplate[T] 产出）。
func (r *Repo[T]) PutIndexTemplate(ctx context.Context, name string, body map[string]any) error {
	return r.cli.PutIndexTemplate(ctx, name, body)
}

// GetIndexTemplate 读取组合式索引模板。
func (r *Repo[T]) GetIndexTemplate(ctx context.Context, name string) ([]map[string]any, error) {
	return r.cli.GetIndexTemplate(ctx, name)
}

// DeleteIndexTemplate 删除组合式索引模板（不存在视为成功）。
func (r *Repo[T]) DeleteIndexTemplate(ctx context.Context, name string) error {
	return r.cli.DeleteIndexTemplate(ctx, name)
}

// IndexTemplateExists 判断组合式索引模板是否存在。
func (r *Repo[T]) IndexTemplateExists(ctx context.Context, name string) (bool, error) {
	return r.cli.IndexTemplateExists(ctx, name)
}
