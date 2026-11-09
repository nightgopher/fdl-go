package fdl

import (
	"strings"
	"testing"
)

func TestRenderHTMLBlocks(t *testing.T) {
	doc, err := Parse(`<title icon="📘">渲染 <b>测试</b></title>
<h1 seq="auto">标题</h1>
<p>普通 <b>加粗</b> <span text-color="red">红字</span> 公式 <latex>E=mc^2</latex> <a href="https://x.y">链接</a></p>
<ul><li>一<ul><li>嵌套</li></ul></li><li folded="true">二</li></ul>
<ol><li seq="3">三</li></ol>
<checkbox done="true">完成</checkbox>
<blockquote><p>引用</p></blockquote>
<pre lang="rust"><code>fn x() {}</code></pre>
<hr/>
<callout emoji="💡"><p>提示</p></callout>
<table><colgroup><col width="100"/></colgroup><thead><tr><th><p>A</p></th></tr></thead><tbody><tr><td><p>1</p></td></tr></tbody></table>
<img href="https://x/y.png" caption="图"/>
<p>结尾</p>
`)
	if err != nil {
		t.Fatal(err)
	}
	htmlText := RenderHTML(&doc)
	for _, want := range []string{
		"<h1>标题</h1>",
		"<b>加粗</b>",
		`style="color:#d83931"`,
		`<code class="math">$E=mc^2$</code>`,
		`<a href="https://x.y" target="_blank">链接</a>`,
		"<ul><li>一", "嵌套", "<ol><li>三</li></ol>",
		`<input type="checkbox" disabled checked>`,
		"<blockquote><p>引用</p></blockquote>",
		`data-lang="rust"`, "fn x() {}",
		"<hr>",
		`class="callout"`, "💡",
		"<table><colgroup>", "<th>", "<td>",
		`src="https://x/y.png"`,
		"<p>结尾</p>",
	} {
		if !strings.Contains(htmlText, want) {
			t.Errorf("缺少 %s:\n%s", want, htmlText)
		}
	}
	// 文本被转义，防注入。
	doc2, _ := Parse("<title>T</title><p>&lt;script&gt;alert(1)&lt;/script&gt;</p>")
	if h := RenderHTML(&doc2); strings.Contains(h, "<script>") {
		t.Errorf("没有转义：%s", h)
	}
}

func TestRenderHTMLIndentAndGrid(t *testing.T) {
	doc, err := Parse(`<title>T</title>
<p>父<p>子段落</p></p>
<grid><column width-ratio="0.3"><p>左</p></column><column width-ratio="0.7"><p>右</p></column></grid>
`)
	if err != nil {
		t.Fatal(err)
	}
	h := RenderHTML(&doc)
	for _, want := range []string{
		`class="indent"`, "<p>子段落</p>",
		`class="grid"`, `flex:0 0 30.0%`, `flex:0 0 70.0%`,
	} {
		if !strings.Contains(h, want) {
			t.Errorf("缺少 %s:\n%s", want, h)
		}
	}
}
