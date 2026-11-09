package fdl

import (
	"strings"
	"testing"
)

func coreDoc(xmlSrc string) *Core {
	doc, err := Parse(xmlSrc)
	if err != nil {
		panic(err)
	}
	return NewCore(doc)
}

func coreXML(c *Core) string { return Serialize(&c.Doc) }

func unitID(c *Core, n int) BlockID { return c.Units()[n].ID }

// typeChars 模拟逐字符输入（空格后跑块级快捷键，-/空格后跑分割线，每
// 个字符后跑行内规则）。
func typeChars(c *Core, s string) {
	for _, ch := range s {
		c.InsertText(string(ch))
		if ch == ' ' {
			c.MarkdownBlockShortcut()
		}
		if ch == '-' || ch == ' ' {
			c.MarkdownDivider()
		}
		c.MarkdownInlineShortcut()
	}
}

func TestSelectAllTwiceDeletesAFoldedTail(t *testing.T) {
	c := coreDoc(`<title>T</title><h1>a</h1><h2 folded="true">b</h2><p>hidden</p>`)
	c.Sel = Caret(NewPos(unitID(c, 1), 0))
	c.SelectAll()
	c.SelectAll()
	if got := len(c.BlockSelIDs()); got != 3 {
		t.Fatalf("block_sel_ids = %d, want 3", got)
	}
	c.DeleteSelectedBlocks()
	if strings.Contains(coreXML(c), "hidden") {
		t.Errorf("藏住的块没删:\n%s", coreXML(c))
	}
}

func TestEnterOnAFoldedHeadingNeverHidesTheNewLine(t *testing.T) {
	c := coreDoc(`<title>T</title><h1>a</h1><p>x</p><h1 folded="true">b</h1><p>hidden</p>`)
	b := unitID(c, 3)
	c.Sel = Caret(NewPos(b, 1))
	c.Enter()
	found := false
	for _, u := range c.Units() {
		if u.ID == c.Sel.Head.Block {
			found = true
		}
	}
	if !found {
		t.Error("新块不在 units 里")
	}
	if blk := c.Block(b); blk == nil || !blk.Folded {
		t.Error("原块应保持折叠")
	}
	if !strings.HasSuffix(coreXML(c), "<p>hidden</p>\n<h1></h1>\n") {
		t.Errorf("落点不对:\n%s", coreXML(c))
	}
}

func TestCodeKeepsEquationsAsSourceAndNeverHoldsChildren(t *testing.T) {
	c := coreDoc(`<title>T</title><p>e <latex>x^2</latex><ul><li>kid</li></ul></p><p>b <b>bold</b></p>`)
	p := unitID(c, 1)
	c.TurnInto([]BlockID{p}, Code{Lang: "plaintext"})
	if !strings.HasPrefix(coreXML(c), "<title>T</title>\n<pre lang=\"plaintext\"><code>e x^2</code></pre>\n<ul>") {
		t.Errorf("转代码不对:\n%s", coreXML(c))
	}
	// 删进代码：尾巴按纯文本并入，其子块跟在代码块后。
	c = coreDoc(`<title>T</title><pre lang="plaintext"><code>c</code></pre><p>b <b>bold</b><ul><li>kid</li></ul></p>`)
	c.Sel = Selection{Anchor: NewPos(unitID(c, 1), 1), Head: NewPos(unitID(c, 2), 2)}
	c.DeleteSelection()
	if !strings.HasPrefix(coreXML(c), "<title>T</title>\n<pre lang=\"plaintext\"><code>cbold</code></pre>\n<ul>") {
		t.Errorf("跨块删除不对:\n%s", coreXML(c))
	}
	// 拖放不能把块塞进带不了子块的块里。
	c = coreDoc("<title>T</title><hr/><p>a</p>")
	hr, a := unitID(c, 1), unitID(c, 2)
	if c.MoveBlocks([]BlockID{a}, hr, DropFirstChild) {
		t.Error("FirstChild 到 hr 应返回 false")
	}
}

func TestUndoKeepsFoldsAndIcon(t *testing.T) {
	c := coreDoc(`<title>T</title><h1>h</h1><p>a</p>`)
	h := unitID(c, 1)
	c.Sel = Caret(NewPos(unitID(c, 2), 1))
	c.InsertText("b")
	c.ToggleFold(h)
	icon := "🐱"
	c.Doc.Icon = &icon
	c.Undo()
	if txt := c.TextOf(unitID(c, 1)); txt == nil || txt.Text != "h" {
		t.Errorf("h1 文本 = %v", txt)
	}
	if blk := c.Block(h); blk == nil || !blk.Folded {
		t.Error("折叠应保留")
	}
	if c.Doc.Icon == nil || *c.Doc.Icon != "🐱" {
		t.Error("图标应保留")
	}
	if !strings.Contains(coreXML(c), "<p>a</p>") {
		t.Errorf("撤销后:\n%s", coreXML(c))
	}
}

