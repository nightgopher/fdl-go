package fdl

// 表格操作（对拍 editor/table_ops.rs）：单元格/行/列选择，表格工具栏
// 和块菜单背后的编辑。

// CellSelKind 是单元格选区的形状。
type CellSelKind uint8

const (
	CellSelRange CellSelKind = iota // 拖出的矩形
	CellSelRows                     // 整行（行栏）
	CellSelCols                     // 整列（列栏）
)

// cellSel 是一张表里从 Anchor 到 Head 的选中单元格（网格槽位）。
type cellSel struct {
	Table  BlockID
	Anchor [2]int
	Head   [2]int
	Kind   CellSelKind
}

// CellSel 是对外暴露的单元格选区类型。
type CellSelAlias = cellSel

// Raw 是合并单元格撑开前选中的槽位。
func (s *cellSel) Raw(t *Table) CellRange {
	nr := max(len(t.Rows), 1)
	nc := max(t.Ncols(), 1)
	clamp := func(rc [2]int) [2]int {
		return [2]int{min(rc[0], nr-1), min(rc[1], nc-1)}
	}
	r := CellRangeOf(clamp(s.Anchor)[0], clamp(s.Anchor)[1], clamp(s.Head)[0], clamp(s.Head)[1])
	switch s.Kind {
	case CellSelRows:
		r.Left, r.Right = 0, nc-1
	case CellSelCols:
		r.Top, r.Bottom = 0, nr-1
	}
	return r
}

// firstLeaf：光标进入这些块时落点。
func firstLeaf(blocks []Block) BlockID {
	if len(blocks) == 0 {
		return 0
	}
	b := &blocks[0]
	if HasText(b.Kind) {
		return b.ID
	}
	if tb, ok := b.Kind.(Table); ok {
		for _, cell := range tb.Cells() {
			if id := firstLeaf(cell.Cell.Blocks); id != 0 {
				return id
			}
		}
		return 0
	}
	if IsContainer(b.Kind) {
		if id := firstLeaf(b.Children); id != 0 {
			return id
		}
		return b.ID
	}
	return b.ID
}

// firstRowText 是表第一行里的文本块。
func firstRowText(t *Table) []BlockID {
	var out []BlockID
	for _, cr := range t.Cells() {
		if cr.Row == 0 {
			collectTextBlocks(cr.Cell.Blocks, &out)
		}
	}
	return out
}

func collectTextBlocks(blocks []Block, out *[]BlockID) {
	for i := range blocks {
		b := &blocks[i]
		if HasText(b.Kind) {
			*out = append(*out, b.ID)
		}
		collectTextBlocks(b.Children, out)
		if tb, ok := b.Kind.(Table); ok {
			for _, cell := range tb.Cells() {
				collectTextBlocks(cell.Cell.Blocks, out)
			}
		}
	}
}

// Table 返回 id 处的表格。
func (c *Core) Table(id BlockID) *Table {
	b := c.Block(id)
	if b == nil {
		return nil
	}
	tb, ok := b.Kind.(Table)
	if !ok {
		return nil
	}
	return &tb
}

// CellOf 返回装着 id 的最内层单元格：(表格, 行, 列)。
func (c *Core) CellOf(id BlockID) (BlockID, int, int, bool) {
	p, ok := c.path(id)
	if !ok {
		return 0, 0, 0, false
	}
	at := -1
	for i := len(p) - 1; i >= 0; i-- {
		if p[i].Kind == SegCell {
			at = i
			break
		}
	}
	if at < 0 {
		return 0, 0, 0, false
	}
	t := blockAt(c.Doc.Blocks, p[:at])
	if t == nil {
		return 0, 0, 0, false
	}
	return t.ID, p[at].Row, p[at].Col, true
}

// CellRange 返回选中单元格的表格和矩形（合并单元格整个包含）。
func (c *Core) CellRange() (BlockID, CellRange, bool) {
	s := c.CellSel
	if s == nil {
		return 0, CellRange{}, false
	}
	t := c.Table(s.Table)
	if t == nil {
		return 0, CellRange{}, false
	}
	return s.Table, t.Expand(s.Raw(t)), true
}

// CellBlocks 是选中单元格里的顶层块。
func (c *Core) CellBlocks() []BlockID {
	id, r, ok := c.CellRange()
	if !ok {
		return nil
	}
	t := c.Table(id)
	if t == nil {
		return nil
	}
	var out []BlockID
	for _, rc := range t.CellsIn(r) {
		for i := range t.Rows[rc[0]][rc[1]].Blocks {
			out = append(out, t.Rows[rc[0]][rc[1]].Blocks[i].ID)
		}
	}
	return out
}

