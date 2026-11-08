package fdl

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// 随机编辑 fuzz（对拍 editor/fuzz_tests.rs）：像视图那样驱动 Core（一个
// 选区，然后一条编辑命令），每步后查模型不变量，最后存盘往返和完整
// undo/redo。确定性：失败时打印种子与步骤可重放。

const fuzzSeedDoc = `<title>随机 <b>编辑</b></title>
<h1>标题一</h1>
<p>普通 <b>加粗</b> <em>斜体</em> <code>code</code> 😀👍🏽 中文<br/>第二行</p>
<h2 folded="true">可折叠</h2>
<p>折叠内容</p>
<ul><li>一<ul><li>一.一</li><li>一.二</li></ul></li><li>二</li></ul>
<ol><li seq="3">三</li><li>四</li></ol>
<checkbox done="true">完成</checkbox>
<blockquote><p>引用一</p><p>引用二</p></blockquote>
<pre lang="rust"><code>fn main() {
    let s = "é";
}</code></pre>
<hr/>
<callout emoji="💡"><p>提示</p><ul><li>提示列表</li></ul></callout>
<grid><column width-ratio="0.5"><p>左</p></column><column width-ratio="0.5"><p>右</p><p>右二</p></column></grid>
<table><colgroup><col width="120"/><col width="80"/><col width="80"/></colgroup><thead><tr><th><p>A</p></th><th><p>B</p></th><th><p>C</p></th></tr></thead><tbody><tr><td colspan="2" rowspan="2"><p>合并</p><ul><li>格内列表</li></ul></td><td><p>3</p></td></tr><tr><td><p>6</p></td></tr><tr><td><p>7</p></td><td><p>8</p></td><td><pre lang="text"><code>a
b</code></pre></td></tr></tbody></table>
<p>公式 <latex>E=mc^2</latex> 结束</p>
<p></p>
`

var fuzzTexts = []string{"a", "中文", "😀", " ", "ab cd", "é", "👍🏽", "x"}
var fuzzPastes = []string{"一\n二", "a\n\nb\nc", "\n", "😀\n中", "line"}

type fuzzRng struct{ s uint64 }

func (r *fuzzRng) next() uint64 {
	// xorshift64*
	r.s ^= r.s >> 12
	r.s ^= r.s << 25
	r.s ^= r.s >> 27
	return r.s * 0x2545f4914f6cdd1d
}

func (r *fuzzRng) below(n int) int {
	if n < 1 {
		n = 1
	}
	return int(r.next() % uint64(n))
}

func (r *fuzzRng) pick(xs []string) string { return xs[r.below(len(xs))] }

func (r *fuzzRng) chance(percent uint64) bool { return r.next()%100 < percent }

// offsetIn 是 unit 文本里的字符边界偏移（原子块为 0）。
func offsetIn(c *Core, id BlockID, rng *fuzzRng) int {
	t := c.TextOf(id)
	if t == nil {
		return 0
	}
	o := rng.below(t.Len() + 1)
	for o > 0 && o < len(t.Text) && !utf8.RuneStart(t.Text[o]) {
		o--
	}
	return o
}

// randomSelection 是鼠标和键盘会产生的一元内的光标或范围。
func randomSelection(c *Core, rng *fuzzRng) {
	us := c.Units()
	a := us[rng.below(len(us))]
	var same []Unit
	for _, u := range us {
		if u.Scope == a.Scope {
			same = append(same, u)
		}
	}
	b := a
	if !rng.chance(60) {
		b = same[rng.below(len(same))]
	}
	c.BlockSel = nil
	c.CellSel = nil
	c.Sel = Selection{
		Anchor: NewPos(a.ID, offsetIn(c, a.ID, rng)),
		Head:   NewPos(b.ID, offsetIn(c, b.ID, rng)),
	}
	c.FixSelection()
}

func randomKind(rng *fuzzRng) BlockKind {
	switch rng.below(7) {
	case 0:
		return Text{}
	case 1:
		return Heading{Level: uint8(1 + rng.below(9))}
	case 2:
		return Bullet{}
	case 3:
		return Ordered{}
	case 4:
		return Todo{}
	case 5:
		return Quote{}
	default:
		return Code{Lang: "plaintext"}
	}
}

