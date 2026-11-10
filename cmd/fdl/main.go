// fdl 是 quiet-md 文档引擎（fdl_core）Go 移植的命令行入口：解析/序列
// 化 DocxXML、Markdown 与 HTML 互转、round-trip 校验。
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/nightgopher/fdl-go"
)

func usage() {
	fmt.Fprint(os.Stderr, `用法：fdl <命令> [参数]

命令：
  roundtrip <file.xml>...   解析→序列化，字节级对比（M1 验收口径）
  xml2md   <file.xml>       DocxXML → Markdown（-assets 复制本地图片）
  md2xml   <file.md>        Markdown → DocxXML
  html2xml <file.html>      剪贴板 HTML → DocxXML
  info     <file.xml>       文档结构摘要（块数、提醒、@文档、资源）
`)
	os.Exit(2)
}

func readArg(fs *flag.FlagSet) string {
	if fs.NArg() < 1 {
		usage()
	}
	data, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		fatal(err)
	}
	return string(data)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "fdl:", err)
	os.Exit(1)
}

func main() {
	if len(os.Args) == 2 && (os.Args[1] == "-version" || os.Args[1] == "--version") {
		fmt.Println(fdl.Version)
		return
	}
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "roundtrip":
		fs := flag.NewFlagSet("roundtrip", flag.ExitOnError)
		fs.Parse(os.Args[2:])
		if fs.NArg() == 0 {
			usage()
		}
		bad := false
		for _, path := range fs.Args() {
			data, err := os.ReadFile(path)
			if err != nil {
				fatal(err)
			}
			doc, err := fdl.Parse(string(data))
			if err != nil {
				fmt.Fprintf(os.Stderr, "fdl: %s: 解析失败: %v\n", path, err)
				bad = true
				continue
			}
			out := fdl.Serialize(&doc)
			status := "ok"
			if out != string(data) {
				status = "DIFF"
				bad = true
			}
			fmt.Printf("%-8s %s (%d 字节)\n", status, path, len(data))
			if status == "DIFF" {
				again, err := fdl.Parse(out)
				if err == nil && fdl.Serialize(&again) == out {
					fmt.Printf("         （再读再写稳定；与原文件差异为排版规范化）\n")
				}
			}
		}
		if bad {
			os.Exit(1)
		}
	case "xml2md":
		fs := flag.NewFlagSet("xml2md", flag.ExitOnError)
		out := fs.String("o", "", "输出文件（默认 stdout）")
		assets := fs.String("assets", "", "复制图片/附件到该目录并登记（默认仅文本）")
		root := fs.String("root", "", "文档库根目录（默认 QUIET_MD_HOME 或 ~/Documents/Quiet.md）")
		fs.Parse(os.Args[2:])
		doc, err := fdl.Parse(readArg(fs))
		if err != nil {
			fatal(err)
		}
		opts := &fdl.ExportOptions{LibraryRoot: *root, Assets: *assets}
		exp := fdl.ExportMarkdown(&doc, opts)
		writeOut(*out, exp.Text)
		for _, f := range exp.Files {
			fmt.Fprintf(os.Stderr, "待复制: %s -> %s\n", f.Src, f.Rel)
		}
		if exp.Missing > 0 {
			fmt.Fprintf(os.Stderr, "缺 %d 个本地资源\n", exp.Missing)
		}
	case "md2xml":
		fs := flag.NewFlagSet("md2xml", flag.ExitOnError)
		out := fs.String("o", "", "输出文件（默认 stdout）")
		title := fs.String("title", "", "文档标题（默认取第一个标题块）")
		file := fs.Bool("file", false, "整文件导入：空行只分段，![alt](src) 整行是图片")
		fs.Parse(os.Args[2:])
		text := readArg(fs)
		blocks := fdl.MarkdownToBlocks(text, fdl.Off{File: *file})
		doc := fdl.NewDocument()
		doc.Blocks = blocks
		if *title != "" {
			doc.Title = fdl.Plain(*title)
		} else {
			// 第一个标题块当文档标题，其余顺延。
			for i := range doc.Blocks {
				if h, ok := doc.Blocks[i].Kind.(fdl.Heading); ok && h.Level == 1 {
					doc.Title = doc.Blocks[i].Text
					doc.Blocks = append(doc.Blocks[:i], doc.Blocks[i+1:]...)
					break
				}
			}
		}
		writeOut(*out, fdl.Serialize(&doc))
	case "html2xml":
		fs := flag.NewFlagSet("html2xml", flag.ExitOnError)
		out := fs.String("o", "", "输出文件（默认 stdout）")
		fs.Parse(os.Args[2:])
		doc := fdl.NewDocument()
		doc.Blocks = fdl.HTMLToBlocks(readArg(fs))
		writeOut(*out, fdl.Serialize(&doc))
	case "info":
		fs := flag.NewFlagSet("info", flag.ExitOnError)
		fs.Parse(os.Args[2:])
		doc, err := fdl.Parse(readArg(fs))
		if err != nil {
			fatal(err)
		}
		n := 0
		doc.ForEachBlock(func(*fdl.Block) { n++ })
		fmt.Printf("块数：%d\n标题：%q\n", n, doc.Title.Text)
		if rs := doc.Reminders(); len(rs) > 0 {
			fmt.Printf("提醒：%d 个\n", len(rs))
		}
		if ls := doc.DocLinks(); len(ls) > 0 {
			fmt.Printf("@文档：%d 条\n", len(ls))
		}
		if srcs := doc.AssetSources(); len(srcs) > 0 {
			fmt.Println("资源：")
			for _, s := range srcs {
				fmt.Println("  " + s)
			}
		}
	default:
		usage()
	}
}

func writeOut(path, content string) {
	if path == "" {
		os.Stdout.WriteString(content)
		return
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		fatal(err)
	}
}
