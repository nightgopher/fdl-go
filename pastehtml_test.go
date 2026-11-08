package fdl

import (
	"strings"
	"testing"
)

func htmlXML(htmlText string) string {
	doc := NewDocument()
	doc.Blocks = HTMLToBlocks(htmlText)
	return Serialize(&doc)
}

// 对拍 paste.rs 的 blocks_and_marks。
func TestHTMLBlocksAndMarks(t *testing.T) {
	x := htmlXML("<meta charset='utf-8'><h2>Title</h2><p>a <b>bold</b> and <a href=\"https://x.y/\">link</a></p>" +
		"<ul><li>one<ul><li>sub</li></ul></li><li><input type=checkbox checked> done</li></ul>" +
		"<ol start=3><li>three</li></ol><blockquote><p>q</p></blockquote><hr><pre><code class=\"language-rust\">fn x() {}\n</code></pre>")
	for _, want := range []string{
		"<h2>Title</h2>",
		`<p>a <b>bold</b> and <a href="https://x.y/">link</a></p>`,
		"<li>one", "sub",
		`<checkbox done="true">done</checkbox>`,
		`<li seq="3">three</li>`,
		"<blockquote>", "<hr/>", `lang="rust"`, "<code>fn x() {}</code></pre>",
	} {
		if !strings.Contains(x, want) {
			t.Errorf("缺少 %s:\n%s", want, x)
		}
	}
}

// 对拍 paste.rs 的 feishu_todo_and_callout。
func TestHTMLFeishuTodoAndCallout(t *testing.T) {
	x := htmlXML("<ul class=\"list-done1\"><li data-list=\"done\"><div>t</div></li></ul>" +
		"<div class=\"zoneType-calloutBlock\"><div class=\"callout-container\" data-emoji-id=\"chestnut\">" +
		"<div class=\"callout-block\" style=\"background-color:rgb(255,245,235);border-color:rgb(254,212,164);\"><div>c</div></div></div></div>")
	if !strings.Contains(x, `<checkbox done="true">t</checkbox>`) {
		t.Errorf("todo:\n%s", x)
	}
	if !strings.Contains(x, `<callout emoji="🌰" background-color="light-orange" border-color="orange">`) {
		t.Errorf("callout:\n%s", x)
	}
}

// 对拍 paste.rs 的 google_docs_wrapper_is_not_bold。
func TestHTMLGoogleDocsWrapperIsNotBold(t *testing.T) {
	x := htmlXML("<b style=\"font-weight:normal;\" id=\"docs-internal-guid-1\"><p dir=\"ltr\"><span style=\"font-weight:700\">B</span><span> plain</span></p></b>")
	if !strings.Contains(x, "<p><b>B</b> plain</p>") {
		t.Errorf("google docs:\n%s", x)
	}
}

// 对拍 paste.rs 的 whitespace_and_breaks。
func TestHTMLWhitespaceAndBreaks(t *testing.T) {
	x := htmlXML("<div>\n  line one <br>\n  line   two\n</div><p></p><div>x</div>")
	if !strings.Contains(x, "<p>line one</p>\n<p>line two</p>\n<p></p>\n<p>x</p>") {
		t.Errorf("空白:\n%s", x)
	}
}

// 对拍 paste.rs 的 tables。
func TestHTMLTables(t *testing.T) {
	x := htmlXML("<table><tr><th>a</th><th>b</th></tr><tr><td colspan=2>c</td></tr></table>")
	if !strings.Contains(x, "<table") {
		t.Errorf("表格:\n%s", x)
	}
	if !strings.Contains(x, `colspan="2"`) {
		t.Errorf("跨度:\n%s", x)
	}
}