func docTables(doc *Document) []BlockID {
	var out []BlockID
	doc.ForEachBlock(func(b *Block) {
		if _, ok := b.Kind.(Table); ok {
			out = append(out, b.ID)
		}
	})
	return out
}

// fuzzStep 是一条编辑命令；返回名字供失败报告。
func fuzzStep(c *Core, rng *fuzzRng) string {
	if rng.chance(70) {
		randomSelection(c, rng)
	}
	if c.BlockSel != nil {
		return blockModeStep(c, rng)
	}
	us := c.Units()
	any := func() BlockID { return us[rng.below(len(us))].ID }
	switch n := rng.below(23); {
	case n <= 2:
		s := rng.pick(fuzzTexts)
		c.InsertText(s)
		return fmt.Sprintf("insert_text(%q)", s)
	case n == 3:
		c.Enter()
		return "enter"
	case n == 4 || n == 5:
		c.Backspace()
		return "backspace"
	case n == 6:
		c.DeleteForward()
		return "delete_forward"
	case n == 7:
		c.DeleteWordBack()
		return "delete_word_back"
	case n == 8:
		c.Indent()
		return "indent"
	case n == 9:
		c.Outdent()
		return "outdent"
	case n == 10:
		ids := c.SelectedBlocks()
		kind := randomKind(rng)
		c.TurnInto(ids, kind)
		return fmt.Sprintf("turn_into(%v)", ids)
	case n == 11:
		switch rng.below(3) {
		case 0:
			c.ToggleMark(func(m Marks) bool { return m.Bold }, func(m *Marks, v bool) { m.Bold = v })
		case 1:
			c.ToggleMark(func(m Marks) bool { return m.Italic }, func(m *Marks, v bool) { m.Italic = v })
		default:
			c.ToggleMark(func(m Marks) bool { return m.Code }, func(m *Marks, v bool) { m.Code = v })
		}
		return "toggle_mark"
	case n == 12:
		c.SelectAll()
		if rng.chance(50) {
			c.SelectAll()
		}
		if rng.chance(50) {
			c.DeleteSelectedBlocks()
			return "select_all+delete_selected_blocks"
		}
		return "select_all"
	case n == 13:
		id := any()
		c.SelectBlock(id)
		c.DeleteSelectedBlocks()
		return fmt.Sprintf("delete_selected_blocks(%d)", id)
	case n == 14:
		fwd := rng.chance(50)
		c.CellStep(fwd)
		return fmt.Sprintf("cell_step(%v)", fwd)
	case n == 15:
		s := rng.pick(fuzzPastes)
		c.PasteText(s)
		return fmt.Sprintf("paste_text(%q)", s)
	case n == 16:
		c.Undo()
		return "undo"
	case n == 17:
		c.Redo()
		return "redo"
	case n == 18:
		id := any()
		if c.CanFold(id) {
			c.ToggleFold(id)
		}
		return fmt.Sprintf("toggle_fold(%d)", id)
	case n == 19:
		a, b := any(), any()
		places := []DropPlace{DropBefore, DropAfter, DropFirstChild}
		c.MoveBlocks([]BlockID{a}, b, places[rng.below(3)])
		return fmt.Sprintf("move_blocks(%d, %d)", a, b)
	case n == 20:
		return tableStep(c, rng)
	case n == 21:
		return objectStep(c, rng)
	default:
		c.TrailingLine()
		return "trailing_line"
	}
}

