package fdl

import (
	"fmt"
	"testing"
)

func coreWithTable(nr, nc int) (*Core, BlockID) {
	rows := make([][]TableCell, nr)
	for r := range rows {
		rows[r] = make([]TableCell, nc)
		for c := range rows[r] {
			rows[r][c] = TableCell{Blocks: []Block{BlockText(fmt.Sprintf("%d%d", r, c))}, Colspan: 1, Rowspan: 1}
		}
	}
	b := NewBlock(Table{ColWidths: repeatF32(TableDefaultWidth, nc), Rows: rows}, RichText{})
	id := b.ID
	doc := NewDocument()
	doc.Title = Plain("t")
	doc.Blocks = []Block{b, BlockText("after")}
	return NewCore(doc), id
}

func tableText(c *Core, id BlockID, r, col int) string {
	var parts []string
	for _, b := range c.Table(id).Rows[r][col].Blocks {
		parts = append(parts, b.Text.Text)
	}
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += "|"
		}
		out += p
	}
	return out
}

func TestBarSelectionCoversWholeRowsAndColumns(t *testing.T) {
	c, id := coreWithTable(3, 3)
	c.SelectCells(CellSel{Table: id, Anchor: [2]int{1, 0}, Head: [2]int{2, 0}, Kind: CellSelRows})
	if got := len(c.SelectedCells()); got != 6 {
		t.Fatalf("cells = %d, want 6", got)
	}
	if txt := c.TextOf(c.Sel.Head.Block); txt == nil || txt.Text != "10" {
		t.Errorf("光标块 = %v", txt)
	}
	c.SelectCells(CellSel{Table: id, Anchor: [2]int{0, 2}, Head: [2]int{0, 2}, Kind: CellSelCols})
	want := [][2]int{{0, 2}, {1, 2}, {2, 2}}
	got := c.SelectedCells()
	if len(got) != len(want) {
		t.Fatalf("cells = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("cell[%d] = %v", i, got[i])
		}
	}
}

func TestDeleteRowsAndWholeTable(t *testing.T) {
	c, id := coreWithTable(3, 2)
	c.SelectCells(CellSel{Table: id, Anchor: [2]int{0, 0}, Head: [2]int{1, 0}, Kind: CellSelRows})
	c.TableDeleteSelected()
	if got := len(c.Table(id).Rows); got != 1 {
		t.Fatalf("rows = %d", got)
	}
	if got := tableText(c, id, 0, 0); got != "20" {
		t.Errorf("(0,0) = %q", got)
	}
	c.SelectCells(CellSel{Table: id, Anchor: [2]int{0, 0}, Head: [2]int{0, 1}, Kind: CellSelCols})
	c.TableDeleteSelected()
	if c.Table(id) != nil {
		t.Error("表格应被删掉")
	}
	if txt := c.TextOf(c.Sel.Head.Block); txt == nil || txt.Text != "after" {
		t.Errorf("光标块 = %v", txt)
	}
}

func TestMergeAndSplit(t *testing.T) {
	c, id := coreWithTable(2, 3)
	c.SelectCells(CellSel{Table: id, Anchor: [2]int{0, 0}, Head: [2]int{1, 1}, Kind: CellSelRange})
	c.TableMerge()
	tb := c.Table(id)
	if tb.Rows[0][0].Rowspan != 2 || tb.Rows[0][0].Colspan != 2 {
		t.Fatalf("跨度 = %d×%d", tb.Rows[0][0].Rowspan, tb.Rows[0][0].Colspan)
	}
	if got := tableText(c, id, 0, 0); got != "00|01|10|11" {
		t.Errorf("内容 = %q", got)
	}
	if tid, r, cc, ok := c.MergedSelected(); !ok || tid != id || r != 0 || cc != 0 {
		t.Fatalf("merged = %v %d %d %v", tid, r, cc, ok)
	}
	c.TableUnmerge()
	if got := len(c.SelectedCells()); got != 4 {
		t.Fatalf("cells = %d", got)
	}
	if got := tableText(c, id, 1, 1); got != "" {
		t.Errorf("(1,1) = %q", got)
	}
	if !(c.Undo() && c.Undo()) {
		t.Fatal("undo")
	}
	if got := tableText(c, id, 1, 1); got != "11" {
		t.Errorf("undo 后 (1,1) = %q", got)
	}
}

func TestInsertPutsCaretInNewCells(t *testing.T) {
	c, id := coreWithTable(2, 2)
	c.TableInsert(id, true, 1)
	if got := c.Table(id).Ncols(); got != 3 {
		t.Fatalf("ncols = %d", got)
	}
	if tid, r, cc, ok := c.CellOf(c.Sel.Head.Block); !ok || tid != id || r != 0 || cc != 1 {
		t.Errorf("cell_of = %v %d %d %v", tid, r, cc, ok)
	}
	c.TableInsert(id, false, 2)
	if got := len(c.Table(id).Rows); got != 3 {
		t.Fatalf("rows = %d", got)
	}
	if _, r, _, ok := c.CellOf(c.Sel.Head.Block); !ok || r != 2 {
		t.Errorf("row = %d", r)
	}
}

