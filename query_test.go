package es

import (
	"encoding/json"
	"testing"
)

func TestQueryEqAndMust(t *testing.T) {
	q := NewQuery[product]().Eq(Col[product](func(p *product) *string { return &p.ID }), "p1")
	body := q.BuildBody()
	qj, _ := json.Marshal(body)
	want := `{"query":{"bool":{"must":[{"term":{"id":"p1"}}]}}}`
	assertJSON(t, string(qj), want)
}

func TestQueryOrGroup(t *testing.T) {
	id := Col[product](func(p *product) *string { return &p.ID })
	q := NewQuery[product]().Eq(id, "p1").Or().Eq(id, "p2")
	qj, _ := json.Marshal(q.BuildQuery())
	// 单组内两个 term，应渲染成 should + minimum_should_match=1
	want := `{"bool":{"must":[{"bool":{"minimum_should_match":1,"should":[{"term":{"id":"p1"}},{"term":{"id":"p2"}}]}}]}}`
	assertJSON(t, string(qj), want)
}

func TestQueryRangeAndSortAndPaging(t *testing.T) {
	price := Col[product](func(p *product) *float64 { return &p.Price })
	q := NewQuery[product]().Ge(price, 10).Lt(price, 100).OrderBy(price, false).From(10).Size(5)
	qj, _ := json.Marshal(q.BuildBody())
	want := `{"from":10,"query":{"bool":{"must":[{"range":{"price":{"gte":10}}},{"range":{"price":{"lt":100}}}]}},"size":5,"sort":[{"price":"desc"}]}`
	assertJSON(t, string(qj), want)
}

func TestQueryMustNotAndFilter(t *testing.T) {
	id := Col[product](func(p *product) *string { return &p.ID })
	stock := Col[product](func(p *product) *bool { return &p.InStock })
	q := NewQuery[product]().
		MustNot(func(q *Query[product]) { q.Eq(id, "x") }).
		Filter(func(q *Query[product]) { q.Eq(stock, true) })
	qj, _ := json.Marshal(q.BuildQuery())
	want := `{"bool":{"filter":[{"term":{"in_stock":true}}],"must_not":[{"term":{"id":"x"}}]}}`
	assertJSON(t, string(qj), want)
}

func TestQueryExistsAndNested(t *testing.T) {
	name := Col[product](func(p *product) *string { return &p.Name })
	q := NewQuery[product]().Exists(name).
		Nested("tags", func(q *Query[product]) {
			q.Eq(Col[product](func(p *product) *string { return &p.Name }), "red")
		})
	qj, _ := json.Marshal(q.BuildQuery())
	want := `{"bool":{"must":[{"exists":{"field":"name"}},{"nested":{"path":"tags","query":{"bool":{"must":[{"term":{"name":"red"}}]}}}}]}}`
	assertJSON(t, string(qj), want)
}

func TestQueryIfAndIn(t *testing.T) {
	cat := Col[product](func(p *product) *int64 { return &p.CatID })
	q := NewQuery[product]().
		If(false, func(q *Query[product]) { q.Eq(cat, 99) }).
		If(true, func(q *Query[product]) { q.In(cat, []any{1, 2, 3}) })
	qj, _ := json.Marshal(q.BuildQuery())
	want := `{"bool":{"must":[{"terms":{"cat_id":[1,2,3]}}]}}`
	assertJSON(t, string(qj), want)
}

func TestQueryWithAggsAndTrackTotal(t *testing.T) {
	cat := Col[product](func(p *product) *int64 { return &p.CatID })
	price := Col[product](func(p *product) *float64 { return &p.Price })
	byCat := NewAggregation().Terms("by_cat", cat, 5)
	byCat.Sub(NewAggregation().Avg("avg_price", price))
	aggs := NewAggregation()
	aggs.Add(byCat)
	q := NewQuery[product]().TrackTotalHits(true).Aggregate(aggs)
	qj, _ := json.Marshal(q.BuildBody())
	want := `{"aggs":{"by_cat":{"terms":{"field":"cat_id","size":5},"aggs":{"avg_price":{"avg":{"field":"price"}}}}},"query":{"match_all":{}},"track_total_hits":true}`
	assertJSON(t, string(qj), want)
}

// assertJSON 比较两个 JSON 是否语义相等（忽略 key 顺序）。
func assertJSON(t *testing.T, got, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal([]byte(got), &g); err != nil {
		t.Fatalf("got 非合法 JSON: %v\n%s", err, got)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("want 非合法 JSON: %v\n%s", err, want)
	}
	if !equalJSON(g, w) {
		t.Fatalf("JSON 不相等\n got=%s\nwant=%s", got, want)
	}
}

func equalJSON(a, b any) bool {
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for k, v := range av {
			if !equalJSON(v, bv[k]) {
				return false
			}
		}
		return true
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !equalJSON(av[i], bv[i]) {
				return false
			}
		}
		return true
	default:
		return a == b
	}
}
