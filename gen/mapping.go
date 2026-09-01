// Package gen 为 gobreath-es 模型提供字段名代码生成能力。
//
// 除给已有结构体补字段闭包（Generate）外，本文件额外提供「从 ES mapping 生成模型」能力：
// 粘贴一份索引 mapping JSON（Get Mapping API 的返回，或裸 properties 片段），即可生成带
// json tag 的 Go 结构体（实现 IndexName()）与 ColOf 字段闭包文件。这与 ormgen 的
// 「DDL → 模型」互为镜像——ES 没有建表语句，mapping 就是它的「schema」。
//
// 生成器零依赖、纯文本解析：绝不连 ES、绝不执行上传内容；输出不保证可直接编译，
// 命名 / 包冲突由开发自行处理。
package gen

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"sort"
	"strings"
)

// OutputMode 控制 mapping 生成产物如何分文件。
type OutputMode int

const (
	// PerType 每类一个模型文件 + 一个闭包文件（默认）：Doc.go + Doc_cols.go
	PerType OutputMode = iota
	// TwoFiles 合并为 models.go（所有结构体）+ columns.go（所有闭包）
	TwoFiles
	// SingleFile 合并为 models_gen.go（结构体与闭包同文件）
	SingleFile
)

// Options 控制 mapping 生成行为。
type Options struct {
	Package    string // 生成代码的包名，默认 model
	IndexName  string // 索引名；为空则用 plural(toSnake(StructName)) 兜底
	StructName string // 顶层文档结构体名；为空则默认 Doc
	Mode       OutputMode
	Example    bool // 是否附带「生成物即所用」示例代码（example.go）；默认 false
	Repo       bool // 是否附带 Repo[T] 便捷构造脚手架（<struct>_repo.go）；默认 false
}

// ESField 是一个 ES 字段解析后的目标形态。
type ESField struct {
	GoName   string // PascalCase 的 Go 字段名
	JsonName string // ES 文档里的 JSON key
	GoType   string
	IsVector bool   // dense_vector
	VectorDim int   // dense_vector 的 dims
	HasTime  bool   // time.Time
	IsNested bool   // 嵌套 object 生成的子结构（不进列集合、不实现 IndexName）
}

// ESStruct 解析出的一个文档结构（含嵌套子结构）。
type ESStruct struct {
	Name      string     // Go 结构体名（顶层文档）
	IndexName string     // 索引名
	Fields    []ESField  // 顶层字段
	Nested    []ESStruct // 嵌套 object 生成的子结构体
	HasTime   bool
}

// ParseMapping 从 ES mapping JSON 解析出顶层文档结构（通常一个）。
//
// 兼容三种输入形态：
//   - Get Mapping 响应：{"mappings":{"properties":{...}}}
//   - 裸 properties：{"properties":{...}}
//   - 直接字段表：{"field":{"type":"keyword"}, ...}
func ParseMapping(jsonStr string) ([]ESStruct, error) {
	var raw map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &raw); err != nil {
		if se, ok := err.(*json.SyntaxError); ok {
			line := strings.Count(jsonStr[:se.Offset], "\n") + 1
			return nil, fmt.Errorf("esgen: 解析 mapping JSON 第 %d 行: %s", line, err.Error())
		}
		return nil, fmt.Errorf("esgen: 解析 mapping JSON: %w", err)
	}
	props := extractProperties(raw)
	if len(props) == 0 {
		return nil, fmt.Errorf("esgen: mapping 中未找到任何字段（需含 properties 或字段表）")
	}
	s := ESStruct{Name: "Doc", IndexName: "docs"}
	for k, v := range props {
		s.Fields = append(s.Fields, parseField(k, v, &s.Nested))
	}
	sortStruct(&s)
	s.HasTime = hasTime(s)
	return []ESStruct{s}, nil
}

// extractProperties 从各种形态里取出字段表。
func extractProperties(m map[string]any) map[string]any {
	if mp, ok := m["mappings"].(map[string]any); ok {
		if p, ok := mp["properties"].(map[string]any); ok {
			return p
		}
		// mappings 里可能直接是字段表（极少见）
		return mp
	}
	if p, ok := m["properties"].(map[string]any); ok {
		return p
	}
	// 直接是字段表：要求至少有一个值是带 type/properties 的对象，避免误把任意 JSON 当 mapping
	for _, v := range m {
		if obj, ok := v.(map[string]any); ok {
			if _, hasType := obj["type"]; hasType {
				return m
			}
			if _, hasProps := obj["properties"]; hasProps {
				return m
			}
		}
	}
	return nil
}

