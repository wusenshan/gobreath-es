package es

import (
	"encoding/json"
	"reflect"
)

// SearchResult[T] 泛型检索结果。
type SearchResult[T any] struct {
	Total int64          // 命中总数（受 track_total_hits 影响）
	Took  int            // 耗时（毫秒）
	Hits  []T            // 命中文档（已反序列化到 T）
	Scores []float64     // 与 Hits 一一对应的 _score（向量检索时为相似度得分，可用于排序/截断）
	SeqNos       []int64 // 与 Hits 一一对应的 _seq_no，供乐观并发控制（Index/Update/Delete + IfSeqNoPrimaryTerm）回传
	PrimaryTerms []int64 // 与 Hits 一一对应的 _primary_term，供乐观并发控制回传
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

// HitMeta 返回第 i 个命中文档的 ES 元信息（_id / _seq_no / _primary_term），
// 用于乐观并发控制：读取后修改，再连同这些值交给 Index/Update/Delete 的 IfSeqNoPrimaryTerm。
// 越界时返回零值 DocMeta（ID 为空）。
func (r *SearchResult[T]) HitMeta(i int) DocMeta {
	if i < 0 || i >= len(r.Hits) {
		return DocMeta{}
	}
	meta := getMeta[T]()
	id := ""
	if meta.idField != nil {
		rv := reflect.ValueOf(&r.Hits[i]).Elem()
		if fv := rv.FieldByName(meta.idField.goName); fv.IsValid() {
			id = toString(fv)
		}
	}
	var seqNo, pt int64
	if i < len(r.SeqNos) {
		seqNo = r.SeqNos[i]
	}
	if i < len(r.PrimaryTerms) {
		pt = r.PrimaryTerms[i]
	}
	return DocMeta{ID: id, SeqNo: seqNo, PrimaryTerm: pt}
}

// unmarshalHits 把 ES 返回的 hit 数组（每个含 _source / _id / _score / _seq_no / _primary_term）
// 反序列化为 []T，并把 _id 回填到 T 的 id 字段（若模型声明了 id 字段）；
// 同时返回与 Hits 对齐的 _score、_seq_no、_primary_term 列表，供乐观并发控制使用。
func unmarshalHits[T any](hits []json.RawMessage) ([]T, []float64, []int64, []int64, error) {
	meta := getMeta[T]()
	out := make([]T, 0, len(hits))
	scores := make([]float64, 0, len(hits))
	seqNos := make([]int64, 0, len(hits))
	primaryTerms := make([]int64, 0, len(hits))
	for _, h := range hits {
		var hit map[string]json.RawMessage
		if err := json.Unmarshal(h, &hit); err != nil {
			return nil, nil, nil, nil, err
		}
		src, ok := hit["_source"]
		if !ok {
			continue
		}
		var t T
		if err := json.Unmarshal(src, &t); err != nil {
			return nil, nil, nil, nil, err
		}
		if meta.idField != nil {
			if idRaw, ok := hit["_id"]; ok {
				var id string
				if err := json.Unmarshal(idRaw, &id); err == nil && id != "" {
					setID(meta, &t, id)
				}
			}
		}
		var sc, sn, pt float64
		if scRaw, ok := hit["_score"]; ok {
			_ = json.Unmarshal(scRaw, &sc)
		}
		if snRaw, ok := hit["_seq_no"]; ok {
			_ = json.Unmarshal(snRaw, &sn)
		}
		if ptRaw, ok := hit["_primary_term"]; ok {
			_ = json.Unmarshal(ptRaw, &pt)
		}
		out = append(out, t)
		scores = append(scores, sc)
		seqNos = append(seqNos, int64(sn))
		primaryTerms = append(primaryTerms, int64(pt))
	}
	return out, scores, seqNos, primaryTerms, nil
}
