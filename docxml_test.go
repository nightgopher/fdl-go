package fdl

import (
	"os"
	"strings"
	"testing"
)

func requireEqStr(t *testing.T, got, want, label string) {
	t.Helper()
	if got == want {
		return
	}
	gl, wl := strings.Split(got, "\n"), strings.Split(want, "\n")
	for n := 0; n < len(gl) && n < len(wl); n++ {
		if gl[n] != wl[n] {
			t.Fatalf("%s: 首个差异在第 %d 行\n got: %q\nwant: %q", label, n+1, gl[n], wl[n])
		}
	}
	t.Fatalf("%s: 内容不同（行数 got=%d want=%d）\n got 尾行: %q\nwant 尾行: %q",
		label, len(gl), len(wl), gl[len(gl)-1], wl[len(wl)-1])
}

const sampleXML = `<title>示例 <b>文档</b></title>
<h1 seq="auto">一级标题</h1>
<p>普通 <b>加粗</b> <em>斜体</em> <span text-color="red" background-color="light-yellow">彩色</span> <a href="https://a.com/?x=1&amp;y=2">链接</a> <code>code</code><br/>第二行</p>
<ul>
  <li>一<ul><li>一.一</li></ul></li>
  <li>二</li>
</ul>
<ol><li seq="3">三</li><li>四</li></ol>
<checkbox done="true">完成</checkbox>
<blockquote><p>引用</p></blockquote>
<pre lang="rust" caption="例"><code>fn main() {
    println!("&lt;hi&gt;");
}</code></pre>
<hr/>
<callout emoji="💡" background-color="light-orange" border-color="orange"><p>提示</p></callout>
<grid><column width-ratio="0.5"><p>左</p></column><column width-ratio="0.5"><p>右</p></column></grid>
<table><colgroup><col width="120"/><col width="80"/></colgroup><thead><tr><th background-color="light-gray"><p>A</p></th><th><p>B</p></th></tr></thead><tbody><tr><td><p>1</p></td><td><p>2</p></td></tr></tbody></table>
<img href="https://x/y.png" width="300" caption="图"/>
<whiteboard type="blank"></whiteboard>
<p>公式 <latex>E=mc^2</latex> 与 <cite type="user" user-id="ou_1"/></p>
`

func TestIconAndCoverRoundTrip(t *testing.T) {
	x := "<title icon=\"🐱\" cover=\"/a/b &amp; c.jpg\" cover-y=\"0.125\">T</title>\n<p>x</p>\n"
	doc, err := Parse(x)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Icon == nil || *doc.Icon != "🐱" {
		t.Errorf("icon = %v, want 🐱", doc.Icon)
	}
	if doc.Cover == nil || *doc.Cover != (Cover{Src: "/a/b & c.jpg", X: 0, Y: 0.125}) {
		t.Errorf("cover = %+v", doc.Cover)
	}
	requireEqStr(t, Serialize(&doc), x, "icon/cover round-trip")
	doc2, err := Parse("<title>T</title>")
	if err != nil {
		t.Fatal(err)
	}
	requireEqStr(t, Serialize(&doc2), "<title>T</title>\n<p></p>\n", "空文档")
}

