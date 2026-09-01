package es

import (
	"encoding/json"
	"testing"
)

// TestUnmarshalHitsScores 验证 _id 回填、_score 与 _seq_no/_primary_term 提取（对齐 Hits）。
func TestUnmarshalHitsScores(t *testing.T) {
	hits := []json.RawMessage{
		json.RawMessage(`{"_id":"1","_score":3.5,"_seq_no":10,"_primary_term":1,"_source":{"id":"1","name":"alpha","price":9.9}}`),
		json.RawMessage(`{"_id":"2","_score":1.2,"_seq_no":11,"_primary_term":2,"_source":{"id":"2","name":"beta","price":4.0}}`),
	}
	docs, scores, seqNos, primaryTerms, err := unmarshalHits[product](hits)
	if err != nil {
		t.Fatalf("unmarshalHits 失败: %v", err)
	}
	if len(docs) != 2 || len(scores) != 2 || len(seqNos) != 2 || len(primaryTerms) != 2 {
		t.Fatalf("期望各 2 个，实际 docs=%d scores=%d seqNos=%d pts=%d",
			len(docs), len(scores), len(seqNos), len(primaryTerms))
	}
	if docs[0].ID != "1" || docs[1].ID != "2" {
		t.Fatalf("期望 _id 回填，实际 %q / %q", docs[0].ID, docs[1].ID)
	}
	if scores[0] != 3.5 || scores[1] != 1.2 {
		t.Fatalf("期望得分 [3.5,1.2]，实际 %v", scores)
	}
	if seqNos[0] != 10 || seqNos[1] != 11 || primaryTerms[0] != 1 || primaryTerms[1] != 2 {
		t.Fatalf("期望 seqNos[10,11] primaryTerms[1,2]，实际 %v / %v", seqNos, primaryTerms)
	}
}

// TestSearchResultHitMeta 验证 HitMeta 按索引对齐返回 _id/_seq_no/_primary_term。
func TestSearchResultHitMeta(t *testing.T) {
	res := &SearchResult[product]{
		Hits:        []product{{ID: "1"}, {ID: "2"}},
		SeqNos:      []int64{10, 11},
		PrimaryTerms: []int64{1, 2},
	}
	m0 := res.HitMeta(0)
	if m0.ID != "1" || m0.SeqNo != 10 || m0.PrimaryTerm != 1 {
		t.Fatalf("HitMeta(0) 不符: %+v", m0)
	}
	if res.HitMeta(99).ID != "" {
		t.Fatalf("越界应返回零值")
	}
}