// parseField 递归解析单个字段。nested 收集嵌套 object 生成的子结构。
func parseField(jsonName string, val any, nested *[]ESStruct) ESField {
	f := ESField{JsonName: jsonName, GoName: toGoName(jsonName)}
	obj, ok := val.(map[string]any)
	if !ok {
		f.GoType = "string"
		return f
	}
	// 嵌套 object / nested：递归生成子结构体
	if sub, ok := obj["properties"].(map[string]any); ok && len(sub) > 0 {
		ns := ESStruct{Name: f.GoName, IndexName: toSnake(f.GoName)}
		for k, v := range sub {
			ns.Fields = append(ns.Fields, parseField(k, v, &ns.Nested))
		}
		sortStruct(&ns)
		ns.HasTime = hasTime(ns)
		*nested = append(*nested, ns)
		f.GoType = f.GoName
		f.IsNested = true
		return f
	}
	// 取类型（type 可能写成字符串或数组，如 ["text","keyword"]）
	typ := ""
	switch t := obj["type"].(type) {
	case string:
		typ = t
	case []any:
		if len(t) > 0 {
			if s, ok := t[0].(string); ok {
				typ = s
			}
		}
	}
	if isNestedType(typ) {
		// object / nested / flattened 无显式 properties：退化为通用 map
		f.GoType = "map[string]any"
		return f
	}
	gt, isVec, dim, hasTime := mapESType(typ, obj)
	f.GoType = gt
	f.IsVector = isVec
	f.VectorDim = dim
	f.HasTime = hasTime
	if f.GoType == "" {
		f.GoType = "string"
	}
	return f
}

// isNestedType 判断是否为需要递归展开的类型（仅当无 properties 时退化为 map）。
func isNestedType(typ string) bool {
	switch strings.ToLower(typ) {
	case "object", "nested", "flattened":
		return true
	}
	return false
}

// mapESType 把 ES 类型映射为 Go 类型。
func mapESType(typ string, obj map[string]any) (goType string, isVector bool, dim int, hasTime bool) {
	switch strings.ToLower(typ) {
	case "keyword", "text", "wildcard", "constant_keyword", "ip", "version",
		"id", "match_only_text", "search_as_you_type", "completion", "token_count",
		"percolator", "murmur3", "annotated_text", "binary", "alias", "icu":
		return "string", false, 0, false
	case "long":
		return "int64", false, 0, false
	case "integer":
		return "int32", false, 0, false
	case "short":
		return "int16", false, 0, false
	case "byte":
		return "int8", false, 0, false
	case "unsigned_long":
		return "uint64", false, 0, false
	case "float", "half_float":
		return "float32", false, 0, false
	case "double", "scaled_float":
		return "float64", false, 0, false
	case "boolean":
		return "bool", false, 0, false
	case "date", "date_nanos":
		return "time.Time", false, 0, true
	case "dense_vector":
		d := 0
		if dv, ok := obj["dims"].(float64); ok {
			d = int(dv)
		}
		return "[]float32", true, d, false
	case "geo_point", "geo_shape", "integer_range", "float_range", "long_range",
		"double_range", "date_range", "histogram":
		return "map[string]any", false, 0, false
	default:
		// 未知类型按字符串兜底，避免生成不可编译代码
		return "string", false, 0, false
	}
}

// FromMapping 从 mapping JSON 生成文件内容，返回 filename→源码。
func FromMapping(jsonStr string, opts Options) (map[string]string, error) {
	structs, err := ParseMapping(jsonStr)
	if err != nil {
		return nil, err
	}
	return assembleStructs(structs, opts)
}

// ---- 「生成物即所用」：示例代码 + Repo 脚手架 ----