func TestMoveBlocksReordersNestsAndUndoes(t *testing.T) {
	c := coreDoc(`<title>T</title><p>a</p><p>b</p><ul><li>c</li></ul>`)
	a, b, li := unitID(c, 1), unitID(c, 2), unitID(c, 3)
	if !c.MoveBlocks([]BlockID{a}, b, DropAfter) {
		t.Fatal("第一次移动应成功")
	}
	if !strings.HasPrefix(coreXML(c), "<title>T</title>\n<p>b</p>\n<p>a</p>") {
		t.Errorf("排序不对:\n%s", coreXML(c))
	}
	if !c.MoveBlocks([]BlockID{a, b}, li, DropFirstChild) {
		t.Fatal("嵌套移动应成功")
	}
	liEl := c.Block(li)
	var kidIDs []BlockID
	for i := range liEl.Children {
		kidIDs = append(kidIDs, liEl.Children[i].ID)
	}
	if len(kidIDs) != 2 || kidIDs[0] != a || kidIDs[1] != b {
		t.Errorf("子块顺序 = %v", kidIDs)
	}
	// 拖进自己子树，或原地不动：都不发生。
	if c.MoveBlocks([]BlockID{li}, a, DropAfter) {
		t.Error("拖进自己子树应 false")
	}
	if c.MoveBlocks([]BlockID{b}, a, DropAfter) {
		t.Error("原地不动应 false")
	}
	c.Undo()
	c.Undo()
	if !strings.HasPrefix(coreXML(c), "<title>T</title>\n<p>a</p>\n<p>b</p>") {
		t.Errorf("撤销后:\n%s", coreXML(c))
	}
}

func TestCanFoldFollowsFeishu(t *testing.T) {
	c := coreDoc("<title>T</title><h2>h</h2><p>plain</p><p>parent<p>child</p></p><ul><li>leaf</li><li folded=\"true\">gone</li></ul>" +
		"<callout emoji=\"💡\"><ul><li>x<ul><li>y</li></ul></li></ul></callout>" +
		"<table><tbody><tr><td><h2>in cell</h2></td></tr></tbody></table>")
	var find func(list []Block, text string) (BlockID, bool)
	find = func(list []Block, text string) (BlockID, bool) {
		for i := range list {
			b := &list[i]
			if b.Text.Text == text {
				return b.ID, true
			}
			if id, ok := find(b.Children, text); ok {
				return id, true
			}
			if tb, isTable := b.Kind.(Table); isTable {
				for _, row := range tb.Rows {
					for _, cell := range row {
						if id, ok := find(cell.Blocks, text); ok {
							return id, true
						}
					}
				}
			}
		}
		return 0, false
	}
	want := map[string]bool{
		"h": true, "plain": false, "parent": true, "leaf": false,
		"gone": true, "x": false, "in cell": false,
	}
	for text, can := range want {
		id, ok := find(c.Doc.Blocks, text)
		if !ok {
			t.Fatalf("找不到 %q", text)
		}
		if got := c.CanFold(id); got != can {
			t.Errorf("can_fold(%q) = %v, want %v", text, got, can)
		}
	}
}

func TestUndoHistoryIsBounded(t *testing.T) {
	c := coreDoc("<title>T</title><p>a</p>")
	for i := 0; i < undoSteps+10; i++ {
		c.Checkpoint(EditOther)
	}
	if len(c.undo) != undoSteps {
		t.Fatalf("undo = %d, want %d", len(c.undo), undoSteps)
	}
	// 大快照：内存说了算，砍到下限。
	for i := 0; i < undoMinSteps+5; i++ {
		c.pushUndo(snapshot{doc: NewDocument(), sel: c.Sel, size: undoBytes / 4})
	}
	if len(c.undo) != undoMinSteps {
		t.Fatalf("undo = %d, want %d", len(c.undo), undoMinSteps)
	}
	if !c.Undo() {
		t.Error("undo 应成功")
	}
}

func TestUnfoldingToAHeadingIsSaved(t *testing.T) {
	c := coreDoc(`<title>T</title><h1 folded="true">A</h1><h2>B</h2><p>b1</p>`)
	b := c.Doc.Blocks[1].ID
	c.Dirty = false
	c.UnfoldTo(b)
	if !c.Dirty {
		t.Error("展开是编辑，应置 dirty")
	}
	if got := coreXML(c); got != "<title>T</title>\n<h1>A</h1>\n<h2>B</h2>\n<p>b1</p>\n" {
		t.Errorf("xml = %s", got)
	}
	// 没藏东西：不用保存。
	c.Dirty = false
	c.UnfoldTo(b)
	if c.Dirty {
		t.Error("二次展开不应 dirty")
	}
}