// blockModeStep 只接受块命令（视图忽略文本命令）。
func blockModeStep(c *Core, rng *fuzzRng) string {
	ids := c.BlockSelIDs()
	switch n := rng.below(8); {
	case n <= 1:
		c.DeleteSelectedBlocks()
		return fmt.Sprintf("block mode: delete %v", ids)
	case n == 2:
		down := rng.chance(50)
		c.BlockSelMove(down, rng.chance(50))
		return fmt.Sprintf("block mode: move selection (down %v)", down)
	case n == 3:
		kind := randomKind(rng)
		c.TurnInto(ids, kind)
		return fmt.Sprintf("block mode: turn_into(%v)", ids)
	case n == 4:
		c.SelectAll()
		return "block mode: select_all"
	case n == 5:
		c.Undo()
		return "block mode: undo"
	default:
		// 粘贴和回车把块模式留在选区末尾。
		if len(ids) > 0 {
			last := ids[len(ids)-1]
			c.ClearBlockSel()
			c.CaretToEndOf(last)
		}
		s := rng.pick(fuzzPastes)
		c.PasteText(s)
		return fmt.Sprintf("block mode: paste_text(%q) after %v", s, ids)
	}
}

func tableStep(c *Core, rng *fuzzRng) string {
	// 只操作屏幕上的表（没被折叠标题藏住）。
	vis := map[BlockID]bool{}
	for _, b := range c.blockOrder(true) {
		vis[b] = true
	}
	var ts []BlockID
	for _, t := range docTables(&c.Doc) {
		if vis[t] {
			ts = append(ts, t)
		}
	}
	if len(ts) == 0 {
		return "table: none"
	}
	table := ts[rng.below(len(ts))]
	t := c.Table(table)
	nr, nc := len(t.Rows), t.Ncols()
	slot := func() [2]int { return [2]int{rng.below(nr), rng.below(nc)} }
	kinds := []CellSelKind{CellSelRange, CellSelRows, CellSelCols}
	s := CellSel{Table: table, Anchor: slot(), Head: slot(), Kind: kinds[rng.below(3)]}
	c.SelectCells(s)
	what := ""
	switch rng.below(6) {
	case 0:
		c.TableMerge()
		what = "merge"
	case 1:
		c.TableUnmerge()
		what = "unmerge"
	case 2:
		c.TableDeleteSelected()
		what = "delete_selected"
	case 3:
		cols := rng.chance(50)
		if cols {
			c.TableInsert(table, true, rng.below(nc+1))
		} else {
			c.TableInsert(table, false, rng.below(nr+1))
		}
		what = "insert"
	case 4:
		c.TableMoveSelected(rng.below(max(nr, nc) + 1))
		what = "move_selected"
	default:
		c.TableClearCells()
		what = "clear_cells"
	}
	return fmt.Sprintf("table_%s(%+v)", what, s)
}

// objectStep：光标处插行内对象，或编辑其下的对象。
func objectStep(c *Core, rng *fuzzRng) string {
	p := c.Sel.Head
	switch rng.below(5) {
	case 0:
		c.InsertEquation(rng.pick([]string{"x^2", "a<b", ""}))
		return "insert_equation"
	case 1:
		actions := []ButtonAction{ActionDuplicatePage, ActionFollowPage}
		c.InsertButton(NewButton(actions[rng.below(2)]))
		return "insert_button"
	case 2:
		c.InsertReminder(Reminder{Expire: 1700000000000, Notify: 1700000000000, ShouldNotify: rng.chance(50), WholeDay: rng.chance(50)})
		return "insert_reminder"
	case 3:
		c.InsertDocLink(InlineDocLink{ID: "doc1", Title: "文档 & <一>"})
		return "insert_doc_link"
	default:
		c.SetEquationSource(p, rng.pick([]string{"y", `\frac{1}{2}`, "x^2"}))
		return fmt.Sprintf("set_equation_source(%+v)", p)
	}
}

// ------------------------------------------------------------------ 不变量

func fuzzCheckText(t *RichText, at string) error {
	o := 0
	for i, r := range t.Runs {
		if r.Len == 0 {
			return fmt.Errorf("%s: 零长度 run %d", at, i)
		}
		o += r.Len
		if o > len(t.Text) || (o < len(t.Text) && !utf8.RuneStart(t.Text[o])) {
			return fmt.Errorf("%s: run %d 结尾不在字符边界", at, i)
		}
		if r.Marks.Inline != nil {
			for _, ch := range t.Text[o-r.Len : o] {
				if ch != 0xfffc {
					return fmt.Errorf("%s: 对象 run %d 混有文字", at, i)
				}
			}
		}
		if i > 0 && t.Runs[i-1].Marks.Equal(r.Marks) {
			return fmt.Errorf("%s: run %d 与前一 run 样式相同", at, i)
		}
	}
	if o != len(t.Text) {
		return fmt.Errorf("%s: runs 覆盖 %d / %d 字节", at, o, len(t.Text))
	}
	return nil
}

