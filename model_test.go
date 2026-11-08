package fdl

import "testing"

func TestRichTextPushMergeAndSlice(t *testing.T) {
	var rt RichText
	plain := Marks{}
	bold := Marks{Bold: true}
	rt.Push("hello ", plain)
	rt.Push("world", bold)
	rt.Push("!", bold)
	if rt.Text != "hello world!" {
		t.Errorf("text = %q", rt.Text)
	}
	if len(rt.Runs) != 2 {
		t.Errorf("runs = %+v, want 2 段", rt.Runs)
	}
	if rt.Len() != 12 || rt.IsEmpty() {
		t.Errorf("len = %d", rt.Len())
	}
	sub := rt.Slice(6, 11)
	if sub.Text != "world" || len(sub.Runs) != 1 || !sub.Runs[0].Marks.Bold {
		t.Errorf("slice = %+v", sub)
	}
	// 区间夹到字符边界：emoji 中间切开不 panic。
	if s := Plain("😀😀").Slice(1, 5); s.Text != "😀" {
		t.Errorf("slice emoji = %q", s.Text)
	}
	if p := Plain(""); !p.IsEmpty() || p.Runs != nil {
		t.Errorf("plain(\"\") = %+v", p)
	}
}

func TestRichTextPlainText(t *testing.T) {
	var rt RichText
	rt.Push("公式 ", Marks{})
	rt.Push("\ufffc", Marks{Inline: &Inline{Kind: Equation{Src: "a+b"}, XML: "<latex>a+b</latex>"}})
	rt.Push(" 与提及", Marks{Inline: &Inline{Kind: OtherInline{Tag: "cite"}, XML: "<cite/>"}})
	if got := rt.PlainText(); got != "公式 a+b 与提及" {
		t.Errorf("plain = %q", got)
	}
}

func TestMarksEqual(t *testing.T) {
	link := "https://x"
	a := Marks{Bold: true, Link: &link}
	b := Marks{Bold: true, Link: &link}
	if !a.Equal(b) {
		t.Error("同值样式应当相等")
	}
	// 行内对象只比较 XML。
	x1 := Marks{Inline: &Inline{Kind: Equation{Src: "a"}, XML: "<latex>a</latex>"}}
	x2 := Marks{Inline: &Inline{Kind: OtherInline{Tag: "latex"}, XML: "<latex>a</latex>"}}
	if !x1.Equal(x2) {
		t.Error("行内对象应按 XML 比较")
	}
	x3 := Marks{Inline: &Inline{Kind: Equation{Src: "b"}, XML: "<latex>b</latex>"}}
	if x1.Equal(x3) || x1.Equal(Marks{}) {
		t.Error("不同行内对象不应相等")
	}
}

func TestDisplayTitle(t *testing.T) {
	if DisplayTitle("  ") != Untitled {
		t.Error("空标题应是未命名")
	}
	if DisplayTitle(" 笔记 ") != "笔记" {
		t.Error("应去掉边缘空白")
	}
}

func TestBlockIDsAreFresh(t *testing.T) {
	a, b := BlockText("a"), BlockText("b")
	if a.ID == b.ID || a.ID == 0 {
		t.Errorf("ids = %d, %d", a.ID, b.ID)
	}
}

func TestCountdownRoundTrip(t *testing.T) {
	x := "<title>T</title>\n<countdown duration=\"3600\" start-time=\"1760000000000\" notify=\"true\" color=\"#FF8800\" timing=\"date\" days=\"1\" hours=\"2\" minutes=\"3\" seconds=\"4\" date=\"2026-11-08\" time=\"09:30\"/>\n"
	doc, err := Parse(x)
	if err != nil {
		t.Fatal(err)
	}
	c, ok := doc.Blocks[0].Kind.(Countdown)
	if !ok {
		t.Fatalf("kind = %#v", doc.Blocks[0].Kind)
	}
	if c.Duration != 3600 || c.Start != 1760000000000 || !c.Notify || c.Color != "#FF8800" || !c.ByDate {
		t.Errorf("countdown = %+v", c)
	}
	if c.Dhms != [4]uint32{1, 2, 3, 4} || c.Date != "2026-11-08" || c.Time != "09:30" {
		t.Errorf("countdown = %+v", c)
	}
	got := Serialize(&doc)
	if got != x {
		t.Errorf("round-trip:\n got: %s\nwant: %s", got, x)
	}
	if r := c.Remaining(c.Start + 500*1000); r != 3100 {
		t.Errorf("remaining = %d, want 3100", r)
	}
}

func TestDocumentHelpers(t *testing.T) {
	doc, err := Parse(`<title>文档</title><p>见 <cite type="doc" doc-id="d1">目标</cite></p><img href="a.png"/>`)
	if err != nil {
		t.Fatal(err)
	}
	if !doc.IsBlank() == false {
		t.Error("非空文档不应是 blank")
	}
	links := doc.DocLinks()
	if len(links) != 1 || links[0].Target != "d1" {
		t.Fatalf("doc links = %+v", links)
	}
	if len(links[0].Line) == 0 || links[0].Line[0].Text != "见 " {
		t.Errorf("line = %+v", links[0].Line)
	}
	srcs := doc.AssetSources()
	if len(srcs) != 1 || srcs[0] != "a.png" {
		t.Errorf("assets = %v", srcs)
	}
	if n := NewDocument(); !n.IsBlank() {
		t.Error("新文档应是 blank")
	}
}
