package es

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	esapi "github.com/elastic/go-elasticsearch/v8/esapi"
)

// ---- 底层传输（非泛型，索引名 + 通用参数）----

// IndexDoc 写入单个文档。id 为空时由 ES 自动生成。
// 可选传入 IfSeqNoPrimaryTerm 启用乐观并发控制：仅当文档当前的
// _seq_no/_primary_term 匹配时才写入，否则返回错误（典型为 409 冲突）。
func (c *Client) IndexDoc(ctx context.Context, index string, id string, doc any, opts ...WriteOption) error {
	body, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("gobreath-es: 序列化文档失败: %w", err)
	}
	path := index + "/_doc"
	if id != "" {
		path = index + "/_doc/" + id
	}
	reqOpts := []func(*esapi.IndexRequest){c.Client.Index.WithContext(ctx)}
	if id != "" {
		reqOpts = append(reqOpts, c.Client.Index.WithDocumentID(id))
	}
	reqOpts = c.applyIndexCAS(reqOpts, opts)
	res, err := c.doLog("POST", path, body, func() (*esapi.Response, error) {
		return c.Client.Index(index, bytes.NewReader(body), reqOpts...)
	})
	m, err := decodeResponse(res, err)
	if err != nil {
		return err
	}
	if shards, ok := m["_shards"].(map[string]any); ok {
		if failed, _ := shards["failed"].(float64); failed > 0 {
			return fmt.Errorf("gobreath-es: 写入文档部分失败 shards=%v", shards)
		}
	}
	return nil
}

// applyIndexCAS 把 WriteOption 里的乐观并发控制附加到 Index 请求选项上。
func (c *Client) applyIndexCAS(base []func(*esapi.IndexRequest), opts []WriteOption) []func(*esapi.IndexRequest) {
	o := &writeOpts{}
	for _, fn := range opts {
		fn(o)
	}
	if o.hasCAS {
		base = append(base,
			c.Client.Index.WithIfSeqNo(int(o.ifSeqNo)),
			c.Client.Index.WithIfPrimaryTerm(int(o.ifPrimaryTerm)),
		)
	}
	return base
}

// applyUpdateCAS 把 WriteOption 里的乐观并发控制附加到 Update 请求选项上。
func (c *Client) applyUpdateCAS(base []func(*esapi.UpdateRequest), opts []WriteOption) []func(*esapi.UpdateRequest) {
	o := &writeOpts{}
	for _, fn := range opts {
		fn(o)
	}
	if o.hasCAS {
		base = append(base,
			c.Client.Update.WithIfSeqNo(int(o.ifSeqNo)),
			c.Client.Update.WithIfPrimaryTerm(int(o.ifPrimaryTerm)),
		)
	}
	return base
}

// applyDeleteCAS 把 WriteOption 里的乐观并发控制附加到 Delete 请求选项上。
func (c *Client) applyDeleteCAS(base []func(*esapi.DeleteRequest), opts []WriteOption) []func(*esapi.DeleteRequest) {
	o := &writeOpts{}
	for _, fn := range opts {
		fn(o)
	}
	if o.hasCAS {
		base = append(base,
			c.Client.Delete.WithIfSeqNo(int(o.ifSeqNo)),
			c.Client.Delete.WithIfPrimaryTerm(int(o.ifPrimaryTerm)),
		)
	}
	return base
}

// PutDoc 上传/覆盖单个文档，等价于 ES 的 PUT /_doc/{id} 语义。
func (c *Client) PutDoc(ctx context.Context, index string, id string, doc any) error {
	return c.IndexDoc(ctx, index, id, doc)
}

// CreateDoc 仅在文档不存在时创建，等价于 ES 的 POST /_create/{id}。
func (c *Client) CreateDoc(ctx context.Context, index string, id string, doc any) error {
	if id == "" {
		return fmt.Errorf("gobreath-es: create requires a document id")
	}
	body, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("gobreath-es: 序列化文档失败: %w", err)
	}
	res, err := c.doLog("POST", index+"/_create/"+id, body, func() (*esapi.Response, error) {
		return c.Client.Create(index, id, bytes.NewReader(body), c.Client.Create.WithContext(ctx))
	})
	_, err = decodeResponse(res, err)
	return err
}