func TestParseSample(t *testing.T) {
	doc, err := Parse(sampleXML)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Title.Text != "示例 文档" {
		t.Errorf("title = %q", doc.Title.Text)
	}
	if len(doc.Blocks) != 16 {
		var kinds []string
		for _, b := range doc.Blocks {
			kinds = append(kinds, blockKindName(b.Kind))
		}
		t.Fatalf("blocks = %d (%v), want 16", len(doc.Blocks), kinds)
	}
	if p := doc.Blocks[1].Text.Text; p != "普通 加粗 斜体 彩色 链接 code\n第二行" {
		t.Errorf("p = %q", p)
	}
	if _, ok := doc.Blocks[2].Kind.(Bullet); !ok {
		t.Errorf("blocks[2] 不是 Bullet")
	}
	if len(doc.Blocks[2].Children) != 1 {
		t.Errorf("bullet children = %d, want 1", len(doc.Blocks[2].Children))
	}
	if o, ok := doc.Blocks[4].Kind.(Ordered); !ok || o.Seq == nil || *o.Seq != 3 {
		t.Errorf("blocks[4] = %#v, want Ordered seq=3", doc.Blocks[4].Kind)
	}
	if td, ok := doc.Blocks[6].Kind.(Todo); !ok || !td.Done {
		t.Errorf("blocks[6] = %#v, want Todo done", doc.Blocks[6].Kind)
	}
	if c, ok := doc.Blocks[8].Kind.(Code); ok {
		if c.Lang != "Rust" {
			t.Errorf("lang = %q, want Rust", c.Lang)
		}
	} else {
		t.Errorf("blocks[8] 不是 Code")
	}
	if !strings.Contains(doc.Blocks[8].Text.Text, `println!("<hi>");`) {
		t.Errorf("code = %q", doc.Blocks[8].Text.Text)
	}
	if tb, ok := doc.Blocks[12].Kind.(Table); ok {
		if tb.HeaderRows != 1 || len(tb.ColWidths) != 2 || tb.ColWidths[0] != 120 || tb.ColWidths[1] != 80 {
			t.Errorf("table = %+v", tb)
		}
	} else {
		t.Errorf("blocks[12] 不是 Table")
	}
}

func TestNestedTextChildrenAndFoldsRoundTrip(t *testing.T) {
	src := `<title>T</title><p folded="true">p<p>child</p></p><h2>h<ul><li>x</li></ul></h2><ul><li folded="true">a<ul><li>b</li></ul></li></ul><checkbox done="false" folded="true">t<p>c</p></checkbox>`
	doc, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Blocks) != 4 {
		t.Fatalf("blocks = %d, want 4", len(doc.Blocks))
	}
	for i, b := range doc.Blocks {
		if len(b.Children) == 0 {
			t.Errorf("blocks[%d] 无子级", i)
		}
	}
	wantFolded := []bool{true, false, true, true}
	for i, w := range wantFolded {
		if doc.Blocks[i].Folded != w {
			t.Errorf("blocks[%d].Folded = %v, want %v", i, doc.Blocks[i].Folded, w)
		}
	}
	xml := Serialize(&doc)
	if !strings.Contains(xml, "<p folded=\"true\">p\n  <p>child</p>\n</p>") {
		t.Errorf("嵌套写法不对:\n%s", xml)
	}
	doc2, err := Parse(xml)
	if err != nil {
		t.Fatal(err)
	}
	requireEqStr(t, Serialize(&doc2), xml, "稳定性")
}

func TestBlockBackgroundRoundTrips(t *testing.T) {
	src := `<title>T</title><p background-color="light-blue">p</p><ul><li background-color="light-red">a</li></ul><blockquote background-color="light-gray"><p>q</p></blockquote><hr background-color="light-red"/>`
	doc, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"light-blue", "light-red", "light-gray", ""}
	for i, w := range want {
		got := ""
		if doc.Blocks[i].Bg != nil {
			got = string(*doc.Blocks[i].Bg)
		}
		if got != w {
			t.Errorf("blocks[%d].bg = %q, want %q", i, got, w)
		}
	}
	xml := Serialize(&doc)
	if !strings.Contains(xml, `<p background-color="light-blue">p</p>`) {
		t.Errorf("p 背景丢失:\n%s", xml)
	}
	if !strings.Contains(xml, `<blockquote background-color="light-gray">`) {
		t.Errorf("blockquote 背景丢失:\n%s", xml)
	}
	doc2, err := Parse(xml)
	if err != nil {
		t.Fatal(err)
	}
	requireEqStr(t, Serialize(&doc2), xml, "稳定性")
}

