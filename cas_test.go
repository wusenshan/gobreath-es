package es

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	elastic "github.com/elastic/go-elasticsearch/v8"
	esapi "github.com/elastic/go-elasticsearch/v8/esapi"
)

// fakeRT 拦截所有 ES 请求，记录 method/path/query/body，并返回预置响应，
// 使乐观并发、Upsert、SearchRaw 等可在无真实 ES 的情况下做离线 DSL 校验。
type fakeRT struct {
	method   string
	path     string
	rawQuery string
	body     []byte

	getBody    string // GET（GetWithMeta）返回的响应体
	writeBody  string // 写入类返回的响应体
	searchBody string // _search 返回的响应体
}

func (f *fakeRT) RoundTrip(req *http.Request) (*http.Response, error) {
	b, _ := io.ReadAll(req.Body)
	f.method = req.Method
	f.path = req.URL.Path
	f.rawQuery = req.URL.RawQuery
	f.body = b

	var resp string
	switch {
	case strings.HasSuffix(req.URL.Path, "/_search") || req.URL.Path == "/_search":
		resp = f.searchBody
	case req.Method == http.MethodGet:
		resp = f.getBody
	default:
		resp = f.writeBody
	}
	if resp == "" {
		if strings.Contains(req.URL.Path, "_search") {
			resp = `{"hits":{"total":{"value":0},"hits":[]},"took":0}`
		} else if req.Method == http.MethodGet {
			resp = `{"_index":"products","_id":"1","_seq_no":10,"_primary_term":1,"_source":{"id":"1","name":"alpha"}}`
		} else {
			resp = `{"_shards":{"failed":0}}`
		}
	}
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(resp)),
		Header: http.Header{
			"Content-Type":     []string{"application/json"},
			"X-Elastic-Product": []string{"Elasticsearch"},
		},
	}, nil
}

func newFakeClient(t *testing.T) (*Client, *fakeRT) {
	t.Helper()
	rt := &fakeRT{}
	ec, err := elastic.NewClient(elastic.Config{
		Addresses: []string{"http://127.0.0.1:9200"},
		Transport: rt,
	})
	if err != nil {
		t.Fatalf("创建假客户端失败: %v", err)
	}
	return &Client{Client: ec}, rt
}

// TestIndexOCCSendsQueryParams 验证乐观并发控制会作为 if_seq_no/if_primary_term 查询参数发出。
func TestIndexOCCSendsQueryParams(t *testing.T) {
	cli, rt := newFakeClient(t)
	err := cli.IndexDoc(context.Background(), "products", "1", product{ID: "1", Name: "alpha"},
		IfSeqNoPrimaryTerm(10, 1))
	if err != nil {
		t.Fatalf("IndexDoc 失败: %v", err)
	}
	if !strings.Contains(rt.rawQuery, "if_seq_no=10") || !strings.Contains(rt.rawQuery, "if_primary_term=1") {
		t.Fatalf("期望携带乐观并发查询参数，实际 query=%q", rt.rawQuery)
	}
}

// TestApplyIndexCASBuildsRequestFields 直接校验 applyIndexCAS 把 WriteOption 落到请求结构上。
func TestApplyIndexCASBuildsRequestFields(t *testing.T) {
	cli, _ := newFakeClient(t)
	opts := cli.applyIndexCAS(nil, []WriteOption{IfSeqNoPrimaryTerm(7, 3)})
	req := &esapi.IndexRequest{}
	for _, o := range opts {
		o(req)
	}
	if req.IfSeqNo == nil || *req.IfSeqNo != 7 || req.IfPrimaryTerm == nil || *req.IfPrimaryTerm != 3 {
		t.Fatalf("期望 IfSeqNo=7 IfPrimaryTerm=3，实际 %v / %v", req.IfSeqNo, req.IfPrimaryTerm)
	}
}

// TestUpsertDocDSL 验证 UpsertDoc 使用 _update + doc_as_upsert。
func TestUpsertDocDSL(t *testing.T) {
	cli, rt := newFakeClient(t)
	err := cli.UpsertDoc(context.Background(), "products", "1", product{ID: "1", Name: "alpha"})
	if err != nil {
		t.Fatalf("UpsertDoc 失败: %v", err)
	}
	if rt.path != "/products/_update/1" {
		t.Fatalf("期望路径 /products/_update/1，实际 %q", rt.path)
	}
	var body map[string]any
	if err := json.Unmarshal(rt.body, &body); err != nil {
		t.Fatalf("解析请求体失败: %v", err)
	}
	if v, ok := body["doc_as_upsert"].(bool); !ok || !v {
		t.Fatalf("期望 doc_as_upsert=true，实际 %v", body["doc_as_upsert"])
	}
	if _, ok := body["doc"].(map[string]any); !ok {
		t.Fatalf("期望包含 doc 对象，实际 %v", body)
	}
}