func fuzzCheckTable(t *Table, at string) error {
	nr, nc := len(t.Rows), t.Ncols()
	if nr == 0 || nc == 0 {
		return fmt.Errorf("%s: 空表 %d×%d", at, nr, nc)
	}
	count := make([][]int, nr)
	for i := range count {
		count[i] = make([]int, nc)
	}
	for r, row := range t.Rows {
		if len(row) != nc {
			return fmt.Errorf("%s: 行 %d 有 %d / %d 格", at, r, len(row), nc)
		}
		for c := range row {
			cell := &row[c]
			if cell.IsCovered() {
				if cell.Rowspan != 0 || len(cell.Blocks) != 0 {
					return fmt.Errorf("%s: 被盖住的槽 (%d,%d) 带内容或跨度", at, r, c)
				}
				continue
			}
			rs, cs := int(cell.Rowspan), int(cell.Colspan)
			if rs == 0 || r+rs > nr || c+cs > nc {
				return fmt.Errorf("%s: 格 (%d,%d) 跨度 %d×%d 越界 %d×%d", at, r, c, rs, cs, nr, nc)
			}
			if len(cell.Blocks) == 0 {
				return fmt.Errorf("%s: 格 (%d,%d) 没有块", at, r, c)
			}
			for rr := r; rr < r+rs; rr++ {
				for cc := c; cc < c+cs; cc++ {
					count[rr][cc]++
				}
			}
		}
	}
	for r, row := range count {
		for c, n := range row {
			if n != 1 {
				return fmt.Errorf("%s: 槽 (%d,%d) 被 %d 个格盖住", at, r, c, n)
			}
		}
	}
	return nil
}

func fuzzCheckBlocks(blocks []Block, parent BlockKind, ids map[BlockID]bool) error {
	for i := range blocks {
		b := &blocks[i]
		at := fmt.Sprintf("块 %d (%d)", b.ID, kindTag(b.Kind))
		if ids[b.ID] {
			return fmt.Errorf("%s: 重复 id", at)
		}
		ids[b.ID] = true
		if err := fuzzCheckText(&b.Text, at); err != nil {
			return err
		}
		_, parentIsGrid := parent.(Grid)
		_, isColumn := b.Kind.(Column)
		if isColumn != parentIsGrid {
			return fmt.Errorf("%s: 栏在 grid 外，或 grid 里不是栏", at)
		}
		switch b.Kind.(type) {
		case Grid, Column:
			if len(b.Children) == 0 {
				return fmt.Errorf("%s: 空的 grid 或栏", at)
			}
		case Code:
			for _, r := range b.Text.Runs {
				if !r.Marks.Equal(Marks{}) {
					return fmt.Errorf("%s: 代码里有样式或行内对象", at)
				}
			}
		}
		if len(b.Children) > 0 && !Nests(b.Kind) && !IsContainer(b.Kind) {
			return fmt.Errorf("%s: 带子块的块存不下它们", at)
		}
		if tb, ok := b.Kind.(Table); ok {
			if err := fuzzCheckTable(&tb, at); err != nil {
				return err
			}
			for _, cell := range tb.Cells() {
				if err := fuzzCheckBlocks(cell.Cell.Blocks, nil, ids); err != nil {
					return err
				}
			}
		}
		if err := fuzzCheckBlocks(b.Children, b.Kind, ids); err != nil {
			return err
		}
	}
	return nil
}