func TestCellContentKeepsItsNewlines(t *testing.T) {
	src := "<title>T</title><table><colgroup><col width=\"100\"/></colgroup><tbody><tr><td><pre lang=\"rust\"><code>fn a() {\n    1\n}</code></pre><p>x<br/>y</p><chat_card id=\"c\">a\nb</chat_card></td></tr></tbody></table><pre lang=\"rust\"><code>top\nlevel</code></pre>"
	doc, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	xml := Serialize(&doc)
	doc2, err := Parse(xml)
	if err != nil {
		t.Fatal(err)
	}
	tb, ok := doc2.Blocks[0].Kind.(Table)
	if !ok {
		t.Fatalf("blocks[0] 不是 Table:\n%s", xml)
	}
	cell := tb.Rows[0][0].Blocks
	if cell[0].Text.Text != "fn a() {\n    1\n}" {
		t.Errorf("cell code = %q", cell[0].Text.Text)
	}
	if cell[1].Text.Text != "x\ny" {
		t.Errorf("cell p = %q", cell[1].Text.Text)
	}
	if raw, ok := cell[2].Kind.(Raw); !ok || !strings.Contains(raw.XML, "a\nb") {
		t.Errorf("cell raw = %#v", cell[2].Kind)
	}
	// 表格外代码保留字面换行。
	if !strings.Contains(xml, "<code>top\nlevel</code>") {
		t.Errorf("顶层代码换行被改写:\n%s", xml)
	}
	requireEqStr(t, Serialize(&doc2), xml, "稳定性")
}

func TestRoundTripIsStable(t *testing.T) {
	doc, err := Parse(sampleXML)
	if err != nil {
		t.Fatal(err)
	}
	xml := Serialize(&doc)
	doc2, err := Parse(xml)
	if err != nil {
		t.Fatal(err)
	}
	requireEqStr(t, Serialize(&doc2), xml, "稳定性")
	if doc.PlainText() != doc2.PlainText() {
		t.Error("plain text 变了")
	}
	for _, s := range []string{
		"<latex>E=mc^2</latex>",
		`<cite type="user" user-id="ou_1"/>`,
		`<whiteboard type="blank"/>`,
		`<span text-color="red" background-color="light-yellow">彩色</span>`,
	} {
		if !strings.Contains(xml, s) {
			t.Errorf("缺少 %s:\n%s", s, xml)
		}
	}
}

// reread 把对象写出再读回。
func reread(o Inline) InlineKind {
	doc, err := Parse("<title>T</title><p>" + o.XML + "</p>")
	if err != nil {
		panic(err)
	}
	return doc.Blocks[0].Text.Runs[0].Marks.Inline.Kind
}

func TestEquationsAreSingleObjects(t *testing.T) {
	doc, err := Parse("<title>T</title><p>a<latex>x&lt;1</latex><latex>x&lt;1</latex>b</p>")
	if err != nil {
		t.Fatal(err)
	}
	rt := &doc.Blocks[0].Text
	if rt.Text != "a\ufffc\ufffcb" {
		t.Errorf("text = %q", rt.Text)
	}
	spans := rt.Spans()
	src, ok := spans[1].Marks.Inline.Equation()
	if !ok || src != "x<1" {
		t.Errorf("equation = %q, %v", src, ok)
	}
	if s := Serialize(&doc); !strings.Contains(s, "a<latex>x&lt;1</latex><latex>x&lt;1</latex>b") {
		t.Errorf("公式写法不对:\n%s", s)
	}
	if got := reread(ToInline(Equation{Src: "a&b<c>"})); got != InlineKind(Equation{Src: "a&b<c>"}) {
		t.Errorf("reread = %#v", got)
	}
	if p := doc.PlainText(); p != "ax<1x<1b" {
		t.Errorf("plain = %q", p)
	}
}

func TestButtonsAreInlineObjects(t *testing.T) {
	doc, err := Parse(`<title>T</title><p>a<button action="OpenLink" src="https://x.cn/?a=1&amp;b" background-color="deep-red">去 &lt;看&gt;</button>b</p><button action="DuplicatePage">复制</button>`)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Blocks) != 2 {
		t.Fatalf("blocks = %d, want 2", len(doc.Blocks))
	}
	rt := &doc.Blocks[0].Text
	if rt.Text != "a\ufffcb" {
		t.Errorf("text = %q", rt.Text)
	}
	var btn Button
	for _, sp := range rt.Spans() {
		if sp.Marks.Inline != nil {
			b, ok := sp.Marks.Inline.Button()
			if !ok {
				t.Fatal("行内对象不是按钮")
			}
			btn = b
		}
	}
	if btn.Text != "去 <看>" || btn.Action != ActionOpenLink || btn.Src != "https://x.cn/?a=1&b" || btn.Color != "deep-red" {
		t.Errorf("button = %+v", btn)
	}
	if got := reread(ToInline(btn)); got != InlineKind(btn) {
		t.Errorf("reread = %#v", got)
	}
	if _, ok := doc.Blocks[1].Text.Spans()[0].Marks.Inline.Button(); !ok {
		t.Error("顶层按钮的行内对象丢失")
	} else if b, _ := doc.Blocks[1].Text.Spans()[0].Marks.Inline.Button(); b.Action != ActionDuplicatePage {
		t.Errorf("顶层按钮 action = %v", b.Action)
	}
	xml := Serialize(&doc)
	doc2, err := Parse(xml)
	if err != nil {
		t.Fatal(err)
	}
	requireEqStr(t, Serialize(&doc2), xml, "稳定性")
}