// CellTextBlocks 是选中单元格里的全部文本块。
func (c *Core) CellTextBlocks() []BlockID {
	id, r, ok := c.CellRange()
	if !ok {
		return nil
	}
	t := c.Table(id)
	if t == nil {
		return nil
	}
	var out []BlockID
	for _, rc := range t.CellsIn(r) {
		collectTextBlocks(t.Rows[rc[0]][rc[1]].Blocks, &out)
	}
	return out
}

// SelectedCells 是选中的单元格（行列原点）。
func (c *Core) SelectedCells() [][2]int {
	id, r, ok := c.CellRange()
	if !ok {
		return nil
	}
	t := c.Table(id)
	if t == nil {
		return nil
	}
	return t.CellsIn(r)
}

// MergedSelected：选区恰好是一个合并单元格时（拆分单元格）。
func (c *Core) MergedSelected() (BlockID, int, int, bool) {
	id, r, ok := c.CellRange()
	if !ok {
		return 0, 0, 0, false
	}
	t := c.Table(id)
	if t == nil {
		return 0, 0, 0, false
	}
	cells := t.CellsIn(r)
	if len(cells) == 1 {
		cell := &t.Rows[cells[0][0]][cells[0][1]]
		if cell.Colspan > 1 || cell.Rowspan > 1 {
			return id, cells[0][0], cells[0][1], true
		}
	}
	return 0, 0, 0, false
}

// FirstSelectedCell 是第一个选中的单元格（工具栏显示它的样式）。
func (c *Core) FirstSelectedCell() *TableCell {
	id, _, ok := c.CellRange()
	if !ok {
		return nil
	}
	cells := c.SelectedCells()
	if len(cells) == 0 {
		return nil
	}
	t := c.Table(id)
	if t == nil {
		return nil
	}
	return &t.Rows[cells[0][0]][cells[0][1]]
}

// CaretInCell 把光标放到盖住 (r, c) 的单元格开头（夹进网格）。
func (c *Core) CaretInCell(table BlockID, r, cc int) {
	t := c.Table(table)
	if t == nil || len(t.Rows) == 0 || t.Ncols() == 0 {
		return
	}
	tr, tc := t.Origin(min(r, len(t.Rows)-1), min(cc, t.Ncols()-1))
	if id := firstLeaf(t.Rows[tr][tc].Blocks); id != 0 {
		c.Sel = Caret(NewPos(id, 0))
	}
}

// SelectCells 选中单元格。
func (c *Core) SelectCells(s CellSel) {
	c.BlockSel = nil
	ss := s
	c.CellSel = &ss
	c.CaretInCell(s.Table, s.Anchor[0], s.Anchor[1])
}

// editTable 对表格 id 做一步撤销内的修改；不是表格返回 false。
func (c *Core) editTable(id BlockID, f func(*Table)) bool {
	if c.Table(id) == nil {
		return false
	}
	c.Checkpoint(EditOther)
	b := c.BlockMut(id)
	if b == nil {
		return false
	}
	tb, ok := b.Kind.(Table)
	if !ok {
		return false
	}
	f(&tb)
	tb.Repair()
	b.Kind = tb
	return true
}

// pasteTableMatrix：恰好一张表（空段除外）粘进表格里时，从选区（或光
// 标格）的左上格开始填它的单元格，按需加行加列，然后选中它们。
func (c *Core) pasteTableMatrix(blocks []Block) bool {
	var src *Table
	others := false
	for i := range blocks {
		b := &blocks[i]
		_, isText := b.Kind.(Text)
		emptyText := isText && b.Text.IsEmpty() && len(b.Children) == 0
		if emptyText {
			continue
		}
		if tb, isTable := b.Kind.(Table); isTable && src == nil {
			t := tb
			src = &t
		} else {
			others = true
		}
	}
	if src == nil || others {
		return false
	}
	var table BlockID
	var r0, c0 int
	if id, r, ok := c.CellRange(); ok {
		table, r0, c0 = id, r.Top, r.Left
	} else if id, r, cc, ok := c.CellOf(c.Sel.Head.Block); ok {
		table, r0, c0 = id, r, cc
	} else {
		return false
	}
	h, w := len(src.Rows), src.Ncols()
	if h == 0 || w == 0 {
		return false
	}
	srcCopy := src
	c.editTable(table, func(t *Table) {
		for len(t.Rows) < r0+h {
			t.InsertRow(len(t.Rows))
		}
		for t.Ncols() < c0+w {
			t.InsertCol(t.Ncols())
		}
		for _, cellRef := range srcCopy.Cells() {
			slot := &t.Rows[r0+cellRef.Row][c0+cellRef.Col]
			if slot.IsCovered() {
				continue
			}
			blocks := make([]Block, 0, len(cellRef.Cell.Blocks))
			for i := range cellRef.Cell.Blocks {
				blocks = append(blocks, cellRef.Cell.Blocks[i].FreshIDs())
			}
			if len(blocks) == 0 {
				blocks = append(blocks, BlockText(""))
			}
			slot.Blocks = blocks
			if cellRef.Cell.Bg != nil {
				slot.Bg = clonePtr(cellRef.Cell.Bg)
			}
		}
	})
	c.SelectCells(CellSel{
		Table:  table,
		Anchor: [2]int{r0, c0},
		Head:   [2]int{r0 + h - 1, c0 + w - 1},
		Kind:   CellSelRange,
	})
	return true
}