func TestFoldingMovesHiddenCaretAndEnterSkipsSection(t *testing.T) {
	c := coreDoc(`<title>T</title><h1>A</h1><p>a1</p><h2>B</h2><p>b1</p><h1>C</h1>`)
	a, b1 := unitID(c, 1), unitID(c, 4)
	c.Sel = Caret(NewPos(b1, 1))
	c.ToggleFold(a)
	if !c.Dirty {
		t.Error("fold 应 dirty")
	}
	if c.Sel.Head != (Pos{Block: a, Offset: 1}) {
		t.Errorf("光标 = %v", c.Sel.Head)
	}
	if got := len(c.Units()); got != 3 {
		t.Fatalf("units = %d, want 3", got)
	}
	c.Enter()
	want := "<title>T</title>\n<h1 folded=\"true\">A</h1>\n<p>a1</p>\n<h2>B</h2>\n<p>b1</p>\n<h1></h1>\n<h1>C</h1>\n"
	if got := coreXML(c); got != want {
		t.Errorf("xml:\n got: %s\nwant: %s", got, want)
	}
}

func TestEnterSplitsAndBackspaceMerges(t *testing.T) {
	c := coreDoc("<title>T</title><p>hello world</p>")
	b := unitID(c, 1)
	c.Sel = Caret(NewPos(b, 5))
	c.Enter()
	if got := len(c.Units()); got != 3 {
		t.Fatalf("units = %d, want 3", got)
	}
	if txt := c.TextOf(c.Sel.Head.Block); txt == nil || txt.Text != " world" {
		t.Errorf("尾段 = %v", txt)
	}
	c.Backspace()
	if got := coreXML(c); got != "<title>T</title>\n<p>hello world</p>\n" {
		t.Errorf("xml = %s", got)
	}
	if c.Sel.Head != (Pos{Block: b, Offset: 5}) {
		t.Errorf("光标 = %v", c.Sel.Head)
	}
}

func TestCodeEnterKeepsIndentAndDownLeavesLastBlock(t *testing.T) {
	c := coreDoc("<title>T</title><pre lang=\"py\"><code>def f():\n    x</code></pre>")
	b := unitID(c, 1)
	c.Sel = Caret(NewPos(b, c.LenOf(b)))
	c.Enter()
	c.InsertText("y")
	if !strings.Contains(coreXML(c), "<pre lang=\"python\"><code>def f():\n    x\n    y</code></pre>") {
		t.Errorf("缩进不对:\n%s", coreXML(c))
	}
	tr := c.TrailingLine()
	if tr == b {
		t.Error("尾行应是新块")
	}
	if c.TrailingLine() != tr {
		t.Error("尾行应复用")
	}
	if !strings.HasSuffix(coreXML(c), "</pre>\n<p></p>\n") {
		t.Errorf("尾段不对:\n%s", coreXML(c))
	}
}

func TestBackspaceStaysInsideTableCells(t *testing.T) {
	src := "<title>T</title><p>ab</p><table><tr><td><p>x</p></td><td><p></p><p>y</p></td></tr></table>"
	c := coreDoc(src)
	us := c.Units()
	// 第一格开头：怎么按都没事。
	c.Sel = Caret(NewPos(us[2].ID, 0))
	c.Backspace()
	c.Backspace()
	if got := coreXML(c); got != coreXML(coreDoc(src)) {
		t.Errorf("文档变了:\n%s", got)
	}
	if c.InBlockMode() {
		t.Error("不应进入块模式")
	}
	// 单元格空首行消失，光标到下一行。
	c.Sel = Caret(NewPos(us[3].ID, 0))
	c.Backspace()
	if c.Sel.Head != (Pos{Block: us[4].ID, Offset: 0}) {
		t.Errorf("光标 = %v", c.Sel.Head)
	}
	if !strings.Contains(coreXML(c), "<td><p>y</p></td>") {
		t.Errorf("空行没删:\n%s", coreXML(c))
	}
	// 再按也原地不动。
	c.Backspace()
	if !strings.Contains(coreXML(c), "<td><p>x</p></td><td><p>y</p></td>") {
		t.Errorf("不应继续删:\n%s", coreXML(c))
	}
	if c.Sel.Head != (Pos{Block: us[4].ID, Offset: 0}) || c.InBlockMode() {
		t.Errorf("光标/模式不对: %v %v", c.Sel.Head, c.InBlockMode())
	}
	// 格末 Delete 不拉下一格。
	c.Sel = Caret(NewPos(us[2].ID, 1))
	c.DeleteForward()
	if !strings.Contains(coreXML(c), "<td><p>x</p></td><td><p>y</p></td>") {
		t.Errorf("Delete 越界:\n%s", coreXML(c))
	}
}

