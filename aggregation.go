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

// NewAgg 显式构造一个聚合节点（用于组合 / 子聚合 / 自定义类型）。
func NewAgg(name, typ string, body map[string]any) *Agg {
	return &Agg{name: name, typ: typ, body: body}
}

// compositeSource 组合聚合的一个分组源（terms / histogram / date_histogram）。
type compositeSource struct {
	name     string
	kind     string // terms | histogram | date_histogram
	field    string
	interval string
	format   string
	order    string
}

// CompositeAgg 组合聚合（composite）构造器。它是 ES 官方推荐的"可翻页聚合"方式，
// 用于突破 search.max_buckets 对单请求桶总数的限制。每页通过 After(map) 续接上一页的 key。
type CompositeAgg struct {
	name    string
	size    int
	after   map[string]any
	sources []compositeSource
	subs    map[string]*Agg
}

// NewComposite 创建组合聚合构造器，name 为其在 aggs 下的 key。
func NewComposite(name string) *CompositeAgg {
	return &CompositeAgg{name: name}
}

// Terms 增加一个 terms 类型的分组源。
func (c *CompositeAgg) Terms(name string, col ColExpr, order string) *CompositeAgg {
	c.sources = append(c.sources, compositeSource{name: name, kind: "terms", field: col.name, order: order})
	return c
}

// Histogram 增加一个数值直方图分组源（interval 为数值字符串，如 "10"）。
func (c *CompositeAgg) Histogram(name string, col ColExpr, interval string, order string) *CompositeAgg {
	c.sources = append(c.sources, compositeSource{name: name, kind: "histogram", field: col.name, interval: interval, order: order})
	return c
}

// DateHistogram 增加一个日期直方图分组源（calendarInterval 如 "day"，format 如 "yyyy-MM-dd"）。
func (c *CompositeAgg) DateHistogram(name string, col ColExpr, interval, format, order string) *CompositeAgg {
	c.sources = append(c.sources, compositeSource{name: name, kind: "date_histogram", field: col.name, interval: interval, format: format, order: order})
	return c
}

// Size 设置每页桶数（ES 对 composite 的 size 上限通常为 10000）。
func (c *CompositeAgg) Size(n int) *CompositeAgg { c.size = n; return c }

// After 设置翻页游标（取上一页最后一个 bucket 的 key 作为 map，如 {"cat":"1","date":"2026-01-01"}）。
func (c *CompositeAgg) After(after map[string]any) *CompositeAgg { c.after = after; return c }

// Sub 追加子聚合（桶内指标，如 avg/sum）。
func (c *CompositeAgg) Sub(sub *Agg) *CompositeAgg {
	if c.subs == nil {
		c.subs = map[string]*Agg{}
	}
	c.subs[sub.name] = sub
	return c
}

// Agg 渲染为 *Agg，经 Aggregation.Add 即可加入聚合容器。
func (c *CompositeAgg) Agg() *Agg {
	sources := make([]map[string]any, 0, len(c.sources))
	for _, s := range c.sources {
		inner := map[string]any{"field": s.field}
		switch s.kind {
		case "histogram":
			inner["interval"] = s.interval
		case "date_histogram":
			inner["calendar_interval"] = s.interval
			if s.format != "" {
				inner["format"] = s.format
			}
		}
		if s.order != "" {
			inner["order"] = s.order
		}
		sources = append(sources, map[string]any{s.name: map[string]any{s.kind: inner}})
	}
	comp := map[string]any{"sources": sources}
	if c.size > 0 {
		comp["size"] = c.size
	}
	if c.after != nil {
		comp["after"] = c.after
	}
	return &Agg{name: c.name, typ: "composite", body: comp, subs: c.subs}
}