// TestBulkUpsertDSL 验证 BulkUpsertDocs 产出 update 动作 + doc_as_upsert 的 NDJSON。
func TestBulkUpsertDSL(t *testing.T) {
	cli, rt := newFakeClient(t)
	err := cli.BulkUpsertDocs(context.Background(), "products",
		[]any{product{ID: "1", Name: "a"}}, []string{"1"})
	if err != nil {
		t.Fatalf("BulkUpsertDocs 失败: %v", err)
	}
	if rt.path != "/_bulk" {
		t.Fatalf("期望路径 /_bulk，实际 %q", rt.path)
	}
	lines := strings.Split(strings.TrimSpace(string(rt.body)), "\n")
	if len(lines) != 2 {
		t.Fatalf("期望 2 行 NDJSON，实际 %d 行: %q", len(lines), rt.body)
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &meta); err != nil {
		t.Fatalf("解析动作行失败: %v", err)
	}
	upd, ok := meta["update"].(map[string]any)
	if !ok || upd["_id"] != "1" || upd["_index"] != "products" {
		t.Fatalf("期望 update 动作含 _id=1/_index=products，实际 %v", meta)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &payload); err != nil {
		t.Fatalf("解析载荷行失败: %v", err)
	}
	if v, ok := payload["doc_as_upsert"].(bool); !ok || !v {
		t.Fatalf("期望 doc_as_upsert=true，实际 %v", payload["doc_as_upsert"])
	}
}

// TestGetWithMeta 验证 GetWithMeta 解析并返回 _seq_no/_primary_term。
func TestGetWithMeta(t *testing.T) {
	raw := map[string]any{
		"_index":        "products",
		"_id":           "1",
		"_seq_no":       float64(10),
		"_primary_term": float64(1),
		"_source":       map[string]any{"id": "1", "name": "alpha"},
	}
	doc, meta, err := parseGetResult[product](raw, getMeta[product](), "1")
	if err != nil {
		t.Fatalf("parseGetResult 失败: %v", err)
	}
	if doc.ID != "1" {
		t.Fatalf("期望回填 id=1，实际 %q", doc.ID)
	}
	if meta.SeqNo != 10 || meta.PrimaryTerm != 1 {
		t.Fatalf("期望 SeqNo=10 PrimaryTerm=1，实际 %d / %d", meta.SeqNo, meta.PrimaryTerm)
	}

	// 缺 _source 视为未找到
	if _, _, e := parseGetResult[product](map[string]any{}, getMeta[product](), "x"); e != ErrNotFound {
		t.Fatalf("缺少 _source 应返回 ErrNotFound，实际 %v", e)
	}
}

// TestRepoUpsertAndBulkUpsert 验证 Repo 层 upsert 透传 id。
func TestRepoUpsertAndBulkUpsert(t *testing.T) {
	cli, rt := newFakeClient(t)
	repo := &Repo[product]{cli: cli, index: "products", meta: getMeta[product]()}
	if err := repo.Upsert(context.Background(), product{ID: "1", Name: "alpha"}); err != nil {
		t.Fatalf("Repo.Upsert 失败: %v", err)
	}
	if rt.path != "/products/_update/1" {
		t.Fatalf("Repo.Upsert 路径不符: %q", rt.path)
	}
	if err := repo.BulkUpsert(context.Background(), []product{{ID: "1"}, {ID: "2"}}); err != nil {
		t.Fatalf("Repo.BulkUpsert 失败: %v", err)
	}
	if rt.path != "/_bulk" {
		t.Fatalf("Repo.BulkUpsert 路径不符: %q", rt.path)
	}
	if err := repo.Upsert(context.Background(), product{Name: "no-id"}); err == nil {
		t.Fatalf("缺少 id 的 upsert 应报错")
	}
}

// TestRepoSearchRaw 验证 SearchRaw 逃生舱返回原始响应 map。
func TestRepoSearchRaw(t *testing.T) {
	cli, rt := newFakeClient(t)
	repo := &Repo[product]{cli: cli, index: "products", meta: getMeta[product]()}
	rt.searchBody = `{"hits":{"total":{"value":3},"hits":[]},"took":1}`
	raw, err := repo.SearchRaw(context.Background(), map[string]any{
		"query": map[string]any{"match_all": map[string]any{}},
	})
	if err != nil {
		t.Fatalf("SearchRaw 失败: %v", err)
	}
	hits, ok := raw["hits"].(map[string]any)
	if !ok {
		t.Fatalf("期望返回原始 hits，实际 %v", raw)
	}
	if total, ok := hits["total"].(map[string]any)["value"].(float64); !ok || total != 3 {
		t.Fatalf("期望 total=3，实际 %v", hits["total"])
	}
}