func TestBackspaceInEmptyCellSelectsThenDeletesTable(t *testing.T) {
	c := coreDoc("<title>T</title><p>ab</p><table><tr><td><p>x</p></td><td><p></p></td></tr></table><p>cd</p>")
	us := c.Units()
	c.Sel = Caret(NewPos(us[3].ID, 0))
	c.Backspace()
	if !c.InBlockMode() {
		t.Fatal("应选中表格")
	}
	if !strings.Contains(coreXML(c), "<table>") {
		t.Errorf("表格没了:\n%s", coreXML(c))
	}
	c.DeleteSelectedBlocks()
	if strings.Contains(coreXML(c), "<table>") {
		t.Errorf("表格应被删:\n%s", coreXML(c))
	}
	// 嵌套表先删内层，留下外层。
	c = coreDoc("<title>T</title><table><tr><td><table><tr><td><p></p></td></tr></table></td><td><p>y</p></td></tr></table>")
	us = c.Units()
	c.Sel = Caret(NewPos(us[1].ID, 0))
	c.Backspace()
	c.DeleteSelectedBlocks()
	if n := strings.Count(coreXML(c), "<table>"); n != 1 {
		t.Errorf("剩 %d 张表:\n%s", n, coreXML(c))
	}
}

func TestBackspaceAfterTableSelectsThenDeletesIt(t *testing.T) {
	c := coreDoc("<title>T</title><p>ab</p><table><tr><td><p>x</p></td><td><p></p></td></tr></table><p></p>")
	last := c.Units()
	id := last[len(last)-1].ID
	c.Sel = Caret(NewPos(id, 0))
	// 空行消失，表格被选中。
	c.Backspace()
	if !c.InBlockMode() {
		t.Fatal("应选中表格")
	}
	if !strings.Contains(coreXML(c), "<table>") {
		t.Errorf("表格没了:\n%s", coreXML(c))
	}
	if strings.HasSuffix(coreXML(c), "<p></p>\n") {
		t.Errorf("空行没删:\n%s", coreXML(c))
	}
	// 块模式再退格：表格删除，光标落在 "ab" 上。
	c.DeleteSelectedBlocks()
	if strings.Contains(coreXML(c), "<table>") {
		t.Errorf("表格应被删:\n%s", coreXML(c))
	}
	if txt := c.TextOf(c.Sel.Head.Block); txt == nil || txt.Text != "ab" {
		t.Errorf("光标块 = %v", txt)
	}
	if c.Sel.Head.Offset != 2 {
		t.Errorf("offset = %d", c.Sel.Head.Offset)
	}
	// 继续删穿过文本。
	c.Backspace()
	if txt := c.TextOf(c.Sel.Head.Block); txt == nil || txt.Text != "a" {
		t.Errorf("继续删 = %v", txt)
	}
	// 表后非空行保留；只删表格。
	c = coreDoc("<title>T</title><table><tr><td><p>x</p></td></tr></table><p>keep</p>")
	units := c.Units()
	c.Sel = Caret(NewPos(units[len(units)-1].ID, 0))
	c.Backspace()
	if !c.InBlockMode() {
		t.Fatal("应选中表格")
	}
	c.DeleteSelectedBlocks()
	if got := coreXML(c); got != "<title>T</title>\n<p>keep</p>\n" {
		t.Errorf("xml = %s", got)
	}
}

func TestDeleteBeforeTableSelectsIt(t *testing.T) {
	c := coreDoc("<title>T</title><p>ab</p><table><tr><td><p>x</p></td></tr></table>")
	a := unitID(c, 1)
	c.Sel = Caret(NewPos(a, 2))
	c.DeleteForward()
	if !c.InBlockMode() {
		t.Fatal("应选中表格")
	}
	c.DeleteSelectedBlocks()
	if strings.Contains(coreXML(c), "<table>") {
		t.Errorf("表格应被删:\n%s", coreXML(c))
	}
}

func TestTabStepsThroughTableCells(t *testing.T) {
	c := coreDoc("<title>T</title><table><tr><td><p></p></td><td><p></p></td></tr></table><p></p>")
	a := unitID(c, 1)
	c.Sel = Caret(NewPos(a, 0))
	c.InsertText("a")
	if !c.CellStep(true) {
		t.Fatal("cell_step")
	}
	c.InsertText("b")
	if !c.CellStep(true) {
		t.Fatal("cell_step")
	}
	c.InsertText("c")
	if !c.CellStep(false) {
		t.Fatal("cell_step")
	}
	if !c.CellStep(false) {
		t.Fatal("cell_step")
	}
	c.InsertText("X")
	out := coreXML(c)
	for _, want := range []string{"<td><p>aX</p></td>", "<td><p>b</p></td>", "<td><p>c</p></td>"} {
		if !strings.Contains(out, want) {
			t.Errorf("缺少 %s:\n%s", want, out)
		}
	}
	if n := strings.Count(out, "<tr>"); n != 2 {
		t.Errorf("行数 = %d:\n%s", n, out)
	}
	units := c.Units()
	c.Sel = Caret(NewPos(units[len(units)-1].ID, 0))
	if c.CellStep(true) {
		t.Error("表外应 false")
	}
}

