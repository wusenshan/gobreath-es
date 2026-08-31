// Package es 是一个 lambda 风格、类型安全的 Elasticsearch 框架，设计哲学与 gobreath-orm 一致：
// 调用点零字符串字段名（通过 Col[T] 闭包 + 反射从结构体 tag 取字段名）、参数化防注入、
// 查询构造器产出标准 ES DSL。区别在于存储是 JSON 文档而非关系表，所以字段名以 json tag 为准。
package es

import (
	"fmt"
	"reflect"
	"strings"
)

// ColExpr 字段选择器解析后的字段表达式。调用点不会出现任何字符串字段名。
type ColExpr struct {
	name string
}

// Col 把一个「返回字段指针的闭包」解析成 ES 文档字段名。
//
// 用法：
//
//	es.Col[Product](func(p *Product) *string { return &p.Name })
//
// 字段名解析优先级：json tag > es tag > 蛇形命名（字段名）。由于 ES 文档本质是 JSON，
// 以 json tag 作为字段名能保证「查询条件」与「文档序列化」完全一致，无需自定义编解码。
// 写错字段会在编译期（类型不对）或运行期（tag 拼错）立刻暴露，而不是生成一条错误查询。
//
// 若不想写闭包，可用更简短的 ColOf[T](字段名) 取字段表达式（详见 ColOf）。
func Col[T any, F any](picker func(*T) *F) ColExpr {
	return ColExpr{name: resolveColumn(picker)}
}

// ColOf 按结构体字段名直接取字段表达式，免去手写字段指针闭包，调用点更短。
//
// 用法：
//
//	es.ColOf[Product]("Name")   // 等价于上面的 Col 闭包，但无需 func 包裹
//
// 匹配优先级：Go 字段名（大小写敏感）> ES 字段名（json/es tag 或蛇形命名）。
// 适用于不想写闭包、或结合代码生成（反射批量构造条件）的场景；未匹配会 panic，
// 提示字段不存在。注意：与 Col 闭包相比，ColOf 的字段名是字符串，仅运行期校验
// （字段改名时不会编译报错），需要更强编译期保障时仍用 Col 闭包。
func ColOf[T any](fieldName string) ColExpr {
	m := getMeta[T]()
	for i := range m.fields {
		if m.fields[i].goName == fieldName {
			return ColExpr{name: m.fields[i].name}
		}
	}
	for i := range m.fields {
		if m.fields[i].name == fieldName {
			return ColExpr{name: m.fields[i].name}
		}
	}
	panic(fmt.Sprintf("es: 类型 %s 中不存在字段 %q", typeName[T](), fieldName))
}

// typeName 返回类型 T 的 Go 类型名（用于错误信息）。
func typeName[T any]() string {
	return reflect.TypeOf((*T)(nil)).Elem().Name()
}

// resolveColumn 用反射从 picker 闭包反查其指向的结构体字段，读出字段名。
func resolveColumn[T any, F any](picker func(*T) *F) string {
	t := new(T)
	tv := reflect.ValueOf(t).Elem()          // 可寻址的结构体
	ptr := picker(t)                         // *F，指向 t 的某个字段
	target := reflect.ValueOf(ptr).Pointer() // 该指针持有的地址（即字段地址）

	rt := tv.Type()
	for i := 0; i < rt.NumField(); i++ {
		f := tv.Field(i)
		if f.CanAddr() && f.Addr().Pointer() == target {
			return fieldName(rt.Field(i))
		}
	}
	panic("es: 无法从 picker 闭包解析出字段，请确认闭包返回的是该结构体的字段指针")
}

func fieldName(f reflect.StructField) string {
	if tag := f.Tag.Get("json"); tag != "" {
		if n := strings.Split(tag, ",")[0]; n != "" && n != "-" {
			return n
		}
	}
	if tag := f.Tag.Get("es"); tag != "" {
		if n := strings.Split(tag, ",")[0]; n != "" && n != "-" {
			return n
		}
	}
	return toSnake(f.Name)
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