// BulkIndexDocs 批量写入文档（NDJSON _bulk）。ids 与 docs 一一对应，ids[i] 为空则自动生成。
func (c *Client) BulkIndexDocs(ctx context.Context, index string, docs []any, ids []string) error {
	if len(docs) == 0 {
		return nil
	}
	var b strings.Builder
	for i, doc := range docs {
		action := map[string]any{"index": map[string]any{"_index": index}}
		if i < len(ids) && ids[i] != "" {
			action["index"].(map[string]any)["_id"] = ids[i]
		}
		ab, err := json.Marshal(action)
		if err != nil {
			return err
		}
		b.Write(ab)
		b.WriteByte('\n')
		db, err := json.Marshal(doc)
		if err != nil {
			return err
		}
		b.Write(db)
		b.WriteByte('\n')
	}
	ndjson := []byte(b.String())
	res, err := c.doLog("POST", index+"/_bulk", ndjson, func() (*esapi.Response, error) {
		return c.Client.Bulk(
			bytes.NewReader(ndjson),
			c.Client.Bulk.WithContext(ctx),
		)
	})
	m, err := decodeResponse(res, err)
	if err != nil {
		return err
	}
	if errs, ok := m["errors"].(bool); ok && errs {
		return fmt.Errorf("gobreath-es: bulk 存在失败项: %v", m["items"])
	}
	return nil
}

// BulkUpsertDocs 批量 upsert（NDJSON _bulk）：每个文档按 _id 进行 upsert，
// 不存在则插入、存在则局部合并。ids 与 docs 一一对应，ids[i] 为空视为非法（upsert 必须显式 id）。
func (c *Client) BulkUpsertDocs(ctx context.Context, index string, docs []any, ids []string) error {
	if len(docs) == 0 {
		return nil
	}
	var b strings.Builder
	for i, doc := range docs {
		id := ""
		if i < len(ids) {
			id = ids[i]
		}
		if id == "" {
			return fmt.Errorf("gobreath-es: bulk upsert 第 %d 项缺少文档 id", i)
		}
		meta, err := json.Marshal(map[string]any{"update": map[string]any{"_index": index, "_id": id}})
		if err != nil {
			return err
		}
		b.Write(meta)
		b.WriteByte('\n')
		body, err := json.Marshal(map[string]any{"doc": doc, "doc_as_upsert": true})
		if err != nil {
			return err
		}
		b.Write(body)
		b.WriteByte('\n')
	}
	ndjson := []byte(b.String())
	res, err := c.doLog("POST", index+"/_bulk", ndjson, func() (*esapi.Response, error) {
		return c.Client.Bulk(
			bytes.NewReader(ndjson),
			c.Client.Bulk.WithContext(ctx),
		)
	})
	m, err := decodeResponse(res, err)
	if err != nil {
		return err
	}
	if errs, ok := m["errors"].(bool); ok && errs {
		return fmt.Errorf("gobreath-es: bulk upsert 存在失败项: %v", m["items"])
	}
	return nil
}

// GetDoc 按 id 读取文档，反序列化到 dest。不存在返回 ErrNotFound。
func (c *Client) GetDoc(ctx context.Context, index, id string, dest any) error {
	raw, err := c.getDocRaw(ctx, index, id)
	if err != nil {
		return err
	}
	src, ok := raw["_source"]
	if !ok {
		return ErrNotFound
	}
	srcBytes, err := json.Marshal(src)
	if err != nil {
		return err
	}
	return json.Unmarshal(srcBytes, dest)
}

