package main

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui/richtext"

	fdl "github.com/nightgopher/fdl-go"
)

// 打开 → richtext → 保存 → 再打开：正文保真（标题单列）。
func TestRoundTripThroughEditor(t *testing.T) {
	src := `<title>往返</title>
<h1>标题一</h1>
<p>普通 <b>加粗</b> <em>斜体</em> <u>下划线</u> <del>删除</del> <code>码</code> <a href="https://x.y">链</a> 公式 <latex>E</latex> 完</p>
<ul><li>一<ul><li>嵌</li></ul></li><li>二</li></ul>
<ol><li seq="3">三</li></ol>
<checkbox done="true">完</checkbox><checkbox done="false">未</checkbox>
<blockquote><p>引</p></blockquote>
<pre lang="rust"><code>fn x()</code></pre>
<hr/>
<p>结尾</p>
`
	doc, err := fdl.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	// → 编辑器
	rt := fdlToRichText(&doc)
	// → 保存
	doc.Title = fdl.Plain("往返")
	doc.Blocks = richToFdl(rt)
	out := fdl.Serialize(&doc)
	// 再开一遍（编辑器往返应稳定）。
	doc2, err := fdl.Parse(out)
	if err != nil {
		t.Fatalf("保存后再解析失败：%v\n%s", err, out)
	}
	out2 := fdl.Serialize(&doc2)
	if out2 != out {
		t.Errorf("编辑器往返不稳定:\n--- 第一次\n%s\n--- 第二次\n%s", out, out2)
	}
	// 行内样式保留。
	for _, want := range []string{"<b>加粗</b>", "<em>斜体</em>", "<u>下划线</u>", "<del>删除</del>", "<code>码</code>", `href="https://x.y"`, "<latex>E</latex>"} {
		if !strings.Contains(out, want) {
			t.Errorf("缺少 %s:\n%s", want, out)
		}
	}
	// 块结构保留（已知偏差：编辑器列表从 1 编号、代码块语言不保留）。
	for _, want := range []string{"<h1>标题一</h1>", "<li>一\n    <ul>", "<li>嵌</li>", `<li>三</li>`, `<checkbox done="true">完</checkbox>`, "<blockquote>\n  <p>引</p>\n</blockquote>", "fn x()", "<hr/>", "<p>结尾</p>"} {
		if !strings.Contains(out, want) {
			t.Errorf("缺少 %s:\n%s", want, out)
		}
	}
	// 纯文本总量不丢。
	if pt := doc2.PlainText(); !strings.Contains(pt, "公式") || !strings.Contains(pt, "嵌") {
		t.Errorf("plain text 丢内容：%q", pt)
	}
}

// 空文档与标题段。
func TestEdgeCases(t *testing.T) {
	rt := richtext.TextOf(richtext.Paragraph{})
	if got := richToFdl(rt); len(got) != 1 {
		t.Errorf("空文本 = %d 块", len(got))
	}
	// 标题段（保存时被 main 提为文档标题）转换无碍。
	rt = richtext.TextOf(
		richtext.Paragraph{Style: richtext.ParagraphStyle{Heading: 1}, Runs: []richtext.Run{{Text: "T"}}},
		richtext.Paragraph{Runs: []richtext.Run{{Text: "正文"}}},
	)
	blocks := richToFdl(rt)
	if len(blocks) != 2 {
		t.Fatalf("blocks = %d", len(blocks))
	}
	if _, ok := blocks[0].Kind.(fdl.Heading); !ok {
		t.Errorf("首块 = %T", blocks[0].Kind)
	}
}