func TestDocLinksRoundTrip(t *testing.T) {
	doc, err := Parse(`<title>T</title><p>见 <cite type="doc" doc-id="abc" file-type="docx">A &amp; B</cite></p>`)
	if err != nil {
		t.Fatal(err)
	}
	var l InlineDocLink
	for _, sp := range doc.Blocks[0].Text.Spans() {
		if sp.Marks.Inline != nil {
			l, _ = sp.Marks.Inline.DocLink()
		}
	}
	if l != (InlineDocLink{ID: "abc", Title: "A & B"}) {
		t.Errorf("doc link = %+v", l)
	}
	if got := reread(ToInline(l)); got != InlineKind(l) {
		t.Errorf("reread = %#v", got)
	}
	if s := Serialize(&doc); !strings.Contains(s, `<cite type="doc" doc-id="abc" file-type="docx">A &amp; B</cite>`) {
		t.Errorf("doc link 写法不对:\n%s", s)
	}
}

func TestRemindersRoundTrip(t *testing.T) {
	doc, err := Parse(`<title>T</title><p>交付 <time expire-time="1761200000000" notify-time="1761199700000" should-notify="false">x</time> 前</p>`)
	if err != nil {
		t.Fatal(err)
	}
	var r Reminder
	for _, sp := range doc.Blocks[0].Text.Spans() {
		if sp.Marks.Inline != nil {
			r, _ = sp.Marks.Inline.Reminder()
		}
	}
	if r.Expire != 1761200000000 || r.Notify != 1761199700000 || r.ShouldNotify {
		t.Errorf("reminder = %+v", r)
	}
	if got := reread(ToInline(r)); got != InlineKind(r) {
		t.Errorf("reread = %#v", got)
	}
	w := ToInline(Reminder{Expire: r.Expire, Notify: r.Notify, ShouldNotify: true, WholeDay: true})
	if !strings.Contains(w.XML, `is-whole-day="true"`) {
		t.Errorf("xml = %s", w.XML)
	}
	if got := reread(w); got != w.Kind {
		t.Errorf("reread = %#v", got)
	}
	xml := Serialize(&doc)
	doc2, err := Parse(xml)
	if err != nil {
		t.Fatal(err)
	}
	requireEqStr(t, Serialize(&doc2), xml, "稳定性")
}

func TestAdjacentRunsShareWrappers(t *testing.T) {
	xml := `<title>T</title><p><span text-color="red">a <b>b</b> <a href="https://x">c</a></span><b>d<em>e</em></b></p>`
	doc, err := Parse(xml)
	if err != nil {
		t.Fatal(err)
	}
	out := Serialize(&doc)
	want := `<p><span text-color="red">a <b>b</b> <a href="https://x">c</a></span><b>d<em>e</em></b></p>`
	if !strings.Contains(out, want) {
		t.Errorf("包裹元素没有共享:\n%s", out)
	}
}

