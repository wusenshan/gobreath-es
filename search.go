package es

import "encoding/json"

// SearchResult[T] 泛型检索结果。
type SearchResult[T any] struct {
	Total int64          // 命中总数（受 track_total_hits 影响）
	Took  int            // 耗时（毫秒）
	Hits  []T            // 命中文档（已反序列化到 T）
	Scores []float64     // 与 Hits 一一对应的 _score（向量检索时为相似度得分，可用于排序/截断）
	Raw   map[string]any // 原始响应（含 aggregations、_scroll_id 等，按需取用）
}

// Aggregations 返回原始聚合结果（顶层 "aggregations" 对象）。无聚合时返回 nil。
func (r *SearchResult[T]) Aggregations() map[string]any {
	if r.Raw == nil {
		return nil
	}
	if agg, ok := r.Raw["aggregations"].(map[string]any); ok {
		return agg
	}
	return nil
}

// unmarshalHits 把 ES 返回的 hit 数组（每个含 _source / _id / _score）反序列化为 []T，
// 并把 _id 回填到 T 的 id 字段（若模型声明了 id 字段）；同时返回与 Hits 对齐的 _score 列表。
func unmarshalHits[T any](hits []json.RawMessage) ([]T, []float64, error) {
	meta := getMeta[T]()
	out := make([]T, 0, len(hits))
	scores := make([]float64, 0, len(hits))
	for _, h := range hits {
		var hit map[string]json.RawMessage
		if err := json.Unmarshal(h, &hit); err != nil {
			return nil, nil, err
		}
		src, ok := hit["_source"]
		if !ok {
			continue
		}
		var t T
		if err := json.Unmarshal(src, &t); err != nil {
			return nil, nil, err
		}
		if meta.idField != nil {
			if idRaw, ok := hit["_id"]; ok {
				var id string
				if err := json.Unmarshal(idRaw, &id); err == nil && id != "" {
					setID(meta, &t, id)
				}
			}
		}
		var sc float64
		if scRaw, ok := hit["_score"]; ok {
			_ = json.Unmarshal(scRaw, &sc)
		}
		out = append(out, t)
		scores = append(scores, sc)
	}
	return out, scores, nil
}
