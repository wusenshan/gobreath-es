package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wusenshan/gobreath-es/gen"
)

func main() {
	var (
		types     = flag.String("type", "", "要生成字段集合的结构体类型，多个用逗号分隔，例如 Product,Order")
		out       = flag.String("out", "", "输出文件名，默认 <第一个type>_cols.go")
		dir       = flag.String("dir", ".", "模型源码所在目录 / 生成文件输出目录")
		mapping   = flag.String("mapping", "", "ES mapping JSON 文件路径；指定后从 mapping 生成模型+字段闭包")
		pkg       = flag.String("pkg", "model", "生成代码的包名")
		mode      = flag.String("mode", "perType", "输出方式：perType / twoFiles / singleFile")
		indexName = flag.String("index", "", "索引名（mapping 模式）；为空则用 plural(toSnake(StructName)) 兜底")
		serve     = flag.Bool("serve", false, "启动本地 Web 生成器（esgen serve）")
		addr      = flag.String("addr", ":8080", "serve 监听地址")
	)
	flag.Parse()

	if *serve {
		runServer(*addr)
		return
	}

	if *mapping != "" {
		runMapping(*mapping, *dir, *pkg, *mode, *indexName, *types)
		return
	}

	if *types == "" {
		fmt.Fprintln(os.Stderr, "用法: esgen -type Product[,Order] [-out product_cols.go] [-dir .]")
		fmt.Fprintln(os.Stderr, "     esgen -mapping mapping.json [-pkg model] [-mode perType|twoFiles|singleFile] [-index products] [-type Product] [-dir .]")
		fmt.Fprintln(os.Stderr, "     esgen -serve   # 启动本地 Web 生成器 http://:8080")
		os.Exit(1)
	}
	typeList := strings.Split(*types, ",")
	for i := range typeList {
		typeList[i] = strings.TrimSpace(typeList[i])
	}

	outFile := *out
	if outFile == "" {
		outFile = typeList[0] + "_cols.go"
	}

	if err := gen.Generate(*dir, typeList, outFile); err != nil {
		fmt.Fprintf(os.Stderr, "esgen: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("esgen: 已生成 %s/%s\n", *dir, outFile)
}

func runMapping(path, dir, pkg, mode, indexName, structName string) {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "esgen: 读取 mapping 文件: %v\n", err)
		os.Exit(1)
	}
	om := gen.PerType
	switch strings.ToLower(mode) {
	case "twofiles", "two":
		om = gen.TwoFiles
	case "singlefile", "single":
		om = gen.SingleFile
	}
	files, err := gen.FromMapping(string(data), gen.Options{
		Package:    pkg,
		IndexName:  indexName,
		StructName: structName,
		Mode:       om,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "esgen: %v\n", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "esgen: 创建输出目录: %v\n", err)
		os.Exit(1)
	}
	for name, content := range files {
		outPath := filepath.Join(dir, name)
		if err := os.WriteFile(outPath, []byte(content), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "esgen: 写入 %s: %v\n", outPath, err)
			os.Exit(1)
		}
		fmt.Printf("esgen: 已生成 %s\n", outPath)
	}
}