func TestTableSpansAndHeaderColumn(t *testing.T) {
	x := "<title>T</title>\n<table>\n  <colgroup><col width=\"120\"/><col width=\"100\"/><col width=\"100\"/></colgroup>\n  <thead>\n    <tr><th colspan=\"2\"><p>a</p></th><th><p>c</p></th></tr>\n  </thead>\n  <tbody>\n    <tr><th rowspan=\"2\"><p>d</p></th><td><p>e</p></td><td><p>f</p></td></tr>\n    <tr><td><p>h</p></td><td><p>i</p></td></tr>\n  </tbody>\n</table>\n"
	doc, err := Parse(x)
	if err != nil {
		t.Fatal(err)
	}
	tb, ok := doc.Blocks[0].Kind.(Table)
	if !ok {
		t.Fatal("不是表格")
	}
	if tb.HeaderRows != 1 || !tb.HeaderCol || tb.Ncols() != 3 {
		t.Errorf("table = HeaderRows %d HeaderCol %v ncols %d", tb.HeaderRows, tb.HeaderCol, tb.Ncols())
	}
	if !tb.Rows[0][1].IsCovered() || !tb.Rows[2][0].IsCovered() {
		t.Error("合并槽没有标记 covered")
	}
	if got := tb.Rows[2][1].Blocks[0].Text.Text; got != "h" {
		t.Errorf("rows[2][1] = %q", got)
	}
	out := Serialize(&doc)
	for _, s := range []string{
		`<tr><th rowspan="2"><p>d</p></th><td><p>e</p></td><td><p>f</p></td></tr>`,
		`<tr><th colspan="2"><p>a</p></th><th><p>c</p></th></tr>`,
	} {
		if !strings.Contains(out, s) {
			t.Errorf("缺少 %s:\n%s", s, out)
		}
	}
	again, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	tb2, _ := again.Blocks[0].Kind.(Table)
	if tb2.HeaderRows != 1 || !tb2.HeaderCol || len(tb2.Rows) != 3 {
		t.Errorf("再读 = HeaderRows %d HeaderCol %v rows %d", tb2.HeaderRows, tb2.HeaderCol, len(tb2.Rows))
	}
}

func TestFilesRoundTrip(t *testing.T) {
	x := "<title>T</title>\n<figure view-type=\"Preview\" align=\"center\"><source path=\"/a/报告 &amp; 1.docx\" name=\"报告 &amp; 1.docx\"/></figure>\n<figure view-type=\"Card\"><source token=\"boxcnX\" name=\"b.zip\"/></figure>\n<p>见 <source path=\"/a/c.pdf\" name=\"c.pdf\"/> 附件</p>\n"
	doc, err := Parse(x)
	if err != nil {
		t.Fatal(err)
	}
	f, ok := doc.Blocks[0].Kind.(FileBlock)
	if !ok {
		t.Fatal("blocks[0] 不是文件")
	}
	if f.Src != "/a/报告 & 1.docx" || f.Name != "报告 & 1.docx" || f.View != ViewPreview {
		t.Errorf("file = %+v", f)
	}
	if doc.Blocks[0].Align != AlignCenter {
		t.Errorf("align = %v", doc.Blocks[0].Align)
	}
	f2, _ := doc.Blocks[1].Kind.(FileBlock)
	if f2.SrcKind != "token" || f2.View != ViewCard {
		t.Errorf("file2 = %+v", f2)
	}
	var name string
	for _, sp := range doc.Blocks[2].Text.Spans() {
		if sp.Marks.Inline != nil {
			if inf, ok := sp.Marks.Inline.File(); ok {
				name = inf.Name
			}
		}
	}
	if name != "c.pdf" {
		t.Errorf("inline file name = %q", name)
	}
	requireEqStr(t, Serialize(&doc), x, "files round-trip")
	// 裸 `<source>` 块是以路径自名的卡片。
	doc2, err := Parse("<title>T</title><source path=\"/x/y.mp4\"/>")
	if err != nil {
		t.Fatal(err)
	}
	f3, _ := doc2.Blocks[0].Kind.(FileBlock)
	if f3.Name != "y.mp4" || f3.View != ViewCard {
		t.Errorf("bare source = %+v", f3)
	}
}

// TestFixtureRoundTrips 是 M1 验收：每种块的规范写法，读入再写出逐字节
// 不变。
func TestFixtureRoundTrips(t *testing.T) {
	data, err := os.ReadFile("testdata/all-blocks.xml")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := Parse(string(data))
	if err != nil {
		t.Fatal(err)
	}
	requireEqStr(t, Serialize(&doc), string(data), "fixture round-trip")
	text := doc.PlainText()
	for _, s := range []string{
		"被折叠的段落",
		"尾随空格 \n",
		"\n \n",
		"fn main() {\n    println!(\"<hi> & 'q'\");\n\n}",
		"a = 1\nb = 2",
		"\\frac{1}{2}",
		"空项下的段落",
	} {
		if !strings.Contains(text, s) {
			t.Errorf("%q 不在 plain text 里:\n%q", s, text)
		}
	}
}

