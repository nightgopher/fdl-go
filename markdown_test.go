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
	want := "# 标题\n\n## 小节\n\na **bold** [link](https://x.y)\n\n1. one\n    - sub\n2. two\n\n- [x] done\n\n```rust\nfn main() {}\n```\n\n> q\n\n---\n"
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

// 对拍 markdown_files_round_trip：编辑器写回的读回一致，含 front
// matter 和相对图片。
func TestMarkdownFilesRoundTrip(t *testing.T) {
	src := "---\ntitle: x\n---\n\n# Heading\n\nSome **bold**, *italic*, `code` and [a link](https://x.y).\n\n- one\n    - nested\n- two\n\n1. first\n2. second\n\n- [ ] todo\n- [x] done\n\n> quoted\n\n```rust\nfn main() {}\n```\n\n![alt](img/a%20b.png)\n\n| a | b |\n| --- | --- |\n| 1 | 2 |\n\n---\n"
	base := "/notes"
	doc := ParseFile(src, "page", base)
	if doc.Title.Text != "page" {
		t.Errorf("title = %q", doc.Title.Text)
	}
	found := false
	for i := range doc.Blocks {
		if img, ok := doc.Blocks[i].Kind.(Image); ok {
			want := "/notes/img/a b.png"
			if img.Src == want {
				found = true
			}
		}
	}
	if !found {
		t.Error("相对图片没有解析成绝对路径")
	}
	fm, _ := FrontMatter(src)
	out := WriteFile(&doc, fm, base)
	if out != src {
		t.Errorf("写回不一致:\n got: %q\nwant: %q", out, src)
	}
	again := ParseFile(out, "page", base)
	fm2, _ := FrontMatter(out)
	if got := WriteFile(&again, fm2, base); got != out {
		t.Errorf("再写不一致:\n got: %q\nwant: %q", got, out)
	}
}

func mdPreserved(t *testing.T, original string, edit func(*Document)) string {
	t.Helper()
	base := "/notes"
	doc := ParseFile(original, "t", base)
	edit(&doc)
	fm, _ := FrontMatter(original)
	out := WriteFileWith(&doc, fm, &ExportOptions{Base: base, Original: original}).Text
	// 无论保留了什么，文件读回来都是这篇文档。
	canon := func(d *Document) string { return WriteFile(d, "", base) }
	re := ParseFile(out, "t", base)
	if canon(&re) != canon(&doc) {
		t.Fatalf("读回不一致:\n%s", out)
	}
	return out
}

// 对拍 saving_keeps_untouched_lines：只有改动的重写，别的块保持自己的
// 语法和前后的空行。
func TestSavingKeepsUntouchedLines(t *testing.T) {
	src := "---\na: 1\n---\n* apple\n+ pear\n\n\nTitle\n\nfirst<br>second\n__bold__ text\n\n1. one\n1. two\n\n```\ncode\n```\n"
	// 没改：逐字节一致。
	if got := mdPreserved(t, src, func(*Document) {}); got != src {
		t.Errorf("没改也变了:\n got: %q\nwant: %q", got, src)
	}
	// 改一段：只有那一行变。
	out := mdPreserved(t, src, func(d *Document) {
		for i := range d.Blocks {
			if d.Blocks[i].Text.Text == "Title" {
				d.Blocks[i].Text = Plain("New title")
			}
		}
	})
	if want := strings.ReplaceAll(src, "Title", "New title"); out != want {
		t.Errorf("改一段:\n got: %q\nwant: %q", out, want)
	}
	// 中间删一块、末尾加一块。
	out = mdPreserved(t, src, func(d *Document) {
		kept := d.Blocks[:0]
		for i := range d.Blocks {
			if d.Blocks[i].Text.Text != "Title" {
				kept = append(kept, d.Blocks[i])
			}
		}
		d.Blocks = append(kept, BlockText("added"))
	})
	want := "---\na: 1\n---\n* apple\n+ pear\n\nfirst<br>second\n__bold__ text\n\n1. one\n1. two\n\n```\ncode\n```\n\nadded\n"
	if out != want {
		t.Errorf("增删:\n got: %q\nwant: %q", out, want)
	}
	// 移动一块保留它的文字。
	out = mdPreserved(t, src, func(d *Document) {
		for i := range d.Blocks {
			if d.Blocks[i].Text.Text == "Title" {
				b := d.Blocks[i]
				d.Blocks = append(d.Blocks[:i], d.Blocks[i+1:]...)
				d.Blocks = append([]Block{b}, d.Blocks...)
				break
			}
		}
	})
	if !strings.HasPrefix(out, "---\na: 1\n---\nTitle\n\n* apple\n+ pear\n") {
		t.Errorf("移动:\n%s", out)
	}
	if !strings.Contains(out, "first<br>second\n__bold__ text") {
		t.Errorf("移动丢了原文:\n%s", out)
	}
	// 列表项变段落，补上空行。
	out = mdPreserved(t, "- a\n- b\n", func(d *Document) { d.Blocks[1].Kind = Text{} })
	if out != "- a\n\nb\n" {
		t.Errorf("类型变换:\n got: %q", out)
	}
	// CRLF 文件保持 CRLF。
	crlf := strings.ReplaceAll(src, "\n", "\r\n")
	if got := mdPreserved(t, crlf, func(*Document) {}); got != crlf {
		t.Errorf("CRLF:\n got: %q", got)
	}
}

