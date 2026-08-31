package gentest

import (
	"strings"
	"testing"

	es "github.com/wusenshan/gobreath-es"
)

// TestProductColsInQuery 验证由 esgen 生成的 ProductCols 能直接用于查询构造，
// 且字段名解析正确（json tag 优先，如 Name->"name"、CatID->"cat_id"）。
func TestProductColsInQuery(t *testing.T) {
	q := es.NewQuery[Product]().Eq(ProductCols.Name, "iPhone")
	dsl := q.Build()
	if !strings.Contains(dsl, "name") {
		t.Fatalf("Eq(ProductCols.Name,...) 的 DSL 应包含字段名 name，实际: %s", dsl)
	}

	q2 := es.NewQuery[Product]().Ge(ProductCols.Price, 1000).Eq(ProductCols.InStock, true)
	dsl2 := q2.Build()
	if !strings.Contains(dsl2, "price") {
		t.Fatalf("Ge(ProductCols.Price,...) 的 DSL 应包含字段名 price，实际: %s", dsl2)
	}
	if !strings.Contains(dsl2, "in_stock") {
		t.Fatalf("Eq(ProductCols.InStock,...) 的 DSL 应包含字段名 in_stock，实际: %s", dsl2)
	}

	q3 := es.NewQuery[Product]().Eq(ProductCols.CatID, int64(1))
	dsl3 := q3.Build()
	if !strings.Contains(dsl3, "cat_id") {
		t.Fatalf("Eq(ProductCols.CatID,...) 的 DSL 应包含字段名 cat_id，实际: %s", dsl3)
	}
}
