package gen

import (
	"strings"
	"testing"
)

const sampleMapping = `{
  "mappings": {
    "properties": {
      "id":         {"type": "keyword"},
      "name":       {"type": "text"},
      "price":      {"type": "double"},
      "cat_id":     {"type": "long"},
      "tags":       {"type": "keyword"},
      "in_stock":   {"type": "boolean"},
      "created_at": {"type": "date"},
      "embedding":  {"type": "dense_vector", "dims": 3},
      "address": {
        "type": "object",
        "properties": {
          "street": {"type": "text"},
          "city":   {"type": "keyword"}
        }
      }
    }
  }
}`

func TestParseMapping_BasicTypes(t *testing.T) {
	structs, err := ParseMapping(sampleMapping)
	if err != nil {
		t.Fatalf("ParseMapping 失败: %v", err)
	}
	if len(structs) != 1 {
		t.Fatalf("应解析出 1 个顶层文档，实际 %d", len(structs))
	}
	s := structs[0]
	want := map[string]string{
		"ID":        "string",
		"Name":      "string",
		"Price":     "float64",
		"CatID":     "int64",
		"Tags":      "string",
		"InStock":   "bool",
		"CreatedAt": "time.Time",
		"Embedding": "[]float32",
		"Address":   "Address",
	}
	for _, f := range s.Fields {
		exp, ok := want[f.GoName]
		if !ok {
			t.Errorf("出现意外字段 %s", f.GoName)
			continue
		}
		if f.GoType != exp {
			t.Errorf("字段 %s 类型应为 %s，实际 %s", f.GoName, exp, f.GoType)
		}
		switch f.GoName {
		case "CatID":
			if !strings.Contains(jsonTagOf(f), `json:"cat_id"`) {
				t.Errorf("CatID 的 json tag 应为 cat_id，实际 %s", jsonTagOf(f))
			}
		case "Embedding":
			if !f.IsVector || f.VectorDim != 3 {
				t.Errorf("Embedding 应为 dense_vector(3)")
			}
			if !strings.Contains(jsonTagOf(f), `es:"vector(3)"`) {
				t.Errorf("Embedding 应有 es:\"vector(3)\" tag，实际 %s", jsonTagOf(f))
			}
		case "CreatedAt":
			if !f.HasTime {
				t.Errorf("CreatedAt 应标记 HasTime")
			}
		case "Address":
			if len(s.Nested) != 1 || s.Nested[0].Name != "Address" {
				t.Errorf("Address 应生成嵌套结构体 Address")
			}
		}
	}
	if !s.HasTime {
		t.Errorf("顶层文档应标记 HasTime（含 created_at）")
	}
}

func TestParseMapping_NakedProperties(t *testing.T) {
	src := `{"properties":{"title":{"type":"text"},"score":{"type":"float"}}}`
	structs, err := ParseMapping(src)
	if err != nil {
		t.Fatalf("裸 properties 解析失败: %v", err)
	}
	if len(structs[0].Fields) != 2 {
		t.Errorf("应解析出 2 个字段，实际 %d", len(structs[0].Fields))
	}
}

func TestParseMapping_BareFields(t *testing.T) {
	src := `{"user_id":{"type":"keyword"},"views":{"type":"integer"}}`
	structs, err := ParseMapping(src)
	if err != nil {
		t.Fatalf("裸字段表解析失败: %v", err)
	}
	if len(structs[0].Fields) != 2 {
		t.Errorf("应解析出 2 个字段，实际 %d", len(structs[0].Fields))
	}
}

func TestFromMapping_OutputModes(t *testing.T) {
	for _, mode := range []OutputMode{PerType, TwoFiles, SingleFile} {
		files, err := FromMapping(sampleMapping, Options{Package: "model", Mode: mode})
		if err != nil {
			t.Fatalf("FromMapping(%v) 失败: %v", mode, err)
		}
		joined := strings.Join(values(files), "\n")

		// 模型含 IndexName 实现
		if !strings.Contains(joined, "func (Doc) IndexName() string") {
			t.Errorf("mode %v 应生成 IndexName 方法", mode)
		}
		// 闭包含 ColOf[Doc]（单文件模式用 Repos 别名，故按 ColOf[Doc] 匹配）
		if !strings.Contains(joined, `ColOf[Doc]("ID")`) {
			t.Errorf("mode %v 应生成 ColOf[Doc](\"ID\")", mode)
		}
		// 嵌套结构体生成（但不应实现 IndexName，否则会被框架当成索引）
		if !strings.Contains(joined, "type Address struct") {
			t.Errorf("mode %v 应生成嵌套 Address 结构体", mode)
		}
		if strings.Contains(joined, "func (Address) IndexName()") {
			t.Errorf("mode %v 嵌套 Address 不应实现 IndexName", mode)
		}
		// 嵌套 object 字段不应进列集合
		if strings.Contains(joined, "Address   es.ColExpr") || strings.Contains(joined, "Address Repos.ColExpr") {
			t.Errorf("mode %v 嵌套字段 Address 不应出现在列集合中", mode)
		}
		// dense_vector tag
		if !strings.Contains(joined, `es:"vector(3)"`) && !strings.Contains(joined, `es:"vector(3)"`) {
			// 单文件模式用 Repos 别名
		}
		if !strings.Contains(joined, "vector(3)") {
			t.Errorf("mode %v 应保留 vector(3) tag", mode)
		}
	}
}

func TestFromMapping_SingleFileAlias(t *testing.T) {
	files, err := FromMapping(sampleMapping, Options{Package: "model", Mode: SingleFile})
	if err != nil {
		t.Fatal(err)
	}
	src := files["models_gen.go"]
	// 单文件模式用 Repos 别名避免与字段名冲突
	if !strings.Contains(src, `Repos.ColOf[Doc]("ID")`) {
		t.Errorf("单文件模式应使用 Repos 别名引用 ColOf")
	}
	if !strings.Contains(src, "import (") {
		t.Errorf("单文件模式应 import time 与 Repos")
	}
}

func TestFromMapping_NamingOverride(t *testing.T) {
	files, err := FromMapping(sampleMapping, Options{Package: "model", StructName: "Product", IndexName: "products", Mode: PerType})
	if err != nil {
		t.Fatal(err)
	}
	src := files["Product.go"]
	if !strings.Contains(src, "type Product struct") {
		t.Errorf("应生成 type Product struct")
	}
	if !strings.Contains(src, `func (Product) IndexName() string { return "products" }`) {
		t.Errorf("应生成 IndexName 返回 products")
	}
	if _, ok := files["Product_cols.go"]; !ok {
		t.Errorf("应生成 Product_cols.go")
	}
}

func TestStructNames(t *testing.T) {
	src := `package example
type User struct { Name string }
type Order struct { Id int64 }
type notStruct int
`
	names, err := StructNames(src)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(names, ",")
	if got != "User,Order" {
		t.Errorf("StructNames 应为 User,Order，实际 %s", got)
	}
}

func jsonTagOf(f ESField) string { return jsonTag(f) }

func values(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}