func docEqual(a, b *Document) bool {
	if !runsEqual(a.Title.Runs, b.Title.Runs) || a.Title.Text != b.Title.Text {
		return false
	}
	if (a.Icon == nil) != (b.Icon == nil) || (a.Icon != nil && *a.Icon != *b.Icon) {
		return false
	}
	if (a.Cover == nil) != (b.Cover == nil) || (a.Cover != nil && *a.Cover != *b.Cover) {
		return false
	}
	return blocksEqual(a.Blocks, b.Blocks)
}

func fuzzCheck(c *Core) error {
	if len(c.Doc.Blocks) == 0 {
		return fmt.Errorf("文档没有块")
	}
	if err := fuzzCheckText(&c.Doc.Title, "标题"); err != nil {
		return err
	}
	if err := fuzzCheckBlocks(c.Doc.Blocks, nil, map[BlockID]bool{}); err != nil {
		return err
	}
	us := c.Units()
	if bs := c.BlockSel; bs != nil {
		// 块模式：文本选区不显示；它的两端要显示。
		for _, id := range []BlockID{bs.Anchor, bs.Head} {
			if c.Block(id) == nil || c.IsHidden(id) {
				return fmt.Errorf("块选择端 %d 不存在或被藏住", id)
			}
		}
		return nil
	}
	a, h := c.Sel.Anchor, c.Sel.Head
	for _, p := range []Pos{a, h} {
		var u *Unit
		for i := range us {
			if us[i].ID == p.Block {
				u = &us[i]
				break
			}
		}
		if u == nil {
			return fmt.Errorf("选区端 %+v 不是 unit", p)
		}
		t := c.TextOf(p.Block)
		if t != nil && (p.Offset > t.Len() || (p.Offset < len(t.Text) && !utf8.RuneStart(t.Text[p.Offset]))) {
			return fmt.Errorf("选区端 %+v 越界 %q", p, t.Text)
		}
		if t == nil && u.Text {
			return fmt.Errorf("文本 unit %+v 没有文本", p)
		}
	}
	vis := map[BlockID]bool{}
	for _, b := range c.blockOrder(true) {
		vis[b] = true
	}
	var hiddenMismatch *BlockID
	c.Doc.ForEachBlock(func(b *Block) {
		if _, isColumn := b.Kind.(Column); isColumn {
			return
		}
		if c.IsHidden(b.ID) == vis[b.ID] {
			if hiddenMismatch == nil {
				id := b.ID
				hiddenMismatch = &id
			}
		}
	})
	if hiddenMismatch != nil {
		return fmt.Errorf("is_hidden(%d) 与 block_order 不一致", *hiddenMismatch)
	}
	scope := func(id BlockID) BlockID {
		for i := range us {
			if us[i].ID == id {
				return us[i].Scope
			}
		}
		return 0
	}
	if c.CellSel == nil && scope(a.Block) != scope(h.Block) {
		return fmt.Errorf("选区 %+v..%+v 跨过表格边界", a, h)
	}
	return nil
}

func fuzzRoundTrip(c *Core) error {
	// 已知丢失：单元格里块文字与嵌套块之间的空格读回时按旧版缩进丢掉
	//（docs/FORMAT.md）。
	var trimBeforeNested func(blocks []Block, inCell bool)
	trimBeforeNested = func(blocks []Block, inCell bool) {
		for i := range blocks {
			b := &blocks[i]
			keep := len(strings.TrimRight(b.Text.Text, " \t"))
			if inCell && len(b.Children) > 0 && keep < b.Text.Len() {
				empty := RichText{}
				b.Text.Replace(keep, b.Text.Len(), &empty)
			}
			trimBeforeNested(b.Children, inCell)
			if tb, ok := b.Kind.(Table); ok {
				for r := range tb.Rows {
					for cc := range tb.Rows[r] {
						trimBeforeNested(tb.Rows[r][cc].Blocks, true)
					}
				}
			}
		}
	}
	doc := c.Doc.Clone()
	trimBeforeNested(doc.Blocks, false)
	x := Serialize(&doc)
	back, err := Parse(x)
	if err != nil {
		return fmt.Errorf("保存的文档不能再解析: %v\n%s", err, x)
	}
	y := Serialize(&back)
	if x != y {
		return fmt.Errorf("存→读→存不稳定:\n--- 第一次\n%s\n--- 第二次\n%s", x, y)
	}
	return nil
}