// TableInsert 在边界 at 前插入行（或列）；光标进新格。
func (c *Core) TableInsert(table BlockID, cols bool, at int) {
	if !c.editTable(table, func(t *Table) {
		if cols {
			t.InsertCol(at)
		} else {
			t.InsertRow(at)
		}
	}) {
		return
	}
	c.CellSel = nil
	if cols {
		c.CaretInCell(table, 0, at)
	} else {
		c.CaretInCell(table, at, 0)
	}
}

// TableDeleteSelected 删选中的行/列；全选删表格。
func (c *Core) TableDeleteSelected() {
	s := c.CellSel
	if s == nil {
		return
	}
	t := c.Table(s.Table)
	if t == nil {
		return
	}
	r := s.Raw(t)
	var all bool
	switch s.Kind {
	case CellSelRows:
		all = r.Top == 0 && r.Bottom+1 >= len(t.Rows)
	case CellSelCols:
		all = r.Left == 0 && r.Right+1 >= t.Ncols()
	case CellSelRange:
		return
	}
	var first BlockID
	if cells := t.Cells(); len(cells) > 0 {
		first = firstLeaf(cells[0].Cell.Blocks)
	}
	c.CellSel = nil
	if all {
		// 表格后面的 unit 接替第一格的位置。
		us := c.Units()
		i := -1
		for j := range us {
			if us[j].ID == first {
				i = j
				break
			}
		}
		c.DeleteBlock(s.Table)
		us2 := c.Units()
		var target BlockID
		if i >= 0 && i < len(us2) {
			target = us2[i].ID
		} else if len(us2) > 0 {
			target = us2[len(us2)-1].ID
		}
		c.Sel = Caret(NewPos(target, 0))
		return
	}
	c.editTable(s.Table, func(t2 *Table) {
		switch s.Kind {
		case CellSelRows:
			t2.DeleteRows(r.Top, r.Bottom+1)
		default:
			t2.DeleteCols(r.Left, r.Right+1)
		}
	})
	if s.Kind == CellSelRows {
		c.CaretInCell(s.Table, r.Top, 0)
	} else {
		c.CaretInCell(s.Table, 0, r.Left)
	}
}

// TableMerge 合并选中的单元格。
func (c *Core) TableMerge() {
	id, r, ok := c.CellRange()
	if !ok {
		return
	}
	if len(c.SelectedCells()) < 2 {
		return
	}
	c.editTable(id, func(t *Table) { t.Merge(r) })
	c.SelectCells(CellSel{Table: id, Anchor: [2]int{r.Top, r.Left}, Head: [2]int{r.Top, r.Left}, Kind: CellSelRange})
}

// TableUnmerge 拆分选中的合并单元格；原跨度保持选中。
func (c *Core) TableUnmerge() {
	id, r, cc, ok := c.MergedSelected()
	if !ok {
		return
	}
	cell := &c.Table(id).Rows[r][cc]
	head := [2]int{r + int(cell.Rowspan) - 1, cc + int(cell.Colspan) - 1}
	c.editTable(id, func(t *Table) { t.Unmerge(r, cc) })
	c.SelectCells(CellSel{Table: id, Anchor: [2]int{r, cc}, Head: head, Kind: CellSelRange})
}

func (c *Core) editCells(f func(*TableCell)) {
	id, _, ok := c.CellRange()
	if !ok {
		return
	}
	cells := c.SelectedCells()
	c.editTable(id, func(t *Table) {
		for _, rc := range cells {
			f(&t.Rows[rc[0]][rc[1]])
		}
	})
}

