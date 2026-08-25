package es

import (
	"reflect"
	"strings"
	"sync"
	"time"
)

// fieldInfo 文档模型字段元数据。
type fieldInfo struct {
	goName string       // Go 字段名
	name   string       // ES 字段名（json/es tag 或蛇形命名）
	typ    reflect.Type // Go 字段类型，用于 mapping 推断
	id     bool         // 是否为文档 _id 来源字段（es tag 含 "id"）
	ignore bool         // 是否忽略（json:"-" 或 es:"-"）
	esType string       // 显式指定的 ES 类型（es tag 含 "type:keyword" 等），为空则自动推断
}

// modelMeta 文档模型的元数据（字段、索引名、id 字段）。
type modelMeta struct {
	index         string
	explicitIndex bool
	fields        []fieldInfo
	idField       *fieldInfo
}

var metaCache sync.Map

type indexNamer interface{ IndexName() string }

// getMeta 解析类型 T 的模型元数据（带缓存）。
func getMeta[T any]() *modelMeta {
	typ := reflect.TypeOf((*T)(nil)).Elem()
	if v, ok := metaCache.Load(typ); ok {
		return v.(*modelMeta)
	}
	m := parseMeta(typ)
	metaCache.Store(typ, m)
	return m
}

func parseMeta(typ reflect.Type) *modelMeta {
	idx, explicit := resolveIndex(typ)
	m := &modelMeta{index: idx, explicitIndex: explicit}
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.PkgPath != "" { // 非导出字段
			continue
		}
		// 忽略判定：json:"-" 或 es:"-"
		jsonTag := f.Tag.Get("json")
		esTag := f.Tag.Get("es")
		if cut(jsonTag) == "-" || cut(esTag) == "-" {
			m.fields = append(m.fields, fieldInfo{goName: f.Name, ignore: true})
			continue
		}
		fi := fieldInfo{goName: f.Name, name: fieldName(f), typ: f.Type}
		for _, opt := range strings.Split(esTag, ",") {
			opt = strings.TrimSpace(opt)
			switch {
			case opt == "id":
				fi.id = true
				m.idField = &fi
			case strings.HasPrefix(opt, "type:"):
				fi.esType = strings.TrimPrefix(opt, "type:")
			}
		}
		m.fields = append(m.fields, fi)
	}
	if m.idField == nil { // 兼容常见命名
		for i := range m.fields {
			if m.fields[i].goName == "ID" || m.fields[i].goName == "Id" {
				m.fields[i].id = true
				m.idField = &m.fields[i]
				break
			}
		}
	}
	return m
}

// resolveIndex 返回索引名，以及是否来自 IndexName() 显式指定。
func resolveIndex(typ reflect.Type) (string, bool) {
	v := reflect.New(typ)
	if in, ok := v.Interface().(indexNamer); ok {
		return in.IndexName(), true
	}
	if in, ok := v.Elem().Interface().(indexNamer); ok {
		return in.IndexName(), true
	}
	return plural(toSnake(typ.Name())), false
}

// cut 取 tag 第一段（去掉 ",omitempty" 等选项）。
func cut(tag string) string {
	if i := strings.Index(tag, ","); i >= 0 {
		return tag[:i]
	}
	return tag
}

// BuildMapping 根据模型 T 推导 ES 索引 mapping（properties 部分）。
// 可在 type 上通过 es:"type:keyword" 等覆盖推断类型。
// shards/replicas 用于 settings；传 0 表示沿用集群默认。
func BuildMapping[T any](shards, replicas int) map[string]any {
	m := getMeta[T]()
	props := map[string]any{}
	for i := range m.fields {
		f := m.fields[i]
		if f.ignore {
			continue
		}
		props[f.name] = mappingFor(f)
	}
	mapping := map[string]any{
		"mappings": map[string]any{"properties": props},
	}
	if shards > 0 || replicas > 0 {
		settings := map[string]any{}
		if shards > 0 {
			settings["number_of_shards"] = shards
		}
		if replicas > 0 {
			settings["number_of_replicas"] = replicas
		}
		mapping["settings"] = settings
	}
	return mapping
}

// mappingFor 为单个字段推断 mapping。
func mappingFor(f fieldInfo) map[string]any {
	if f.esType != "" {
		return map[string]any{"type": f.esType}
	}
	if f.typ == nil {
		return map[string]any{"type": "object"}
	}
	return inferMapping(f.typ)
}

// inferMapping 根据 Go 类型递归推断 ES mapping。
func inferMapping(t reflect.Type) map[string]any {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.String:
		// text + keyword 子字段是 ES 最常见组合：全文检索用 text，精确聚合/排序用 .keyword
		return map[string]any{
			"type":   "text",
			"fields": map[string]any{"keyword": map[string]any{"type": "keyword"}},
		}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32:
		return map[string]any{"type": "integer"}
	case reflect.Int64:
		return map[string]any{"type": "long"}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32:
		return map[string]any{"type": "integer"}
	case reflect.Uint64:
		return map[string]any{"type": "long"}
	case reflect.Float32:
		return map[string]any{"type": "float"}
	case reflect.Float64:
		return map[string]any{"type": "double"}
	case reflect.Slice, reflect.Array:
		elem := t.Elem()
		if elem.Kind() == reflect.Struct {
			// 结构体切片 → nested，递归其 properties
			return map[string]any{"type": "nested", "properties": nestedProps(elem)}
		}
		if elem.Kind() == reflect.Ptr {
			elem = elem.Elem()
		}
		return inferMapping(elem)
	case reflect.Map:
		return map[string]any{"type": "object"}
	case reflect.Struct:
		if t == reflect.TypeOf(time.Time{}) {
			return map[string]any{"type": "date"}
		}
		return map[string]any{"type": "object", "properties": nestedProps(t)}
	default:
		return map[string]any{"type": "object"}
	}
}

func nestedProps(t reflect.Type) map[string]any {
	props := map[string]any{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue
		}
		if cut(f.Tag.Get("json")) == "-" || cut(f.Tag.Get("es")) == "-" {
			continue
		}
		props[fieldName(f)] = inferMapping(f.Type)
	}
	return props
}