// getDocRaw 按 id 读取文档，返回完整响应 map（含 _source / _seq_no / _primary_term / _id 等）。
// 不存在返回 ErrNotFound。供 GetWithMeta 等需要元信息的场景使用。
func (c *Client) getDocRaw(ctx context.Context, index, id string) (map[string]any, error) {
	res, err := c.doLog("GET", index+"/_doc/"+id, nil, func() (*esapi.Response, error) {
		return c.Client.Get(index, id, c.Client.Get.WithContext(ctx))
	})
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if res.IsError() {
		body, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("gobreath-es: get 失败 status=%d body=%s", res.StatusCode, string(body))
	}
	var m map[string]any
	if err := json.NewDecoder(res.Body).Decode(&m); err != nil {
		return nil, err
	}
	return m, nil
}

// DeleteDoc 按 id 删除文档。不存在视为成功（幂等）。
// 可选传入 IfSeqNoPrimaryTerm 启用乐观并发控制：仅当文档当前版本匹配时才删除。
func (c *Client) DeleteDoc(ctx context.Context, index, id string, opts ...WriteOption) error {
	reqOpts := []func(*esapi.DeleteRequest){
		c.Client.Delete.WithContext(ctx),
	}
	reqOpts = c.applyDeleteCAS(reqOpts, opts)
	res, err := c.doLog("DELETE", index+"/_doc/"+id, nil, func() (*esapi.Response, error) {
		return c.Client.Delete(index, id, reqOpts...)
	})
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return nil
	}
	if res.IsError() {
		body, _ := io.ReadAll(res.Body)
		return fmt.Errorf("gobreath-es: delete 失败 status=%d body=%s", res.StatusCode, string(body))
	}
	return nil
}

// UpdateDoc 按 id 局部更新（partial doc）。
// 可选传入 IfSeqNoPrimaryTerm 启用乐观并发控制（仅在文档当前版本匹配时更新，否则报错）。
func (c *Client) UpdateDoc(ctx context.Context, index, id string, partial any, opts ...WriteOption) error {
	body, err := json.Marshal(map[string]any{"doc": partial})
	if err != nil {
		return err
	}
	reqOpts := []func(*esapi.UpdateRequest){
		c.Client.Update.WithContext(ctx),
	}
	reqOpts = c.applyUpdateCAS(reqOpts, opts)
	res, err := c.doLog("POST", index+"/_update/"+id, body, func() (*esapi.Response, error) {
		return c.Client.Update(index, id, bytes.NewReader(body), reqOpts...)
	})
	_, err = decodeResponse(res, err)
	return err
}

// UpsertDoc 按 id 进行 upsert：文档不存在则插入 doc，存在则把 doc 作为局部更新合并进去。
// 底层使用 _update + doc_as_upsert（ES-native upsert 语义）。id 不可为空。
func (c *Client) UpsertDoc(ctx context.Context, index, id string, doc any) error {
	if id == "" {
		return fmt.Errorf("gobreath-es: upsert requires a document id")
	}
	body, err := json.Marshal(map[string]any{
		"doc":            doc,
		"doc_as_upsert": true,
	})
	if err != nil {
		return err
	}
	res, err := c.doLog("POST", index+"/_update/"+id, body, func() (*esapi.Response, error) {
		return c.Client.Update(
			index, id, bytes.NewReader(body),
			c.Client.Update.WithContext(ctx),
		)
	})
	_, err = decodeResponse(res, err)
	return err
}

// DocExists 判断文档是否存在。
func (c *Client) DocExists(ctx context.Context, index, id string) (bool, error) {
	res, err := c.doLog("HEAD", index+"/_doc/"+id, nil, func() (*esapi.Response, error) {
		return c.Client.Exists(index, id, c.Client.Exists.WithContext(ctx))
	})
	if err != nil {
		return false, err
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		if res.IsError() {
			b, _ := io.ReadAll(res.Body)
			return false, fmt.Errorf("gobreath-es: exists 失败 status=%d body=%s", res.StatusCode, string(b))
		}
		return false, nil
	}
}