// TableSetBG 设置选中单元格背景。
func (c *Core) TableSetBG(bg *ColorName) {
	c.editCells(func(cell *TableCell) { cell.Bg = clonePtr(bg) })
}

// TableSetValign 设置垂直对齐（"middle"/"bottom"；顶部默认 nil）。
func (c *Core) TableSetValign(v *string) {
	c.editCells(func(cell *TableCell) { cell.Valign = clonePtr(v) })
}

// TableClearCells 清空选中单元格（Backspace、⌘X）；选区保留。
func (c *Core) TableClearCells() {
	s := c.CellSel
	if s == nil {
		return
	}
	c.editCells(func(cell *TableCell) { cell.Blocks = []Block{BlockText("")} })
	c.CaretInCell(s.Table, s.Anchor[0], s.Anchor[1])
}

// TableSetWidth 提交列宽；没变化返回 false。
func (c *Core) TableSetWidth(table BlockID, col int, w float32) bool {
	if w < TableMinWidth {
		w = TableMinWidth
	}
	w = roundF32(w)
	t := c.Table(table)
	if t == nil || col >= len(t.ColWidths) || t.ColWidths[col] == w {
		return false
	}
	return c.editTable(table, func(t2 *Table) { t2.ColWidths[col] = w })
}

func roundF32(v float32) float32 {
	return float32(int64(v + 0.5)) // Rust f32::round：四舍五入、远离零
}

// TableDistribute 均分列宽。
func (c *Core) TableDistribute(table BlockID) {
	c.editTable(table, func(t *Table) {
		var sum float32
		for _, w := range t.ColWidths {
			sum += w
		}
		avg := sum / float32(max(len(t.ColWidths), 1))
		for i := range t.ColWidths {
			t.ColWidths[i] = roundF32(avg)
		}
	})
}

// TableToggleHeaderRow 切换表头行。
func (c *Core) TableToggleHeaderRow(table BlockID) {
	c.editTable(table, func(t *Table) {
		if t.HeaderRows > 0 {
			t.HeaderRows = 0
		} else {
			t.HeaderRows = 1
		}
	})
}

// TableToggleHeaderCol 切换表头列。
func (c *Core) TableToggleHeaderCol(table BlockID) {
	c.editTable(table, func(t *Table) { t.HeaderCol = !t.HeaderCol })
}

// TableMoveSelected 把选中的行/列移到边界 to 前；保持选中。原地不动返
// 回 false。
func (c *Core) TableMoveSelected(to int) bool {
	s := c.CellSel
	if s == nil {
		return false
	}
	id, r, ok := c.CellRange()
	if !ok {
		return false
	}
	var cols bool
	switch s.Kind {
	case CellSelCols:
		cols = true
	case CellSelRows:
	default:
		return false
	}
	var a, b int
	if cols {
		a, b = r.Left, r.Right
	} else {
		a, b = r.Top, r.Bottom
	}
	if to >= a && to <= b+1 {
		return false
	}
	c.editTable(id, func(t *Table) {
		if cols {
			t.MoveCols(a, b+1, to)
		} else {
			t.MoveRows(a, b+1, to)
		}
	})
	var na, nb int
	if to < a {
		na, nb = to, to+b-a
	} else {
		na, nb = to-(b+1-a), to-1
	}
	var anchor, head [2]int
	if cols {
		anchor, head = [2]int{0, na}, [2]int{0, nb}
	} else {
		anchor, head = [2]int{na, 0}, [2]int{nb, 0}
	}
	c.SelectCells(CellSel{Table: id, Anchor: anchor, Head: head, Kind: s.Kind})
	return true
}

// SelectedCellsContent 把选中的单元格取成一张独立的表（复制用）。
func (c *Core) SelectedCellsContent() []Block {
	id, r, ok := c.CellRange()
	if !ok {
		return nil
	}
	t := c.Table(id)
	if t == nil {
		return nil
	}
	sub := Table{
		HeaderRows: headerRowsIf(r.Top == 0, t.HeaderRows),
		HeaderCol:  t.HeaderCol && r.Left == 0,
	}
	sub.ColWidths = append([]float32{}, t.ColWidths[r.Left:r.Right+1]...)
	for _, row := range t.Rows[r.Top : r.Bottom+1] {
		sub.Rows = append(sub.Rows, append([]TableCell{}, row[r.Left:r.Right+1]...))
	}
	sub.Repair()
	b := NewBlock(sub, RichText{}).FreshIDs()
	return []Block{b}
}

func headerRowsIf(cond bool, v int) int {
	if cond {
		return v
	}
	return 0
}