// insertLitES 返回字段在示例写入语句里的字面量。
func insertLitES(f ESField) string {
	switch {
	case f.IsVector:
		return "[]float32{ /* 维度需与 dense_vector dims 一致，通常来自 embedding 模型 */ }"
	case f.GoType == "string":
		return `"example"`
	case f.GoType == "bool":
		return "false"
	case strings.HasPrefix(f.GoType, "int"), strings.HasPrefix(f.GoType, "uint"):
		return "1"
	case strings.HasPrefix(f.GoType, "float"):
		return "1.0"
	case f.GoType == "time.Time":
		return "time.Now()"
	case f.GoType == "map[string]any":
		return "map[string]any{}"
	case f.GoType == "any":
		return "nil"
	default:
		return `"example"`
	}
}

// queryLitES 返回检索示例里可用的字面量与是否可用。
func queryLitES(f ESField) (string, bool) {
	switch {
	case f.IsVector, f.IsNested:
		return "", false
	case f.GoType == "string":
		return `"example"`, true
	case f.GoType == "bool":
		return "true", true
	case strings.HasPrefix(f.GoType, "int"), strings.HasPrefix(f.GoType, "uint"):
		return "1", true
	case strings.HasPrefix(f.GoType, "float"):
		return "1.0", true
	case f.GoType == "time.Time":
		return "time.Now()", true
	default:
		return "", false
	}
}

// firstSimpleFieldES 选一个适合做检索示例的标量字段（非向量、非嵌套、有可用字面量）。
func firstSimpleFieldES(s ESStruct) (ESField, bool) {
	for _, f := range s.Fields {
		if _, ok := queryLitES(f); ok {
			return f, true
		}
	}
	return ESField{}, false
}

// generateExampleES 生成「生成物即所用」示例文件：写入 / 检索 / 向量 kNN。
func generateExampleES(s ESStruct, pkg string) (string, error) {
	hasTime := s.HasTime
	var b strings.Builder
	b.WriteString("// Code generated by gobreath-es/cmd/esgen. DO NOT EDIT.\n")
	b.WriteString("// 本文件为「生成物即所用」示例代码：演示如何用生成的模型与字段闭包完成常见操作。\n")
	b.WriteString("// 复制到你自己代码里按需修改即可；生产代码请勿放在生成目录以免被覆盖。\n\n")
	b.WriteString("package " + pkg + "\n\n")
	b.WriteString("import (\n\t\"context\"\n")
	if hasTime {
		b.WriteString("\t\"time\"\n")
	}
	b.WriteString("\n\tes \"github.com/wusenshan/gobreath-es\"\n)\n\n")
	b.WriteString("// 把下面的 exampleClient 替换成你自己的 *es.Client（如 es.NewClient(es.WithAddresses(\"http://localhost:9200\"))）。\n")
	b.WriteString("// 此处仅作示例占位，避免为生成代码引入具体 ES 客户端依赖。\n")
	b.WriteString("var exampleClient *es.Client\n\n")

	S := s.Name
	cols := S + "Cols"

	// 1) 写入
	b.WriteString("// 1) 写入文档（Index / Insert 等价；自动取 id 字段作为 _id）。\n")
	b.WriteString("func ExampleIndex(ctx context.Context) error {\n")
	b.WriteString("\tdoc := " + S + "{\n")
	for _, f := range s.Fields {
		if f.IsNested {
			continue // 嵌套对象在示例里留零值
		}
		b.WriteString("\t\t" + f.GoName + ": " + insertLitES(f) + ",\n")
	}
	b.WriteString("\t}\n")
	b.WriteString("\trepo := es.NewRepo[" + S + "](exampleClient)\n")
	b.WriteString("\treturn repo.Index(ctx, doc)\n")
	b.WriteString("}\n\n")

	fld, ok := firstSimpleFieldES(s)
	if ok {
		lit, _ := queryLitES(fld)
		// 2) 检索
		b.WriteString("// 2) 检索（用生成的字段闭包代替手写字段名，重构不怕拼错）。\n")
		b.WriteString("func ExampleSearch(ctx context.Context) (*es.SearchResult[" + S + "], error) {\n")
		b.WriteString("\tq := es.NewQuery[" + S + "]().\n")
		b.WriteString("\t\tEq(" + cols + "." + fld.GoName + ", " + lit + ").\n")
		b.WriteString("\t\tLimit(10)\n")
		b.WriteString("\trepo := es.NewRepo[" + S + "](exampleClient)\n")
		b.WriteString("\treturn repo.Search(ctx, q)\n")
		b.WriteString("}\n\n")
	}

	// 3) 向量 kNN
	for _, f := range s.Fields {
		if f.IsVector {
			b.WriteString("// 3) 向量 kNN 近邻检索（AI / RAG 场景；文档含 dense_vector 字段 " + f.GoName + "）。\n")
			b.WriteString("func ExampleKNN(ctx context.Context) (*es.SearchResult[" + S + "], error) {\n")
			b.WriteString("\tvec := []float32{ /* 与 " + f.GoName + " 同维度的向量，通常来自 embedding 模型（如千问 text-embedding-v3） */ }\n")
			b.WriteString("\tq := es.NewQuery[" + S + "]().\n")
			b.WriteString("\t\tNearest(" + cols + "." + f.GoName + ", vec, 5).\n")
			b.WriteString("\t\tKnnNumCandidates(100)\n")
			b.WriteString("\trepo := es.NewRepo[" + S + "](exampleClient)\n")
		b.WriteString("\treturn repo.Search(ctx, q)\n")
		b.WriteString("}\n\n")
		break
	}
	}
	formatted, err := format.Source([]byte(b.String()))
	if err != nil {
		return "", fmt.Errorf("esgen: 格式化示例失败: %w", err)
	}
	return string(formatted), nil
}