// searchRaw 执行 _search，返回原始响应 map、命中 hit（含 _source/_id）列表、总数、耗时。
// usePIT=true 时省略索引名（PIT 必须走全局 _search）。
func (c *Client) searchRaw(ctx context.Context, index string, body map[string]any, usePIT bool) (map[string]any, []json.RawMessage, int64, int, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, nil, 0, 0, err
	}
	path := "_search"
	if !usePIT {
		path = index + "/_search"
	}
	opts := []func(*esapi.SearchRequest){
		c.Client.Search.WithContext(ctx),
		c.Client.Search.WithBody(bytes.NewReader(b)),
	}
	if !usePIT {
		opts = append(opts, c.Client.Search.WithIndex(index))
	}
	res, err := c.doLog("POST", path, b, func() (*esapi.Response, error) {
		return c.Client.Search(opts...)
	})
	m, err := decodeResponse(res, err)
	if err != nil {
		return nil, nil, 0, 0, err
	}
	var hits []json.RawMessage
	var total int64
	var took int
	if hitsObj, ok := m["hits"].(map[string]any); ok {
		if tObj, ok := hitsObj["total"].(map[string]any); ok {
			if v, ok := tObj["value"].(float64); ok {
				total = int64(v)
			}
		}
		if arr, ok := hitsObj["hits"].([]any); ok {
			for _, h := range arr {
				if hb, e := json.Marshal(h); e == nil {
					hits = append(hits, hb)
				}
			}
		}
	}
	if v, ok := m["took"].(float64); ok {
		took = int(v)
	}
	return m, hits, total, took, nil
}

// CountRaw 按查询条件统计命中数（使用 _count API）。
func (c *Client) CountRaw(ctx context.Context, index string, query map[string]any) (int64, error) {
	body, err := json.Marshal(map[string]any{"query": query})
	if err != nil {
		return 0, err
	}
	res, err := c.doLog("POST", index+"/_count", body, func() (*esapi.Response, error) {
		return c.Client.Count(
			c.Client.Count.WithIndex(index),
			c.Client.Count.WithBody(bytes.NewReader(body)),
			c.Client.Count.WithContext(ctx),
		)
	})
	m, err := decodeResponse(res, err)
	if err != nil {
		return 0, err
	}
	if v, ok := m["count"].(float64); ok {
		return int64(v), nil
	}
	return 0, nil
}

// CreateIndexRaw 创建索引（body 为 mapping/settings JSON）。已存在时返回 nil（幂等）。
func (c *Client) CreateIndexRaw(ctx context.Context, index string, body map[string]any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	res, err := c.doLog("PUT", index, b, func() (*esapi.Response, error) {
		return c.Client.Indices.Create(
			index,
			c.Client.Indices.Create.WithBody(bytes.NewReader(b)),
			c.Client.Indices.Create.WithContext(ctx),
		)
	})
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusBadRequest {
		b2, _ := io.ReadAll(res.Body)
		if strings.Contains(string(b2), "resource_already_exists_exception") {
			return nil // 索引已存在，视为成功
		}
		return fmt.Errorf("gobreath-es: 创建索引失败 body=%s", string(b2))
	}
	if res.IsError() {
		b2, _ := io.ReadAll(res.Body)
		return fmt.Errorf("gobreath-es: 创建索引失败 status=%d body=%s", res.StatusCode, string(b2))
	}
	return nil
}

// ---- Point In Time（一致性翻页快照）----

// OpenPIT 开启一个 Point In Time，返回 pit id。keepAlive 为空默认 "1m"。
// 该 id 应传给 Query.PIT，并与 SearchAfter 配合实现不受 max_result_window 限制的深翻页。
func (c *Client) OpenPIT(ctx context.Context, index, keepAlive string) (string, error) {
	if keepAlive == "" {
		keepAlive = "1m"
	}
	res, err := c.doLog("POST", "_pit", nil, func() (*esapi.Response, error) {
		return c.Client.OpenPointInTime(
			[]string{index},
			keepAlive,
			c.Client.OpenPointInTime.WithContext(ctx),
		)
	})
	m, err := decodeResponse(res, err)
	if err != nil {
		return "", err
	}
	if id, ok := m["id"].(string); ok {
		return id, nil
	}
	return "", fmt.Errorf("gobreath-es: OpenPIT 响应缺少 id: %v", m)
}