// 旧版本给单元格里的块带缩进；那种排版不是内容。
func TestIndentedCellsFromOlderVersions(t *testing.T) {
	doc, err := Parse("<table><tbody><tr><td><ul>  <li>a    <p>b</p>  </li></ul><p>c  <p>d</p></p></td></tr></tbody></table>")
	if err != nil {
		t.Fatal(err)
	}
	x := Serialize(&doc)
	if !strings.Contains(x, "<td><ul><li>a<p>b</p></li></ul><p>c<p>d</p></p></td>") {
		t.Errorf("旧缩进处理不对:\n%s", x)
	}
}

// 未知元素与属性原样保留（大小写也不动）。
func TestUnknownElementsKeepVerbatim(t *testing.T) {
	src := "<title>T</title>\n<MyTag Foo=\"x\" bar=\"1\">a<b>文</b></MyTag>\n<p>k <weird attr=\"&lt;\"/></p>\n"
	doc, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	raw, ok := doc.Blocks[0].Kind.(Raw)
	if !ok || raw.Tag != "MyTag" {
		t.Fatalf("blocks[0] = %#v", doc.Blocks[0].Kind)
	}
	requireEqStr(t, Serialize(&doc), src, "raw round-trip")
	text, link := RawSummary(raw.XML)
	if text != "a文" || link != nil {
		t.Errorf("raw summary = %q, %v", text, link)
	}
}

func TestParseDocumentWrappers(t *testing.T) {
	doc, err := Parse("<doc><title>T</title><p>x</p></doc>")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Title.Text != "T" || len(doc.Blocks) != 1 || doc.Blocks[0].Text.Text != "x" {
		t.Errorf("doc = %+v", doc)
	}
}

func TestParseErrors(t *testing.T) {
	for _, bad := range []string{
		"<p>未闭合",
		"<a></b>",
		`<p>&unknown;</p>`,
	} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) 应该报错", bad)
		}
	}
}

func blockKindName(k BlockKind) string {
	switch k.(type) {
	case Text:
		return "Text"
	case Heading:
		return "Heading"
	case Bullet:
		return "Bullet"
	case Ordered:
		return "Ordered"
	case Todo:
		return "Todo"
	case Code:
		return "Code"
	case Quote:
		return "Quote"
	case Callout:
		return "Callout"
	case Divider:
		return "Divider"
	case Image:
		return "Image"
	case Table:
		return "Table"
	case Grid:
		return "Grid"
	case Column:
		return "Column"
	case SubPageList:
		return "SubPageList"
	case Countdown:
		return "Countdown"
	case FileBlock:
		return "File"
	case Raw:
		return "Raw"
	}
	return "?"
}

// TestLibraryRoundTrips 对拍真实文档库：
// FDL_ROUNDTRIP_DIR=<library>/docs go test -run Library -v
func TestLibraryRoundTrips(t *testing.T) {
	dir := os.Getenv("FDL_ROUNDTRIP_DIR")
	if dir == "" {
		t.Skip("需要 FDL_ROUNDTRIP_DIR")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, ent := range entries {
		if !strings.HasSuffix(ent.Name(), ".xml") {
			continue
		}
		src, err := os.ReadFile(dir + "/" + ent.Name())
		if err != nil {
			t.Fatal(err)
		}
		doc, err := Parse(string(src))
		if err != nil {
			t.Fatalf("%s: %v", ent.Name(), err)
		}
		x := Serialize(&doc)
		again, err := Parse(x)
		if err != nil {
			t.Fatalf("%s: %v", ent.Name(), err)
		}
		requireEqStr(t, Serialize(&again), x, ent.Name()+" 不稳定")
		if again.PlainText() != doc.PlainText() {
			t.Errorf("%s 丢了文本", ent.Name())
		}
	}
}
