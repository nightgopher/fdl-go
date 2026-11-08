package fdl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func link2(rt RichText, url string) RichText {
	u := url
	rt.Push("link", Marks{Link: &u})
	return rt
}

// 对拍 markdown.rs 的 exports_common_blocks。
func TestExportsCommonBlocks(t *testing.T) {
	bold := Plain("a ")
	bold.Push("bold ", Marks{Bold: true})
	bold = link2(bold, "https://x.y")
	doc := Document{
		Title: Plain("标题"),
		Blocks: []Block{
			NewBlock(Heading{Level: 2}, Plain("小节")),
			NewBlock(Text{}, bold),
			withChildren(NewBlock(Ordered{}, Plain("one")), []Block{NewBlock(Bullet{}, Plain("sub"))}),
			NewBlock(Ordered{}, Plain("two")),
			NewBlock(Todo{Done: true}, Plain("done")),
			NewBlock(Code{Lang: "Rust"}, Plain("fn main() {}")),
			withChildren(NewBlock(Quote{}, RichText{}), []Block{BlockText("q")}),
			NewBlock(Divider{}, RichText{}),
		},
	}
	want := "# 标题\n\n## 小节\n\na **bold** [link](https://x.y)\n\n1. one\n    - sub\n2. two\n- [x] done\n\n```rust\nfn main() {}\n```\n\n> q\n\n---\n"
	if got := ToMarkdown(&doc); got != want {
		t.Errorf("md:\n got: %q\nwant: %q", got, want)
	}
}

func withChildren(b Block, children []Block) Block {
	b.Children = children
	return b
}

// 对拍 markdown.rs 的 exports_tables。
func TestExportsTables(t *testing.T) {
	cell := func(s string) TableCell {
		c := EmptyCell()
		c.Blocks = []Block{BlockText(s)}
		return c
	}
	tb := Table{HeaderRows: 1, Rows: [][]TableCell{{cell("a"), cell("b|c")}, {cell("1")}}}
	want := "| a | b\\|c |\n| --- | --- |\n| 1 |  |"
	if got := mdTable(&tb, func(FileBlock) (string, bool) { return "", false }); got != want {
		t.Errorf("table:\n got: %q\nwant: %q", got, want)
	}
}

// 对拍 markdown.rs 的 inline_attachments_export_links_and_copies_including_tables。
func TestInlineAttachmentsExportLinksAndCopiesIncludingTables(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "assets", "report (1).pdf"), []byte("pdf"), 0o644); err != nil {
		t.Fatal(err)
	}
	file := FileBlock{SrcKind: "path", Src: "assets/report (1).pdf", Name: "Report [1]", View: ViewCard}
	text := Plain("before ")
	in := ToInline(InlineFile{File: file})
	text.Push("\ufffc", Marks{Inline: &in})
	text.Push(" after", Marks{})
	var tb Table
	tb.Rows = [][]TableCell{{EmptyCell()}}
	tb.Rows[0][0].Blocks = []Block{NewBlock(Text{}, text.Clone())}
	doc := Document{Blocks: []Block{NewBlock(Text{}, text), NewBlock(tb, RichText{})}}

	out := ExportMarkdown(&doc, &ExportOptions{LibraryRoot: root, Assets: "out.assets"})
	if len(out.Files) != 1 {
		t.Fatalf("files = %+v, want 1", out.Files)
	}
	if out.Missing != 0 {
		t.Errorf("missing = %d", out.Missing)
	}
	if n := strings.Count(out.Text, `[Report \[1\]](out.assets/report%20%281%29.pdf)`); n != 2 {
		t.Errorf("链接出现 %d 次, want 2:\n%s", n, out.Text)
	}
	plain := ExportMarkdown(&doc, &ExportOptions{LibraryRoot: root})
	if len(plain.Files) != 0 {
		t.Errorf("仅文本导出不应带文件: %+v", plain.Files)
	}
	if strings.Contains(plain.Text, "out.assets") {
		t.Errorf("仅文本导出不应有链接:\n%s", plain.Text)
	}
}

// 公式、未知行内对象、Raw 链接块与子页面列表。
func TestExportsInlineObjects(t *testing.T) {
	rt := Plain("公式 ")
	eq := ToInline(Equation{Src: `a_1`})
	rt.Push("\ufffc", Marks{Inline: &eq})
	rt.Push(" 尾", Marks{})
	doc := Document{Title: Plain("t"), Blocks: []Block{NewBlock(Text{}, rt)}}
	// 公式源码作为普通 run 参与转义（同上游：escape(body)）。
	if got := ToMarkdown(&doc); !strings.Contains(got, `公式 $a\_1$ 尾`) {
		t.Errorf("公式导出不对:\n%s", got)
	}

	sub := Document{Blocks: []Block{NewBlock(SubPageList{}, RichText{})}}
	out := ExportMarkdown(&sub, &ExportOptions{SubPages: []string{"甲", "  "}})
	if !strings.Contains(out.Text, "- 甲\n- 未命名文档") {
		t.Errorf("子页面列表不对:\n%s", out.Text)
	}

	raw, err := Parse(`<title>T</title><bookmark href="https://e.com/a b" title="书签"/>`)
	if err != nil {
		t.Fatal(err)
	}
	// 自闭合 bookmark 没有文本，按上游规则用链接本身当标签。
	if got := ToMarkdown(&raw); !strings.Contains(got, `[https://e.com/a b](https://e.com/a%20b)`) {
		t.Errorf("Raw 链接导出不对:\n%s", got)
	}
}

// 库外路径与缺失文件计入 missing，不进 Files。
func TestExportLocalAssetSafety(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "ok.png"), []byte("png"), 0o644)
	doc, err := Parse(`<title>T</title><img path="ok.png"/><img path="../evil.png"/><img path="missing.png"/>`)
	if err != nil {
		t.Fatal(err)
	}
	out := ExportMarkdown(&doc, &ExportOptions{LibraryRoot: root, Assets: "a"})
	if len(out.Files) != 1 || out.Files[0].Rel != "a/ok.png" {
		t.Errorf("files = %+v", out.Files)
	}
	if out.Missing != 2 {
		t.Errorf("missing = %d, want 2", out.Missing)
	}
	// 同名冲突编号。
	doc2, _ := Parse(`<title>T</title><img path="ok.png"/>`)
	doc2.Blocks = append(doc2.Blocks, doc2.Blocks[0].Clone())
	out2 := ExportMarkdown(&doc2, &ExportOptions{LibraryRoot: root, Assets: "a"})
	if len(out2.Files) != 1 { // 第二次引用复用同一个登记
		t.Errorf("重复登记: %+v", out2.Files)
	}
}
