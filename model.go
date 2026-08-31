package es

import (
	"reflect"
	"strconv"
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
	vector bool         // 是否为向量字段（es tag 含 "vector(N)"）
	vectorDim int       // 向量维度（es:"vector(N)" 中的 N；0 表示未显式声明，兜底 1536）
	vectorSim string    // 向量相似度（kNN 必需）：cosine / l2 / dot / max_inner；空则默认 cosine
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
		case strings.HasPrefix(opt, "vector"):
			fi.vector = true
			if n := parseVectorDim(opt); n > 0 {
				fi.vectorDim = n
			}
		case strings.HasPrefix(opt, "sim:"):
			fi.vectorSim = strings.TrimPrefix(opt, "sim:")
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

// TemplateOptions 组合式索引模板（_index_template）的可选项。
type TemplateOptions struct {
	Patterns   []string       // index_patterns，必填，如 []string{"logs-*"}
	Priority   int            // 优先级：覆盖同 pattern 的其他模板；<=0 时默认 100
	Shards     int            // 主分片数；0 沿用集群默认
	Replicas   int            // 副本数；0 沿用集群默认
	ComposedOf []string       // 组件模板名列表（可选，引用既有 component template）
	Version    int            // 模板版本（可选）
	Meta       map[string]any // _meta 自定义元数据（可选）
	Aliases    map[string]any // 别名定义（可选）
}

// mappingAndSettings 根据模型 T 推导 mappings（properties）与可选 settings。
func mappingAndSettings[T any](shards, replicas int) (map[string]any, map[string]any) {
	m := getMeta[T]()
	props := map[string]any{}
	for i := range m.fields {
		f := m.fields[i]
		if f.ignore {
			continue
		}
		props[f.name] = mappingFor(f)
	}
	mappings := map[string]any{"properties": props}
	var settings map[string]any
	if shards > 0 || replicas > 0 {
		settings = map[string]any{}
		if shards > 0 {
			settings["number_of_shards"] = shards
		}
		if replicas > 0 {
			settings["number_of_replicas"] = replicas
		}
	}
	return mappings, settings
}

// BuildMapping 根据模型 T 推导 ES 索引 mapping（含可选 settings）。
// 可在 type 上通过 es:"type:keyword" 等覆盖推断类型。
// shards/replicas 用于 settings；传 0 表示沿用集群默认。
func BuildMapping[T any](shards, replicas int) map[string]any {
	mappings, settings := mappingAndSettings[T](shards, replicas)
	m := map[string]any{"mappings": mappings}
	if settings != nil {
		m["settings"] = settings
	}
	return m
}

// BuildIndexTemplate 根据模型 T 推导组合式索引模板（_index_template）的请求体。
// 调用方把返回值传给 Client.PutIndexTemplate / Repo.PutIndexTemplate 即可。
// 模板会让所有匹配 index_patterns 的新索引自动套用本模型的 mapping/settings。
func BuildIndexTemplate[T any](opts TemplateOptions) map[string]any {
	mappings, settings := mappingAndSettings[T](opts.Shards, opts.Replicas)
	tmpl := map[string]any{"mappings": mappings}
	if settings != nil {
		tmpl["settings"] = settings
	}
	if opts.Aliases != nil {
		tmpl["aliases"] = opts.Aliases
	}
	priority := opts.Priority
	if priority <= 0 {
		priority = 100
	}
	body := map[string]any{
		"index_patterns": opts.Patterns,
		"priority":       priority,
		"template":       tmpl,
	}
	if len(opts.ComposedOf) > 0 {
		body["composed_of"] = opts.ComposedOf
	}
	if opts.Version > 0 {
		body["version"] = opts.Version
	}
	if opts.Meta != nil {
		body["_meta"] = opts.Meta
	}
	return body
}

// mappingFor 为单个字段推断 mapping。
func mappingFor(f fieldInfo) map[string]any {
	// 向量字段：dense_vector 做 kNN 检索必须在 mapping 里声明 similarity + index。
	// 否则运行期 kNN 会报错（Field [...] doesn't support kNN search because it
	// doesn't have a [similarity]）；默认 cosine，可用 es:"sim:l2" 等覆盖。
	if f.vector {
		dim := f.vectorDim
		if dim == 0 {
			dim = 1536 // 未显式声明维度时的兜底；建议用 es:"vector(N)" 显式指定以对齐模型维度
		}
		sim := f.vectorSim
		if sim == "" {
			sim = "cosine"
		}
		return map[string]any{
			"type":      "dense_vector",
			"dims":      dim,
			"index":     true,
			"similarity": sim,
		}
	}
	if f.esType != "" {
		return map[string]any{"type": f.esType}
	}
	if f.typ == nil {
		return map[string]any{"type": "object"}
	}
	return inferMapping(f.typ)
}

// parseVectorDim 从 "vector(1536)" / "vector(384)" 中解析维度 N；无括号或非数字返回 0。
func parseVectorDim(seg string) int {
	// seg 形如 "vector(1536)"
	if !strings.HasSuffix(seg, ")") {
		return 0
	}
	open := strings.Index(seg, "(")
	if open < 0 {
		return 0
	}
	inner := seg[open+1 : len(seg)-1]
	n, err := strconv.Atoi(strings.TrimSpace(inner))
	if err != nil || n <= 0 {
		return 0
	}
	return n
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