// ClosePIT 关闭一个 Point In Time，释放集群资源。翻页完成后务必调用。
func (c *Client) ClosePIT(ctx context.Context, id string) error {
	body, _ := json.Marshal(map[string]any{"id": id})
	res, err := c.doLog("DELETE", "_pit", body, func() (*esapi.Response, error) {
		return c.Client.ClosePointInTime(
			c.Client.ClosePointInTime.WithContext(ctx),
			c.Client.ClosePointInTime.WithBody(bytes.NewReader(body)),
		)
	})
	_, err = decodeResponse(res, err)
	return err
}

// ---- 组合式索引模板（_index_template）----

// PutIndexTemplate 创建/更新组合式索引模板。body 一般为 BuildIndexTemplate[T] 的产出。
func (c *Client) PutIndexTemplate(ctx context.Context, name string, body map[string]any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	res, err := c.doLog("PUT", "_index_template/"+name, b, func() (*esapi.Response, error) {
		return c.Client.Indices.PutIndexTemplate(
			name,
			bytes.NewReader(b),
			c.Client.Indices.PutIndexTemplate.WithContext(ctx),
		)
	})
	_, err = decodeResponse(res, err)
	return err
}

// GetIndexTemplate 读取组合式索引模板，返回响应里的 index_templates 数组（通常为单元素）。
func (c *Client) GetIndexTemplate(ctx context.Context, name string) ([]map[string]any, error) {
	res, err := c.doLog("GET", "_index_template/"+name, nil, func() (*esapi.Response, error) {
		return c.Client.Indices.GetIndexTemplate(
			c.Client.Indices.GetIndexTemplate.WithContext(ctx),
			c.Client.Indices.GetIndexTemplate.WithName(name),
		)
	})
	m, err := decodeResponse(res, err)
	if err != nil {
		return nil, err
	}
	if arr, ok := m["index_templates"].([]any); ok {
		out := make([]map[string]any, 0, len(arr))
		for _, it := range arr {
			if mp, ok := it.(map[string]any); ok {
				out = append(out, mp)
			}
		}
		return out, nil
	}
	return []map[string]any{}, nil
}

// DeleteIndexTemplate 删除组合式索引模板（不存在视为成功）。
func (c *Client) DeleteIndexTemplate(ctx context.Context, name string) error {
	res, err := c.doLog("DELETE", "_index_template/"+name, nil, func() (*esapi.Response, error) {
		return c.Client.Indices.DeleteIndexTemplate(
			name,
			c.Client.Indices.DeleteIndexTemplate.WithContext(ctx),
		)
	})
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return nil
	}
	if res.IsError() {
		b, _ := io.ReadAll(res.Body)
		return fmt.Errorf("gobreath-es: 删除索引模板失败 status=%d body=%s", res.StatusCode, string(b))
	}
	return nil
}

// IndexTemplateExists 判断组合式索引模板是否存在。
func (c *Client) IndexTemplateExists(ctx context.Context, name string) (bool, error) {
	res, err := c.doLog("HEAD", "_index_template/"+name, nil, func() (*esapi.Response, error) {
		return c.Client.Indices.ExistsIndexTemplate(
			name,
			c.Client.Indices.ExistsIndexTemplate.WithContext(ctx),
		)
	})
	if err != nil {
		return false, err
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		if res.IsError() {
			b, _ := io.ReadAll(res.Body)
			return false, fmt.Errorf("gobreath-es: 模板存在性检查失败 status=%d body=%s", res.StatusCode, string(b))
		}
		return false, nil
	}
}