func TestTypedURLBecomesLink(t *testing.T) {
	c := coreDoc("<title>T</title><p></p>")
	b := unitID(c, 1)
	c.Sel = Caret(NewPos(b, 0))
	c.InsertText("见 www.a.com/x).")
	c.InsertText(" ")
	if !c.Autolink() {
		t.Fatal("autolink")
	}
	c.InsertText("https:// ")
	if c.Autolink() {
		t.Error("不应再 autolink")
	}
	if !strings.Contains(coreXML(c), `<p>见 <a href="https://www.a.com/x">www.a.com/x</a>). https:// </p>`) {
		t.Errorf("xml:\n%s", coreXML(c))
	}
}

func TestListEnterOnEmptyExits(t *testing.T) {
	c := coreDoc("<title>T</title><ul><li>a</li></ul>")
	b := unitID(c, 1)
	c.Sel = Caret(NewPos(b, 1))
	c.Enter()
	c.Enter()
	if !strings.Contains(coreXML(c), "<ul>\n  <li>a</li>\n</ul>\n<p></p>") {
		t.Errorf("xml:\n%s", coreXML(c))
	}
}

func TestMarkdownShortcuts(t *testing.T) {
	c := coreDoc("<title>T</title><p></p>")
	b := unitID(c, 1)
	c.Sel = Caret(NewPos(b, 0))
	for _, ch := range []string{"#", "#", " "} {
		c.InsertText(ch)
	}
	if !c.MarkdownBlockShortcut() {
		t.Fatal("块级快捷键")
	}
	if h, ok := c.KindOf(b).(Heading); !ok || h.Level != 2 {
		t.Fatalf("kind = %#v, want h2", c.KindOf(b))
	}
	c.InsertText("a**b*")
	if c.MarkdownInlineShortcut() {
		t.Error("不应转换")
	}
	c.InsertText("*")
	if !c.MarkdownInlineShortcut() {
		t.Fatal("应转换")
	}
	if got := coreXML(c); got != "<title>T</title>\n<h2>a<b>b</b></h2>\n" {
		t.Errorf("xml = %s", got)
	}
	c.Undo()
	if txt := c.TextOf(b); txt == nil || txt.Text != "a**b**" {
		t.Errorf("撤销后 = %v", txt)
	}
}

func TestMarkdownEquations(t *testing.T) {
	cases := []struct {
		typed string
		src   string // 空：不应转换
	}{
		{"x $a^2$", "a^2"},
		{"x $$b$$", "b"},
		{"x$a$", ""},
		{"$ $", ""},
	}
	for _, tc := range cases {
		c := coreDoc("<title>T</title><p></p>")
		b := unitID(c, 1)
		c.Sel = Caret(NewPos(b, 0))
		typeChars(c, tc.typed)
		txt := c.TextOf(b)
		if tc.src != "" {
			if txt.Text != "x "+EquationRune {
				t.Errorf("%q: text = %q", tc.typed, txt.Text)
			}
			if src, ok := c.EquationSource(NewPos(b, 2)); !ok || src != tc.src {
				t.Errorf("%q: src = %q %v", tc.typed, src, ok)
			}
			if c.Sel.Head.Offset != len(txt.Text) {
				t.Errorf("%q: offset = %d", tc.typed, c.Sel.Head.Offset)
			}
		} else if strings.Contains(txt.Text, EquationRune) {
			t.Errorf("%q: 不应转换: %q", tc.typed, txt.Text)
		}
	}
}

func TestEquationEditsCoalesce(t *testing.T) {
	c := coreDoc("<title>T</title><p>ab</p>")
	b := unitID(c, 1)
	c.Sel = Caret(NewPos(b, 1))
	p := c.InsertEquation("")
	if p == nil {
		t.Fatal("insert_equation")
	}
	c.BreakCoalescing()
	for _, s := range []string{"x", "x^", "x^2"} {
		c.SetEquationSource(*p, s)
	}
	if src, ok := c.EquationSource(*p); !ok || src != "x^2" {
		t.Fatalf("src = %q %v", src, ok)
	}
	c.Undo()
	if src, ok := c.EquationSource(*p); !ok || src != "" {
		t.Fatalf("undo 后 src = %q %v", src, ok)
	}
	c.Undo()
	if txt := c.TextOf(b); txt == nil || txt.Text != "ab" {
		t.Errorf("二次 undo 后 = %v", txt)
	}
}

func TestTabIndentsUnderPrevious(t *testing.T) {
	c := coreDoc("<title>T</title><ul><li>a</li><li>b</li><li>c</li></ul>")
	b := unitID(c, 2)
	c.Sel = Caret(NewPos(b, 0))
	c.Indent()
	if len(c.Doc.Blocks) != 2 {
		t.Fatalf("blocks = %d, want 2", len(c.Doc.Blocks))
	}
	if len(c.Doc.Blocks[0].Children) != 1 {
		t.Errorf("子块 = %d, want 1", len(c.Doc.Blocks[0].Children))
	}
	c.Outdent()
	if len(c.Doc.Blocks) != 3 {
		t.Errorf("blocks = %d, want 3", len(c.Doc.Blocks))
	}
}

