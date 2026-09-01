package gen

import "testing"

func TestParseJSONSample_ES(t *testing.T) {
	src := `{"title":"x","price":9.9,"in_stock":true,"embedding":[0.1,0.2],"created_at":"2024-01-15","address":{"city":"sz","zip":"518000"}}`
	structs, err := ParseJSONSample(src)
	if err != nil {
		t.Fatalf("ParseJSONSample: %v", err)
	}
	if len(structs) != 1 {
		t.Fatalf("want 1 struct, got %d", len(structs))
	}
	s := structs[0]
	fields := map[string]ESField{}
	for _, f := range s.Fields {
		fields[f.GoName] = f
	}
	cases := map[string]string{
		"Title":     "string",
		"Price":     "float64",
		"InStock":   "bool",
		"Embedding": "[]float64",
		"CreatedAt": "time.Time",
		"Address":   "Address",
	}
	for k, v := range cases {
		if fields[k].GoType != v {
			t.Errorf("field %s = %q, want %q", k, fields[k].GoType, v)
		}
	}
	if !fields["Address"].IsNested {
		t.Errorf("Address should be flagged nested")
	}
	if !s.HasTime {
		t.Errorf("HasTime should be true for time.Time field")
	}
	if len(s.Nested) != 1 || s.Nested[0].Name != "Address" {
		t.Errorf("want nested Address struct, got %+v", s.Nested)
	}

	// 数组顶层取首个元素
	arr := `[{"id":2,"name":"y"}]`
	structs, err = ParseJSONSample(arr)
	if err != nil {
		t.Fatalf("ParseJSONSample(array): %v", err)
	}
	if len(structs) != 1 || structs[0].Fields[0].GoName != "ID" {
		t.Errorf("array sample should pick first element, got %+v", structs)
	}
}
