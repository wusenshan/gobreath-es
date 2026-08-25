package es

// Agg 单个聚合定义。可携带子聚合（Sub）。
type Agg struct {
	name string
	typ  string // terms / date_histogram / histogram / avg / sum / min / max / value_count / cardinality ...
	body map[string]any
	subs map[string]*Agg
}

// Build 渲染为 ES 聚合 DSL。
func (a *Agg) Build() map[string]any {
	out := map[string]any{a.typ: a.body}
	if len(a.subs) > 0 {
		subMap := map[string]any{}
		for _, s := range a.subs {
			subMap[s.name] = s.Build()
		}
		out["aggs"] = subMap
	}
	return out
}

// Sub 给当前聚合追加一个子聚合（用于桶内再聚合，如 terms 下再算 avg）。
func (a *Agg) Sub(sub *Agg) *Agg {
	if a.subs == nil {
		a.subs = map[string]*Agg{}
	}
	a.subs[sub.name] = sub
	return a
}

// Aggregation 顶层聚合容器，对应 DSL 里的 "aggs" 对象。
type Aggregation struct {
	aggs map[string]*Agg
}

// NewAggregation 创建聚合容器。
func NewAggregation() *Aggregation {
	return &Aggregation{aggs: map[string]*Agg{}}
}

// Add 显式追加一个已构建的聚合。
func (a *Aggregation) Add(agg *Agg) *Agg {
	a.aggs[agg.name] = agg
	return agg
}

// Terms 词条聚合（按字段值分桶）。
func (a *Aggregation) Terms(name string, col ColExpr, size int) *Agg {
	if size <= 0 {
		size = 10
	}
	return a.Add(&Agg{
		name: name,
		typ:  "terms",
		body: map[string]any{"field": col.name, "size": size},
	})
}

// DateHistogram 日期直方图聚合。
// calendarInterval 如 "day"/"month"/"hour"；format 如 "yyyy-MM-dd"。
func (a *Aggregation) DateHistogram(name, col, calendarInterval, format string) *Agg {
	body := map[string]any{
		"field":             col,
		"calendar_interval": calendarInterval,
	}
	if format != "" {
		body["format"] = format
	}
	return a.Add(&Agg{name: name, typ: "date_histogram", body: body})
}

// Histogram 数值直方图聚合。
func (a *Aggregation) Histogram(name, col string, interval float64) *Agg {
	return a.Add(&Agg{
		name: name,
		typ:  "histogram",
		body: map[string]any{"field": col, "interval": interval},
	})
}

// Avg 平均值聚合。
func (a *Aggregation) Avg(name string, col ColExpr) *Agg {
	return a.Add(&Agg{name: name, typ: "avg", body: map[string]any{"field": col.name}})
}

// Sum 求和聚合。
func (a *Aggregation) Sum(name string, col ColExpr) *Agg {
	return a.Add(&Agg{name: name, typ: "sum", body: map[string]any{"field": col.name}})
}

// Min 最小值聚合。
func (a *Aggregation) Min(name string, col ColExpr) *Agg {
	return a.Add(&Agg{name: name, typ: "min", body: map[string]any{"field": col.name}})
}

// Max 最大值聚合。
func (a *Aggregation) Max(name string, col ColExpr) *Agg {
	return a.Add(&Agg{name: name, typ: "max", body: map[string]any{"field": col.name}})
}

// ValueCount 计数聚合（含 null 外的文档数）。
func (a *Aggregation) ValueCount(name string, col ColExpr) *Agg {
	return a.Add(&Agg{name: name, typ: "value_count", body: map[string]any{"field": col.name}})
}

// Cardinality 去重计数聚合（近似 HyperLogLog）。
func (a *Aggregation) Cardinality(name string, col ColExpr) *Agg {
	return a.Add(&Agg{name: name, typ: "cardinality", body: map[string]any{"field": col.name}})
}

// Build 渲染为 "aggs" 对象。
func (a *Aggregation) Build() map[string]any {
	out := map[string]any{}
	for _, agg := range a.aggs {
		out[agg.name] = agg.Build()
	}
	return out
}