func TestCrossBlockDelete(t *testing.T) {
	c := coreDoc("<title>T</title><p>abc</p><hr/><h1>def</h1>")
	a, d := unitID(c, 1), unitID(c, 3)
	c.Sel = Selection{Anchor: NewPos(a, 1), Head: NewPos(d, 2)}
	c.Backspace()
	if got := coreXML(c); got != "<title>T</title>\n<p>af</p>\n" {
		t.Errorf("xml = %s", got)
	}
}

func TestOrderedNumbering(t *testing.T) {
	c := coreDoc(`<title>T</title><ol><li>a</li><li>b</li></ol><p>x</p><ol><li seq="5">c</li><li>d</li></ol>`)
	bl := c.Doc.Blocks
	if got := orderedNumber(bl, 1); got != 2 {
		t.Errorf("number(1) = %d", got)
	}
	if got := orderedNumber(bl, 4); got != 6 {
		t.Errorf("number(4) = %d", got)
	}
	if got := OrderedMarker(3, 1); got != "c." {
		t.Errorf("marker(3,1) = %s", got)
	}
	if got := OrderedMarker(4, 2); got != "iv." {
		t.Errorf("marker(4,2) = %s", got)
	}
}

func TestPasteMultiline(t *testing.T) {
	c := coreDoc("<title>T</title><p>xy</p>")
	a := unitID(c, 1)
	c.Sel = Caret(NewPos(a, 1))
	c.PasteText("1\n2\n3")
	if got := coreXML(c); got != "<title>T</title>\n<p>x1</p>\n<p>2</p>\n<p>3y</p>\n" {
		t.Errorf("xml = %s", got)
	}
	if c.Sel.Head.Offset != 1 {
		t.Errorf("offset = %d", c.Sel.Head.Offset)
	}
}

func TestPasteMarkdownTargets(t *testing.T) {
	// 空段落吃块级规则。
	c := coreDoc("<title>T</title><p></p>")
	c.Sel = Caret(NewPos(unitID(c, 1), 0))
	c.PasteMarkdown("# H\n- a\n**b**")
	if got := coreXML(c); got != "<title>T</title>\n<h1>H</h1>\n<ul>\n  <li>a</li>\n</ul>\n<p><b>b</b></p>\n" {
		t.Errorf("xml = %s", got)
	}
	// 非空的只上行内规则，每行一段。
	c = coreDoc("<title>T</title><p>xy</p>")
	c.Sel = Caret(NewPos(unitID(c, 1), 1))
	c.PasteMarkdown("# H\n**b**")
	if got := coreXML(c); got != "<title>T</title>\n<p>x# H</p>\n<p><b>b</b>y</p>\n" {
		t.Errorf("xml = %s", got)
	}
	// 标题吃第一行；其余从头开始正文。
	c = coreDoc("<title>T</title><p>body</p>")
	c.Sel = Caret(NewPos(TitleID, 1))
	c.PasteMarkdown("A\nB")
	if got := coreXML(c); got != "<title>TA</title>\n<p>B</p>\n<p>body</p>\n" {
		t.Errorf("xml = %s", got)
	}
	if c.Sel.Head != (Pos{Block: unitID(c, 1), Offset: 1}) {
		t.Errorf("光标 = %v", c.Sel.Head)
	}
}

func TestPasteBlocksIntoCodeFlattens(t *testing.T) {
	c := coreDoc("<title>T</title><pre lang=\"python\"><code></code></pre>")
	c.Sel = Caret(NewPos(unitID(c, 1), 0))
	src, err := Parse("<title>x</title><ul><li><b>a</b><ul><li>b</li></ul></li></ul><p>c</p>")
	if err != nil {
		t.Fatal(err)
	}
	c.PasteBlocks(src.Blocks)
	if txt := c.TextOf(unitID(c, 1)); txt == nil || txt.Text != "a\n  b\nc" {
		t.Errorf("code = %q", txt.Text)
	}
}

