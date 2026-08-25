package es

import (
	"encoding/json"
	"testing"
)

func TestAggregationBuild(t *testing.T) {
	cat := Col[product](func(p *product) *int64 { return &p.CatID })
	price := Col[product](func(p *product) *float64 { return &p.Price })

	byCat := NewAggregation().Terms("by_cat", cat, 10)
	byCat.Sub(NewAggregation().Avg("avg_price", price))
	byCat.Sub(&Agg{name: "max_price", typ: "max", body: map[string]any{"field": price.name}})

	aggs := NewAggregation()
	aggs.Add(byCat)

	b, _ := json.Marshal(aggs.Build())
	want := `{"by_cat":{"terms":{"field":"cat_id","size":10},"aggs":{"avg_price":{"avg":{"field":"price"}},"max_price":{"max":{"field":"price"}}}}}`
	assertJSON(t, string(b), want)
}

func TestDateHistogramAgg(t *testing.T) {
	aggs := NewAggregation()
	aggs.DateHistogram("per_day", "created_at", "day", "yyyy-MM-dd")
	b, _ := json.Marshal(aggs.Build())
	want := `{"per_day":{"date_histogram":{"calendar_interval":"day","field":"created_at","format":"yyyy-MM-dd"}}}`
	assertJSON(t, string(b), want)
}
