package gen

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ParseJSONSample 从 JSON 文档样例推断 ES 文档结构（支持嵌套对象与数组）。
// 顶层若为数组，取第一个元素作为文档样本。
func ParseJSONSample(jsonStr string) ([]ESStruct, error) {
	var raw any
	if err := json.Unmarshal([]byte(jsonStr), &raw); err != nil {
		if se, ok := err.(*json.SyntaxError); ok {
			line := strings.Count(jsonStr[:se.Offset], "\n") + 1
			return nil, fmt.Errorf("esgen: 解析 JSON 样例第 %d 行: %s", line, err.Error())
		}
		return nil, fmt.Errorf("esgen: 解析 JSON 样例: %w", err)
	}
	if arr, ok := raw.([]any); ok {
		if len(arr) == 0 {
			return nil, fmt.Errorf("esgen: JSON 数组为空，无法推断结构")
		}
		raw = arr[0]
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("esgen: JSON 样例顶层需为对象（或对象数组）")
	}
	s := ESStruct{Name: "Doc", IndexName: "docs"}
	for k, v := range obj {
		s.Fields = append(s.Fields, inferESField(k, v, &s.Nested))
	}
	sortStruct(&s)
	s.HasTime = hasTime(s)
	return []ESStruct{s}, nil
}

func inferESField(jsonName string, val any, nested *[]ESStruct) ESField {
	f := ESField{JsonName: jsonName, GoName: toGoName(jsonName)}
	switch v := val.(type) {
	case map[string]any:
		ns := ESStruct{Name: f.GoName, IndexName: toSnake(f.GoName)}
		for k, sv := range v {
			ns.Fields = append(ns.Fields, inferESField(k, sv, &ns.Nested))
		}
		sortStruct(&ns)
		ns.HasTime = hasTime(ns)
		*nested = append(*nested, ns)
		f.GoType = f.GoName
		f.IsNested = true
		return f
	case []any:
		elem := inferESElem(v, nested, f.GoName)
		f.GoType = "[]" + elem
		return f
	case float64:
		if v == float64(int64(v)) && !strings.ContainsAny(fmt.Sprintf("%v", v), ".eE") {
			f.GoType = "int64"
		} else {
			f.GoType = "float64"
		}
		return f
	case bool:
		f.GoType = "bool"
		return f
	case string:
		if isTimeStr(v) {
			f.GoType = "time.Time"
			f.HasTime = true
		} else {
			f.GoType = "string"
		}
		return f
	case nil:
		f.GoType = "any"
		return f
	default:
		f.GoType = "string"
		return f
	}
}

func inferESElem(arr []any, nested *[]ESStruct, parentName string) string {
	if len(arr) == 0 {
		return "any"
	}
	switch fv := arr[0].(type) {
	case map[string]any:
		ns := ESStruct{Name: parentName, IndexName: toSnake(parentName)}
		for k, sv := range fv {
			ns.Fields = append(ns.Fields, inferESField(k, sv, &ns.Nested))
		}
		sortStruct(&ns)
		ns.HasTime = hasTime(ns)
		*nested = append(*nested, ns)
		return parentName
	case float64:
		return "float64"
	case bool:
		return "bool"
	case string:
		return "string"
	default:
		return "any"
	}
}

func isTimeStr(s string) bool {
	if _, err := time.Parse(time.RFC3339, s); err == nil {
		return true
	}
	if _, err := time.Parse("2006-01-02", s); err == nil {
		return true
	}
	return false
}

// FromJSON 从 JSON 文档样例生成文件内容，复用 mapping 的渲染管线。
func FromJSON(jsonStr string, opts Options) (map[string]string, error) {
	structs, err := ParseJSONSample(jsonStr)
	if err != nil {
		return nil, err
	}
	if len(structs) == 0 {
		return nil, fmt.Errorf("esgen: 未能从 JSON 样例推断出任何结构")
	}
	return assembleStructs(structs, opts)
}