func TestPasteTableIntoTable(t *testing.T) {
	c := coreDoc("<title>T</title><table><tbody><tr><td><p>1</p></td><td><p>2</p></td></tr></tbody></table>")
	cell := unitID(c, 2)
	c.Sel = Caret(NewPos(cell, 0))
	src, err := Parse("<title>x</title><table><tbody><tr><td><p>a</p></td><td><p>b</p></td></tr><tr><td><p>c</p></td><td><p>d</p></td></tr></tbody></table>")
	if err != nil {
		t.Fatal(err)
	}
	c.PasteBlocks(src.Blocks)
	var tb *Table
	for i := range c.Doc.Blocks {
		if x, ok := c.Doc.Blocks[i].Kind.(Table); ok {
			t := x
			tb = &t
		}
	}
	if tb == nil {
		t.Fatal("没有表格")
	}
	var cells []string
	for _, cr := range tb.Cells() {
		cells = append(cells, cr.Cell.Blocks[0].Text.Text)
	}
	if len(tb.Rows) != 2 || tb.Ncols() != 3 {
		t.Fatalf("形状 = %d×%d", len(tb.Rows), tb.Ncols())
	}
	want := []string{"1", "a", "b", "", "c", "d"}
	for i := range want {
		if cells[i] != want[i] {
			t.Errorf("cell[%d] = %q, want %q", i, cells[i], want[i])
		}
	}
	if c.CellSel == nil {
		t.Error("应选中粘贴的单元格")
	}
	// 其他带表的内容：表被丢弃并提示。
	c = coreDoc("<title>T</title><table><tbody><tr><td><p>1</p></td></tr></tbody></table>")
	c.Sel = Caret(NewPos(unitID(c, 1), 1))
	src, err = Parse("<title>x</title><p>p</p><table><tbody><tr><td><p>a</p></td></tr></tbody></table>")
	if err != nil {
		t.Fatal(err)
	}
	c.PasteBlocks(src.Blocks)
	if c.Notice != "当前位置不支持粘贴表格" {
		t.Errorf("notice = %q", c.Notice)
	}
	if txt := c.TextOf(unitID(c, 1)); txt == nil || txt.Text != "1p" {
		t.Errorf("text = %q", txt.Text)
	}
}

func TestMarkdownBlockTriggers(t *testing.T) {
	cases := []struct{ input, want string }{
		{"- ", "<ul>\n  <li></li>\n</ul>"},
		{"+ ", "<ul>\n  <li></li>\n</ul>"},
		{"3. ", "<ol>\n  <li seq=\"3\"></li>\n</ol>"},
		{"1。 ", "<ol>\n  <li></li>\n</ol>"},
		{"【】 ", "<checkbox"},
		{"> ", "<blockquote>"},
		{"```rust ", "<pre lang=\"rust\""},
		{"--- ", "<hr/>"},
		{"*** ", "<hr/>"},
	}
	for _, tc := range cases {
		c := coreDoc("<title>T</title><p></p>")
		b := unitID(c, 1)
		c.Sel = Caret(NewPos(b, 0))
		typeChars(c, tc.input)
		if !strings.Contains(coreXML(c), tc.want) {
			t.Errorf("%q -> %s", tc.input, coreXML(c))
		}
	}
	// "[x] " 保持字面。
	c := coreDoc("<title>T</title><p></p>")
	c.Sel = Caret(NewPos(unitID(c, 1), 0))
	typeChars(c, "[x] ")
	if !strings.Contains(coreXML(c), "<p>[x] </p>") {
		t.Errorf("xml = %s", coreXML(c))
	}
}

func TestMarkdownInListAndHeading(t *testing.T) {
	// 触发也转换非文本块。
	c := coreDoc("<title>T</title><ul><li></li></ul>")
	b := unitID(c, 1)
	c.Sel = Caret(NewPos(b, 0))
	typeChars(c, "## ")
	if h, ok := c.KindOf(b).(Heading); !ok || h.Level != 2 || h.SeqAuto {
		t.Fatalf("kind = %#v", c.KindOf(b))
	}
	typeChars(c, "1. ")
	if h, ok := c.KindOf(b).(Heading); !ok || h.Level != 2 || !h.SeqAuto {
		t.Errorf("kind = %#v, want h2 auto", c.KindOf(b))
	}
}

func TestMarkdownInlineRules(t *testing.T) {
	cases := []struct{ input, want string }{
		{"a __b__ ", "<b>b</b>"},
		{"a_b_c", "a_b_c"},
		{"_i_", "<em>i</em>"},
		{"***x***", "<b><em>x</em></b>"},
		{"~~s~~", "<del>s</del>"},
		{"~u~", "<u>u</u>"},
		{"`c`", "<code>c</code>"},
		{"a * x*", "a * x*"},
	}
	for _, tc := range cases {
		c := coreDoc("<title>T</title><p></p>")
		c.Sel = Caret(NewPos(unitID(c, 1), 0))
		typeChars(c, tc.input)
		if !strings.Contains(coreXML(c), tc.want) {
			t.Errorf("%q -> %s", tc.input, coreXML(c))
		}
	}
}

func TestToggleTurnIntoQuote(t *testing.T) {
	c := coreDoc("<title>T</title><p>a</p><p>b</p>")
	a, b := unitID(c, 1), unitID(c, 2)
	c.ToggleTurnInto([]BlockID{a, b}, Quote{})
	if len(c.Doc.Blocks) != 1 || len(c.Doc.Blocks[0].Children) != 2 {
		t.Fatalf("blocks = %d", len(c.Doc.Blocks))
	}
	c.ToggleTurnInto([]BlockID{a, b}, Quote{})
	if got := coreXML(c); got != "<title>T</title>\n<p>a</p>\n<p>b</p>\n" {
		t.Errorf("xml = %s", got)
	}
}

