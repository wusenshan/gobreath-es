package es

import (
	"encoding/json"
	"testing"
)

// TestUnmarshalHitsScores 验证 _id 回填与 _score 提取（对齐 Hits）。
func TestUnmarshalHitsScores(t *testing.T) {
	hits := []json.RawMessage{
		json.RawMessage(`{"_id":"1","_score":3.5,"_source":{"id":"1","name":"alpha","price":9.9}}`),
		json.RawMessage(`{"_id":"2","_score":1.2,"_source":{"id":"2","name":"beta","price":4.0}}`),
	}
	docs, scores, err := unmarshalHits[product](hits)
	if err != nil {
		t.Fatalf("unmarshalHits 失败: %v", err)
	}
	if len(docs) != 2 || len(scores) != 2 {
		t.Fatalf("期望 2 条文档与得分，实际 %d / %d", len(docs), len(scores))
	}
	if docs[0].ID != "1" || docs[1].ID != "2" {
		t.Fatalf("期望 _id 回填，实际 %q / %q", docs[0].ID, docs[1].ID)
	}
	if scores[0] != 3.5 || scores[1] != 1.2 {
		t.Fatalf("期望得分 [3.5,1.2]，实际 %v", scores)
	}
}