// fuzzUnfolded 是清掉折叠的保存形态：折叠不入编辑史。
func fuzzUnfolded(doc *Document) string {
	d := doc.Clone()
	var walk func(blocks []Block)
	walk = func(blocks []Block) {
		for i := range blocks {
			blocks[i].Folded = false
			walk(blocks[i].Children)
		}
	}
	walk(d.Blocks)
	return Serialize(&d)
}

func fuzzDiff(a, b string) string {
	as, bs := strings.Split(a, "\n"), strings.Split(b, "\n")
	i := 0
	for i < len(as) && i < len(bs) && as[i] == bs[i] {
		i++
	}
	near := func(v []string) string {
		lo := max(0, i-2)
		hi := min(i+3, len(v))
		return strings.Join(v[lo:hi], "\n")
	}
	return fmt.Sprintf("第 %d 行起:\n--- 期望\n%s\n--- 实际\n%s", i+1, near(as), near(bs))
}

func fuzzRun(t *testing.T, seed uint64, steps int) {
	t.Helper()
	rng := &fuzzRng{s: seed*0x9e3779b97f4a7c15 | 1}
	doc, err := Parse(fuzzSeedDoc)
	if err != nil {
		t.Fatal(err)
	}
	c := NewCore(doc)
	start := fuzzUnfolded(&c.Doc)
	var log []string
	fail := func(e error) {
		tail := log
		if len(tail) > 12 {
			tail = tail[len(tail)-12:]
		}
		t.Fatalf("种子 %d，第 %d 步: %v\n最后几步:\n  %s\n文档:\n%s", seed, len(log), e, strings.Join(tail, "\n  "), Serialize(&c.Doc))
	}
	for i := 0; i < steps; i++ {
		before := c.Doc.Clone()
		generation := c.Generation
		log = append(log, fuzzStep(c, rng))
		// 视图按 generation 缓存每帧的工作。
		if c.Generation == generation && !docEqual(&c.Doc, &before) {
			fail(fmt.Errorf("文档变了但 generation 没动"))
		}
		// Editor::changed：每条命令后。
		c.RevealCaret()
		if err := fuzzCheck(c); err != nil {
			fail(err)
		}
		if i%25 == 0 {
			if err := fuzzRoundTrip(c); err != nil {
				fail(err)
			}
		}
	}
	if err := fuzzRoundTrip(c); err != nil {
		fail(err)
	}
	// 每个改动都可撤销（步数 < undoSteps），redo 原样重放。
	for c.Redo() {
	}
	end := fuzzUnfolded(&c.Doc)
	for c.Undo() {
	}
	if got := fuzzUnfolded(&c.Doc); got != start {
		fail(fmt.Errorf("全部撤销没有还原种子文档，%s", fuzzDiff(start, got)))
	}
	for c.Redo() {
	}
	if got := fuzzUnfolded(&c.Doc); got != end {
		fail(fmt.Errorf("全部重做没有还原最终文档，%s", fuzzDiff(end, got)))
	}
}

func TestRandomEditsKeepTheModelValid(t *testing.T) {
	// FDL_FUZZ_SEEDS=n 跑种子 0..n，FDL_FUZZ_STEPS=m 每个种子 m 步（要
	// 低于 undoSteps 才能验撤销）；FDL_FUZZ_SEED=k 重放一个种子。
	env := func(k string) (uint64, bool) {
		v, err := strconv.ParseUint(os.Getenv(k), 10, 64)
		return v, err == nil
	}
	var from, to uint64 = 0, 40
	if k, ok := env("FDL_FUZZ_SEED"); ok {
		from, to = k, k+1
	} else if n, ok := env("FDL_FUZZ_SEEDS"); ok {
		to = n
	}
	steps := uint64(300)
	if s, ok := env("FDL_FUZZ_STEPS"); ok {
		steps = s
	}
	for seed := from; seed < to; seed++ {
		fuzzRun(t, seed, int(steps))
	}
}