func TestHeadingNumbers(t *testing.T) {
	c := coreDoc("<title>T</title><h2 seq=\"auto\">a</h2><h4 seq=\"auto\">b</h4><h4 seq=\"auto\">c</h4>" +
		"<h2 seq=\"auto\">d</h2><h2>e</h2><callout><h3 seq=\"auto\">f</h3></callout><h4 seq=\"auto\">g</h4>")
	o := HeadingOrders(c.Doc.Blocks)
	n := func(i int) string { return o[unitID(c, i)] }
	want := []string{"1.", "1.1", "1.2", "2.", "", "1.", "2.1"}
	for i, w := range want {
		if n(i+1) != w {
			t.Errorf("n(%d) = %q, want %q", i+1, n(i+1), w)
		}
	}
}

func TestBlockSelection(t *testing.T) {
	c := coreDoc("<title>T</title><p>a</p><ul><li>b<ul><li>c</li></ul></li></ul><p>d</p>")
	a, b, cc, d := unitID(c, 1), unitID(c, 2), unitID(c, 3), unitID(c, 4)
	c.SelectBlock(a)
	c.BlockSelMove(true, false)
	if !idEq(c.BlockSelIDs(), []BlockID{b}) {
		t.Errorf("ids = %v", c.BlockSelIDs())
	}
	// 父块上普通 ↓ 跳过它的子块。
	c.BlockSelMove(true, false)
	if !idEq(c.BlockSelIDs(), []BlockID{d}) {
		t.Errorf("ids = %v", c.BlockSelIDs())
	}
	c.BlockSelMove(false, true)
	if !idEq(c.BlockSelIDs(), []BlockID{cc, d}) {
		t.Errorf("ids = %v", c.BlockSelIDs())
	}
	c.BlockSelHorizontal(false)
	if !idEq(c.BlockSelIDs(), []BlockID{b}) {
		t.Errorf("ids = %v", c.BlockSelIDs())
	}
	c.selectRange(a, cc)
	if !idEq(c.BlockSelIDs(), []BlockID{a, b}) {
		t.Errorf("ids = %v", c.BlockSelIDs())
	}
	c.DeleteSelectedBlocks()
	if got := coreXML(c); got != "<title>T</title>\n<p>d</p>\n" {
		t.Errorf("xml = %s", got)
	}
	if c.Sel.Head != (Pos{Block: d, Offset: 0}) {
		t.Errorf("光标 = %v", c.Sel.Head)
	}
}

func TestSelectAllEscalates(t *testing.T) {
	c := coreDoc("<title>T</title><p>x</p><callout><p>ab</p></callout>")
	x, ab := unitID(c, 1), unitID(c, 2)
	callout := c.Doc.Blocks[1].ID
	c.Sel = Caret(NewPos(ab, 1))
	c.SelectAll()
	if got := c.SelectedText(); got != "ab" {
		t.Errorf("text = %q", got)
	}
	c.SelectAll()
	if !idEq(c.BlockSelIDs(), []BlockID{callout}) {
		t.Errorf("ids = %v", c.BlockSelIDs())
	}
	c.SelectAll()
	if !idEq(c.BlockSelIDs(), []BlockID{x, callout}) {
		t.Errorf("ids = %v", c.BlockSelIDs())
	}
}

func TestBlockModeEnter(t *testing.T) {
	c := coreDoc("<title>T</title><p>ab</p><hr/>")
	ab, hr := unitID(c, 1), unitID(c, 2)
	c.selectRange(ab, hr)
	c.EnterFromBlockSel()
	if c.Sel.Head != (Pos{Block: ab, Offset: 2}) {
		t.Errorf("光标 = %v", c.Sel.Head)
	}
	c.SelectBlock(hr)
	c.EnterFromBlockSel()
	if got := len(c.Units()); got != 4 {
		t.Errorf("units = %d, want 4", got)
	}
}

func idEq(got, want []BlockID) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// 对拍 file_title_keeps_current_and_history_grapheme_boundaries。
func TestFileTitleKeepsCurrentAndHistoryGraphemeBoundaries(t *testing.T) {
	for _, title := range []string{"中文", "👩‍💻笔记", "e\u0301cole"} {
		c := coreDoc("<title>abc</title><p>body</p>")
		c.Sel = Caret(NewPos(TitleID, 1))
		c.Checkpoint(EditOther)
		c.ReceiveFileTitle(title)
		if c.Sel.Head.Offset != 0 {
			t.Errorf("%q: sel offset = %d, want 0", title, c.Sel.Head.Offset)
		}
		if back := c.undo[len(c.undo)-1]; back.sel.Head.Offset != 0 {
			t.Errorf("%q: undo offset = %d, want 0", title, back.sel.Head.Offset)
		}
		c.Backspace()
		if c.Doc.Title.Text != title {
			t.Errorf("%q: backspace 后 title = %q", title, c.Doc.Title.Text)
		}
		if !c.Undo() {
			t.Fatal("undo")
		}
		if c.Doc.Title.Text != title {
			t.Errorf("%q: undo 后 title = %q", title, c.Doc.Title.Text)
		}
	}
}