// generateRepoES 生成 Repo[T] 便捷构造脚手架（等价于 es.NewRepo[T](cli)）。
func generateRepoES(s ESStruct, pkg string) (string, error) {
	S := s.Name
	var b strings.Builder
	b.WriteString("// Code generated by gobreath-es/cmd/esgen. DO NOT EDIT.\n\n")
	b.WriteString("package " + pkg + "\n\n")
	b.WriteString("import es \"github.com/wusenshan/gobreath-es\"\n\n")
	b.WriteString("// New" + S + "Repo 创建 " + S + " 的泛型仓储句柄（便捷构造，等价于 es.NewRepo[" + S + "](cli)）。\n")
	b.WriteString("func New" + S + "Repo(cli *es.Client) *es.Repo[" + S + "] {\n")
	b.WriteString("\treturn es.NewRepo[" + S + "](cli)\n")
	b.WriteString("}\n")
	formatted, err := format.Source([]byte(b.String()))
	if err != nil {
		return "", fmt.Errorf("esgen: 格式化 Repo 脚手架失败: %w", err)
	}
	return string(formatted), nil
}

// appendExtrasES 在 files 中追加 example.go 与可选 <struct>_repo.go。
func appendExtrasES(files map[string]string, structs []ESStruct, pkg string, opts Options) error {
	if opts.Example {
		ex, err := generateExampleES(structs[0], pkg)
		if err != nil {
			return err
		}
		files["example.go"] = ex
	}
	if opts.Repo {
		for _, s := range structs {
			base := strings.ToLower(toSnake(s.Name))
			rp, err := generateRepoES(s, pkg)
			if err != nil {
				return err
			}
			files[base+"_repo.go"] = rp
		}
	}
	return nil
}

// assembleStructs 应用命名覆盖并按输出模式渲染文件内容（FromMapping 与 FromJSON 共用）。
func assembleStructs(structs []ESStruct, opts Options) (map[string]string, error) {
	if len(structs) == 0 {
		return nil, fmt.Errorf("esgen: 未能解析出任何文档结构")
	}
	pkg := opts.Package
	if pkg == "" {
		pkg = "model"
	}
	// 应用命名覆盖（仅作用于第一个顶层文档，单文档场景）
	if opts.StructName != "" {
		structs[0].Name = opts.StructName
	}
	if opts.IndexName != "" {
		structs[0].IndexName = opts.IndexName
	} else if structs[0].IndexName == "docs" {
		structs[0].IndexName = plural(toSnake(structs[0].Name))
	}

	out := map[string]string{}
	switch opts.Mode {
	case TwoFiles:
		out["models.go"] = assembleModels(pkg, structs)
		out["columns.go"] = assembleColumns(pkg, structs)
	case SingleFile:
		out["models_gen.go"] = assembleSingle(pkg, structs)
	default: // PerType
		for _, s := range structs {
			out[s.Name+".go"] = assembleOneModel(pkg, s)
			out[s.Name+"_cols.go"] = assembleOneColumns(pkg, s)
		}
	}
	if err := appendExtrasES(out, structs, pkg, opts); err != nil {
		return nil, err
	}
	return out, nil
}

// ---- 渲染 ----