func TestCellStylesWidthsAndHeaders(t *testing.T) {
	c, id := coreWithTable(2, 2)
	c.SelectCells(CellSel{Table: id, Anchor: [2]int{0, 0}, Head: [2]int{0, 1}, Kind: CellSelRange})
	blue := ColorName("light-blue")
	mid := "middle"
	c.TableSetBG(&blue)
	c.TableSetValign(&mid)
	tb := c.Table(id)
	if tb.Rows[0][1].Bg == nil || tb.Rows[1][0].Bg != nil {
		t.Error("背景范围不对")
	}
	if tb.Rows[0][0].Valign == nil || *tb.Rows[0][0].Valign != "middle" {
		t.Error("垂直对齐没设置")
	}
	if !c.TableSetWidth(id, 0, 20) {
		t.Fatal("set_width")
	}
	if got := c.Table(id).ColWidths[0]; got != TableMinWidth {
		t.Errorf("宽度 = %v, want %v", got, TableMinWidth)
	}
	c.TableDistribute(id)
	if w := c.Table(id).ColWidths; w[0] != 75 || w[1] != 75 {
		t.Errorf("均分 = %v", w)
	}
	c.TableToggleHeaderRow(id)
	c.TableToggleHeaderCol(id)
	tb = c.Table(id)
	if tb.HeaderRows != 1 || !tb.HeaderCol {
		t.Errorf("表头 = %d %v", tb.HeaderRows, tb.HeaderCol)
	}
}

func TestClearAndCopyCells(t *testing.T) {
	c, id := coreWithTable(2, 2)
	c.SelectCells(CellSel{Table: id, Anchor: [2]int{1, 0}, Head: [2]int{1, 1}, Kind: CellSelRange})
	copied := c.SelectedCellsContent()
	tb, ok := copied[0].Kind.(Table)
	if !ok {
		t.Fatal("复制的不是表格")
	}
	if len(tb.Rows) != 1 || tb.Ncols() != 2 {
		t.Fatalf("形状 = %d×%d", len(tb.Rows), tb.Ncols())
	}
	if got := tb.Rows[0][1].Blocks[0].Text.Text; got != "11" {
		t.Errorf("(0,1) = %q", got)
	}
	c.TableClearCells()
	if got := tableText(c, id, 1, 1); got != "" {
		t.Errorf("(1,1) = %q", got)
	}
	if got := tableText(c, id, 0, 1); got != "01" {
		t.Errorf("(0,1) = %q", got)
	}
	if c.CellSel == nil {
		t.Error("选区应保留")
	}
	if got := len(c.CellTextBlocks()); got != 2 {
		t.Errorf("text blocks = %d", got)
	}
}

func TestMoveRowsAndColumns(t *testing.T) {
	c, id := coreWithTable(4, 3)
	c.SelectCells(CellSel{Table: id, Anchor: [2]int{0, 0}, Head: [2]int{1, 0}, Kind: CellSelRows})
	// 挪到自己旁边：不动。
	if c.TableMoveSelected(2) {
		t.Error("应返回 false")
	}
	if !c.TableMoveSelected(4) {
		t.Fatal("move")
	}
	if got := tableText(c, id, 0, 0); got != "20" {
		t.Errorf("(0,0) = %q", got)
	}
	if got := tableText(c, id, 2, 1); got != "01" {
		t.Errorf("(2,1) = %q", got)
	}
	if got := tableText(c, id, 3, 2); got != "12" {
		t.Errorf("(3,2) = %q", got)
	}
	if _, r, ok := c.CellRange(); !ok || r.Top != 2 || r.Bottom != 3 {
		t.Errorf("range = %v", r)
	}
	c.SelectCells(CellSel{Table: id, Anchor: [2]int{0, 2}, Head: [2]int{0, 2}, Kind: CellSelCols})
	c.TableSetWidth(id, 2, 180)
	if !c.TableMoveSelected(0) {
		t.Fatal("move col")
	}
	tb := c.Table(id)
	if tb.ColWidths[0] != 180 || tb.ColWidths[1] != 100 || tb.ColWidths[2] != 100 {
		t.Errorf("宽度 = %v", tb.ColWidths)
	}
	if got := tableText(c, id, 0, 0); got != "22" {
		t.Errorf("(0,0) = %q", got)
	}
	if got := tableText(c, id, 0, 1); got != "20" {
		t.Errorf("(0,1) = %q", got)
	}
	if _, r, ok := c.CellRange(); !ok || r.Left != 0 || r.Right != 0 {
		t.Errorf("range = %v", r)
	}
	if !c.Undo() {
		t.Fatal("undo")
	}
	if got := tableText(c, id, 0, 0); got != "20" {
		t.Errorf("undo 后 (0,0) = %q", got)
	}
}

func TestMovedRowsKeepMergedCells(t *testing.T) {
	c, id := coreWithTable(4, 2)
	c.SelectCells(CellSel{Table: id, Anchor: [2]int{2, 0}, Head: [2]int{3, 0}, Kind: CellSelRange})
	c.TableMerge()
	// 选第 3 行会带上合并单元格跨过的第 2 行。
	c.SelectCells(CellSel{Table: id, Anchor: [2]int{3, 0}, Head: [2]int{3, 0}, Kind: CellSelRows})
	if !c.TableMoveSelected(0) {
		t.Fatal("move")
	}
	tb := c.Table(id)
	if tb.Rows[0][0].Rowspan != 2 || !tb.Rows[1][0].IsCovered() {
		t.Fatalf("合并 = %d, covered = %v", tb.Rows[0][0].Rowspan, tb.Rows[1][0].IsCovered())
	}
	if got := tableText(c, id, 0, 0); got != "20|30" {
		t.Errorf("(0,0) = %q", got)
	}
	if got := tableText(c, id, 0, 1); got != "21" {
		t.Errorf("(0,1) = %q", got)
	}
	if got := tableText(c, id, 2, 0); got != "00" {
		t.Errorf("(2,0) = %q", got)
	}
}