// 对拍 random_edits_keep_files_readable：随机文件与随机编辑，保存的文
// 件总能读回编辑后的文档；没动的原样保留。
func TestRandomEditsKeepFilesReadable(t *testing.T) {
	parts := []string{"* star", "- dash", "+ plus", "1. one", "3) three", "- [ ] todo", "- [x] done", "# Head", "Setext\n===", "para <b>html</b>", "__under__ bold",
		"> quote\n> more", "```\nfence\n\nblank\n```", "---", "| a | b |\n| - | - |\n| 1 | 2 |", "![alt](x.png)", "text\nlazy"}
	seps := []string{"\n", "\n\n", "\n\n\n"}
	seed := uint64(0x2545f4914f6cdd1d)
	rand := func(n int) int {
		seed ^= seed << 13
		seed ^= seed >> 7
		seed ^= seed << 17
		return int(seed % uint64(n))
	}
	for i := 0; i < 400; i++ {
		var src strings.Builder
		for k := 0; k < 1+rand(8); k++ {
			src.WriteString(parts[rand(len(parts))])
			src.WriteString(seps[rand(3)])
		}
		original := src.String()
		if got := mdPreserved(t, original, func(*Document) {}); strings.TrimRight(got, " \t\n\r") != strings.TrimRight(original, " \t\n\r") {
			t.Fatalf("没动也变了:\n got: %q\nwant: %q", got, original)
		}
		mdPreserved(t, original, func(d *Document) {
			for k := 0; k < 1+rand(3); k++ {
				n := len(d.Blocks)
				switch rand(4) {
				case 0:
					if n > 1 {
						i := rand(n)
						d.Blocks = append(d.Blocks[:i], d.Blocks[i+1:]...)
					}
				case 1:
					at := rand(n + 1)
					d.Blocks = append(d.Blocks[:at], append([]Block{BlockText("new")}, d.Blocks[at:]...)...)
				case 2:
					j := rand(n)
					if HasText(d.Blocks[j].Kind) {
						d.Blocks[j].Text = Plain("edited")
					}
				default:
					j := rand(n)
					if HasText(d.Blocks[j].Kind) && d.Blocks[j].Text.Text == strings.TrimSpace(d.Blocks[j].Text.Text) && len(d.Blocks[j].Children) == 0 {
						if _, isCode := d.Blocks[j].Kind.(Code); !isCode {
							switch rand(3) {
							case 0:
								d.Blocks[j].Kind = Text{}
							case 1:
								d.Blocks[j].Kind = Bullet{}
							default:
								d.Blocks[j].Kind = Ordered{}
							}
						}
					}
				}
			}
		})
	}
}

// 对拍 front_matter_needs_a_closing_line。
func TestFrontMatterNeedsAClosingLine(t *testing.T) {
	if fm, body := FrontMatter("---\na: 1\n---\n\nbody"); fm != "---\na: 1\n---\n\n" || body != "body" {
		t.Errorf("fm = %q body = %q", fm, body)
	}
	if fm, body := FrontMatter("---\nno end"); fm != "" || body != "---\nno end" {
		t.Errorf("fm = %q body = %q", fm, body)
	}
	if fm, body := FrontMatter("body"); fm != "" || body != "body" {
		t.Errorf("fm = %q body = %q", fm, body)
	}
}