func assembleModels(pkg string, structs []ESStruct) string {
	var b strings.Builder
	b.WriteString("// Code generated by gobreath-es/cmd/esgen. DO NOT EDIT.\n\n")
	b.WriteString("package " + pkg + "\n\n")
	if anyTime(structs) {
		b.WriteString("import \"time\"\n\n")
	}
	for _, s := range structs {
		b.WriteString(renderStruct(s, true))
	}
	return mustFormat(b.String())
}

func assembleColumns(pkg string, structs []ESStruct) string {
	var b strings.Builder
	b.WriteString("// Code generated by gobreath-es/cmd/esgen. DO NOT EDIT.\n\n")
	b.WriteString("package " + pkg + "\n\n")
	b.WriteString("import es \"github.com/wusenshan/gobreath-es\"\n\n")
	for _, s := range structs {
		b.WriteString(renderCols(s))
	}
	return mustFormat(b.String())
}

func assembleSingle(pkg string, structs []ESStruct) string {
	var b strings.Builder
	b.WriteString("// Code generated by gobreath-es/cmd/esgen. DO NOT EDIT.\n\n")
	b.WriteString("package " + pkg + "\n\n")
	if anyTime(structs) {
		b.WriteString("import (\n\t\"time\"\n\n\tRepos \"github.com/wusenshan/gobreath-es\"\n)\n\n")
	} else {
		b.WriteString("import Repos \"github.com/wusenshan/gobreath-es\"\n\n")
	}
	for _, s := range structs {
		b.WriteString(renderStruct(s, true))
		b.WriteString(renderColsRenamed(s, "Repos"))
	}
	return mustFormat(b.String())
}

func assembleOneModel(pkg string, s ESStruct) string {
	var b strings.Builder
	b.WriteString("// Code generated by gobreath-es/cmd/esgen. DO NOT EDIT.\n\n")
	b.WriteString("package " + pkg + "\n\n")
	if s.HasTime {
		b.WriteString("import \"time\"\n\n")
	}
	b.WriteString(renderStruct(s, true))
	return mustFormat(b.String())
}

func assembleOneColumns(pkg string, s ESStruct) string {
	var b strings.Builder
	b.WriteString("// Code generated by gobreath-es/cmd/esgen. DO NOT EDIT.\n\n")
	b.WriteString("package " + pkg + "\n\n")
	b.WriteString("import es \"github.com/wusenshan/gobreath-es\"\n\n")
	b.WriteString(renderCols(s))
	return mustFormat(b.String())
}

// renderStruct 渲染一个结构体定义 + 嵌套子结构。emitIndex 为 true 时附带 IndexName 方法
//（仅顶层文档需要；嵌套子结构只是模型里的类型，不应被框架当成索引）。
func renderStruct(s ESStruct, emitIndex bool) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("// %s 由 esgen 从 ES mapping 生成。\n", s.Name))
	b.WriteString(fmt.Sprintf("type %s struct {\n", s.Name))
	for _, f := range s.Fields {
		b.WriteString(fmt.Sprintf("\t%s %s %s\n", f.GoName, f.GoType, jsonTag(f)))
	}
	b.WriteString("}\n\n")
	if emitIndex {
		b.WriteString("// IndexName 返回该文档绑定的 ES 索引名。\n")
		b.WriteString(fmt.Sprintf("func (%s) IndexName() string { return %q }\n\n", s.Name, s.IndexName))
	}
	for _, n := range s.Nested {
		b.WriteString(renderStruct(n, false))
	}
	return b.String()
}

// renderCols 渲染字段闭包集合（使用默认别名 es）。
func renderCols(s ESStruct) string {
	return renderColsRenamed(s, "es")
}

// renderColsRenamed 渲染字段闭包集合，import 别名可指定（单文件模式用 Repos 避免与字段名冲突）。
// 嵌套 object 字段不进列集合（它是子文档，不是可比较的标量列；子字段应按 address.city 这类路径查询）。
func renderColsRenamed(s ESStruct, alias string) string {
	var b strings.Builder
	setName := s.Name + "ColumnSet"
	varName := s.Name + "Cols"
	b.WriteString(fmt.Sprintf("// %s 是 %s 的预计算字段集合，字段与 %s 的导出标量字段一一对应。\n", setName, s.Name, s.Name))
	b.WriteString(fmt.Sprintf("type %s struct {\n", setName))
	for _, f := range s.Fields {
		if f.IsNested {
			continue
		}
		b.WriteString(fmt.Sprintf("\t%s %s.ColExpr\n", f.GoName, alias))
	}
	b.WriteString("}\n\n")
	b.WriteString(fmt.Sprintf("// %s 提供了 %s 所有导出标量字段的 ColExpr，可直接用于查询构造器。\n", varName, s.Name))
	b.WriteString(fmt.Sprintf("var %s = %s{\n", varName, setName))
	for _, f := range s.Fields {
		if f.IsNested {
			continue
		}
		b.WriteString(fmt.Sprintf("\t%s: %s.ColOf[%s](%q),\n", f.GoName, alias, s.Name, f.GoName))
	}
	b.WriteString("}\n\n")
	return b.String()
}

func jsonTag(f ESField) string {
	if f.IsVector {
		return fmt.Sprintf("`json:%q es:%q`", f.JsonName, fmt.Sprintf("vector(%d)", f.VectorDim))
	}
	return fmt.Sprintf("`json:%q`", f.JsonName)
}

func mustFormat(src string) string {
	formatted, err := format.Source([]byte(src))
	if err != nil {
		// 理论上不应发生；保留原始内容便于排查
		return src
	}
	return string(formatted)
}

// ---- 辅助 ----

func hasTime(s ESStruct) bool {
	for _, f := range s.Fields {
		if f.HasTime {
			return true
		}
	}
	for _, n := range s.Nested {
		if hasTime(n) {
			return true
		}
	}
	return false
}

func anyTime(structs []ESStruct) bool {
	for _, s := range structs {
		if hasTime(s) {
			return true
		}
	}
	return false
}

func sortStruct(s *ESStruct) {
	sort.Slice(s.Fields, func(i, j int) bool { return s.Fields[i].JsonName < s.Fields[j].JsonName })
	for i := range s.Nested {
		sortStruct(&s.Nested[i])
	}
}

// toGoName 把 snake/kebab 命名转 PascalCase，并对常见缩写做全大写（id→ID, url→URL ...）。
func toGoName(s string) string {
	segs := strings.FieldsFunc(s, func(r rune) bool { return r == '_' || r == '-' || r == ' ' })
	var b strings.Builder
	for _, seg := range segs {
		if seg == "" {
			continue
		}
		switch strings.ToUpper(seg) {
		case "ID", "URL", "IP", "JSON", "HTML", "API", "URI", "HTTP", "HTTPS", "SQL", "UUID", "CPU", "GPU":
			b.WriteString(strings.ToUpper(seg))
		default:
			b.WriteString(strings.ToUpper(seg[:1]) + seg[1:])
		}
	}
	if b.Len() == 0 {
		return "Field"
	}
	out := b.String()
	// Go 标识符首字符必须是字母/下划线；数字开头补 X
	if r := out[0]; r >= '0' && r <= '9' {
		out = "X" + out
	}
	return out
}

func toSnake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r - 'A' + 'a')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func plural(s string) string {
	if s == "" {
		return s
	}
	if strings.HasSuffix(s, "y") &&
		!strings.HasSuffix(s, "ay") && !strings.HasSuffix(s, "ey") &&
		!strings.HasSuffix(s, "oy") && !strings.HasSuffix(s, "uy") {
		return s[:len(s)-1] + "ies"
	}
	switch {
	case strings.HasSuffix(s, "s"), strings.HasSuffix(s, "x"),
		strings.HasSuffix(s, "ch"), strings.HasSuffix(s, "sh"):
		return s + "es"
	}
	return s + "s"
}

// StructNames 从 Go 源码中收集导出的结构体名（供 Web 生成器的 struct 模式批量生成）。
func StructNames(src string) ([]string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "model.go", src, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("esgen: 解析 Go 源码: %w", err)
	}
	var names []string
	for _, decl := range file.Decls {
		ts, ok := decl.(*ast.GenDecl)
		if !ok || ts.Tok != token.TYPE {
			continue
		}
		for _, spec := range ts.Specs {
			tss, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			if ast.IsExported(tss.Name.Name) {
				if _, ok := tss.Type.(*ast.StructType); ok {
					names = append(names, tss.Name.Name)
				}
			}
		}
	}
	return names, nil
}
