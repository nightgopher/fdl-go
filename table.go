package fdl

// TableCell 是表格的一个单元格。Blocks 为空且 Colspan/Rowspan 为 0 的
// 单元格是被合并单元格盖住的网格槽（DocxXML 里直接省略它们）。
type TableCell struct {
	Blocks  []Block
	Bg      *ColorName
	Valign  *string
	Colspan uint32
	Rowspan uint32
}

// EmptyCell 是一个带空段落的单元格。
func EmptyCell() TableCell {
	return TableCell{Blocks: []Block{BlockText("")}, Colspan: 1, Rowspan: 1}
}

// CoveredCell 是被合并单元格盖住的槽（0×0，无内容）。
func CoveredCell() TableCell { return TableCell{} }

// IsCovered 报告该槽是否被合并单元格盖住。
func (c *TableCell) IsCovered() bool { return c.Colspan == 0 }

// Table 是完整网格：ColWidths 定列，Rows 是 rows×cols 的全网格，被合并
// 单元格盖住的槽放 CoveredCell。
type Table struct {
	ColWidths  []float32
	HeaderRows int
	// HeaderCol：第一列是表头列（标题列）。
	HeaderCol bool
	Rows      [][]TableCell
}

const (
	TableDefaultWidth float32 = 100
	TableMinWidth     float32 = 50
)

// Ncols 返回列数。
func (t *Table) Ncols() int { return len(t.ColWidths) }

// Repair 把网格修整成矩形并让每个跨度都合法：行补齐到列数、跨度裁剪
// 到网格内、被孤立盖住的槽恢复成空单元格。
func (t *Table) Repair() {
	n := len(t.ColWidths)
	for _, row := range t.Rows {
		n = max(n, len(row))
	}
	n = max(n, 1)
	for len(t.ColWidths) < n {
		t.ColWidths = append(t.ColWidths, TableDefaultWidth)
	}
	if len(t.Rows) == 0 {
		t.Rows = append(t.Rows, nil)
	}
	for r := range t.Rows {
		for len(t.Rows[r]) < n {
			t.Rows[r] = append(t.Rows[r], EmptyCell())
		}
	}
	nrows := len(t.Rows)
	owned := make([][]bool, nrows)
	for i := range owned {
		owned[i] = make([]bool, n)
	}
	for r := 0; r < nrows; r++ {
		for c := 0; c < n; c++ {
			if owned[r][c] {
				if !t.Rows[r][c].IsCovered() {
					t.Rows[r][c] = CoveredCell()
				}
				continue
			}
			if t.Rows[r][c].IsCovered() {
				t.Rows[r][c] = EmptyCell()
			}
			cell := &t.Rows[r][c]
			cs := min(int(max(cell.Colspan, 1)), n-c)
			// 跨度不能伸进更早的单元格已占的槽。
			for k := 1; k < cs; k++ {
				if owned[r][c+k] {
					cs = k
					break
				}
			}
			rs := min(int(max(cell.Rowspan, 1)), nrows-r)
			for k := 1; k < rs; k++ {
				hit := false
				for x := c; x < c+cs; x++ {
					if owned[r+k][x] {
						hit = true
						break
					}
				}
				if hit {
					rs = k
					break
				}
			}
			cell.Colspan = uint32(cs)
			cell.Rowspan = uint32(rs)
			for rr := r; rr < r+rs; rr++ {
				for cc := c; cc < c+cs; cc++ {
					owned[rr][cc] = true
				}
			}
		}
	}
	t.HeaderRows = min(t.HeaderRows, nrows)
}

// CellRange 是一个含端点的网格槽矩形。
type CellRange struct {
	Top    int
	Left   int
	Bottom int
	Right  int
}

// CellRangeOf 取把 (aRow,aCol) 和 (bRow,bCol) 都框住的最小矩形。
func CellRangeOf(aRow, aCol, bRow, bCol int) CellRange {
	return CellRange{
		Top:    min(aRow, bRow),
		Left:   min(aCol, bCol),
		Bottom: max(aRow, bRow),
		Right:  max(aCol, bCol),
	}
}

func (r CellRange) Contains(row, col int) bool {
	return row >= r.Top && row <= r.Bottom && col >= r.Left && col <= r.Right
}

// Origin 返回盖住 (r, c) 槽的可见单元格（合并单元格的左上角）。
func (t *Table) Origin(r, c int) (int, int) {
	for or := r; or >= 0; or-- {
		for oc := c; oc >= 0; oc-- {
			cell := t.Rows[or][oc]
			if !cell.IsCovered() && or+int(cell.Rowspan) > r && oc+int(cell.Colspan) > c {
				return or, oc
			}
		}
	}
	return r, c
}

// CellRef 是一个可见单元格及其位置。
type CellRef struct {
	Row  int
	Col  int
	Cell *TableCell
}

// Cells 按阅读顺序列出可见单元格。
func (t *Table) Cells() []CellRef {
	var out []CellRef
	for r := range t.Rows {
		for c := range t.Rows[r] {
			if !t.Rows[r][c].IsCovered() {
				out = append(out, CellRef{Row: r, Col: c, Cell: &t.Rows[r][c]})
			}
		}
	}
	return out
}

// CellsIn 按阅读顺序列出 r 里的可见单元格位置。
func (t *Table) CellsIn(r CellRange) [][2]int {
	var out [][2]int
	for _, cr := range t.Cells() {
		if r.Contains(cr.Row, cr.Col) {
			out = append(out, [2]int{cr.Row, cr.Col})
		}
	}
	return out
}

// Expand 把 r 扩成没有任何合并单元格跨过的最小矩形。
func (t *Table) Expand(r CellRange) CellRange {
	for {
		grown := r
		for row := r.Top; row <= r.Bottom; row++ {
			for col := r.Left; col <= r.Right; col++ {
				or, oc := t.Origin(row, col)
				cell := t.Rows[or][oc]
				grown.Top = min(grown.Top, or)
				grown.Left = min(grown.Left, oc)
				grown.Bottom = max(grown.Bottom, or+int(max(cell.Rowspan, 1))-1)
				grown.Right = max(grown.Right, oc+int(max(cell.Colspan, 1))-1)
			}
		}
		if grown == r {
			return r
		}
		r = grown
	}
}

// placedCell 是 takeCells 拿出的一个可见单元格及其原位与跨度。
type placedCell struct {
	row, col, rowspan, colspan int
	cell                       TableCell
}

func (t *Table) takeCells() []placedCell {
	var out []placedCell
	for r := range t.Rows {
		for c := range t.Rows[r] {
			cell := t.Rows[r][c]
			if !cell.IsCovered() {
				out = append(out, placedCell{row: r, col: c, rowspan: int(cell.Rowspan), colspan: int(cell.Colspan), cell: cell})
			}
		}
	}
	return out
}

// putCells 把单元格摆回 nrows × ncols 的网格；空槽补空单元格。
func (t *Table) putCells(nrows int, cells []placedCell) {
	n := len(t.ColWidths)
	grid := make([][]*TableCell, nrows)
	for i := range grid {
		grid[i] = make([]*TableCell, n)
	}
	for _, pc := range cells {
		if pc.rowspan == 0 || pc.colspan == 0 || pc.row >= nrows || pc.col >= n {
			continue
		}
		cell := pc.cell
		cell.Rowspan = uint32(pc.rowspan)
		cell.Colspan = uint32(pc.colspan)
		for rr := pc.row; rr < pc.row+pc.rowspan && rr < nrows; rr++ {
			for cc := pc.col; cc < pc.col+pc.colspan && cc < n; cc++ {
				cov := CoveredCell()
				grid[rr][cc] = &cov
			}
		}
		grid[pc.row][pc.col] = &cell
	}
	t.Rows = make([][]TableCell, nrows)
	for i := range t.Rows {
		t.Rows[i] = make([]TableCell, n)
		for c := range t.Rows[i] {
			if grid[i][c] == nil {
				t.Rows[i][c] = EmptyCell()
			} else {
				t.Rows[i][c] = *grid[i][c]
			}
		}
	}
	t.Repair()
}

// InsertRow 在 at 行前插入空行；跨过该行的合并单元格随之变高。
func (t *Table) InsertRow(at int) {
	nrows := len(t.Rows) + 1
	cells := t.takeCells()
	for i := range cells {
		pc := &cells[i]
		top := pc.row
		if pc.row >= at {
			top++
		}
		end := pc.row + pc.rowspan
		if pc.row+pc.rowspan > at {
			end++
		}
		pc.row, pc.rowspan = top, end-top
	}
	t.putCells(nrows, cells)
}

// InsertCol 在 at 列前插入默认宽的空列。
func (t *Table) InsertCol(at int) {
	at = min(at, len(t.ColWidths))
	nrows := len(t.Rows)
	cells := t.takeCells()
	for i := range cells {
		pc := &cells[i]
		left := pc.col
		if pc.col >= at {
			left++
		}
		end := pc.col + pc.colspan
		if pc.col+pc.colspan > at {
			end++
		}
		pc.col, pc.colspan = left, end-left
	}
	t.ColWidths = append(t.ColWidths, 0)
	copy(t.ColWidths[at+1:], t.ColWidths[at:])
	t.ColWidths[at] = TableDefaultWidth
	t.putCells(nrows, cells)
}

// DeleteRows 删掉 [from, to) 行；合并单元格收缩，起点在被删范围里的
// 连同内容移到范围后的第一行。
func (t *Table) DeleteRows(from, to int) {
	to = min(to, len(t.Rows))
	if from >= to {
		return
	}
	f := func(x int) int {
		if x <= from {
			return x
		}
		if x >= to {
			return x - (to - from)
		}
		return from
	}
	nrows := len(t.Rows) - (to - from)
	cells := t.takeCells()
	for i := range cells {
		pc := &cells[i]
		top, end := f(pc.row), f(pc.row+pc.rowspan)
		pc.row = top
		pc.rowspan = max(end-top, 0)
	}
	t.putCells(nrows, cells)
}

// DeleteCols 删掉 [from, to) 列。
func (t *Table) DeleteCols(from, to int) {
	to = min(to, len(t.ColWidths))
	if from >= to {
		return
	}
	f := func(x int) int {
		if x <= from {
			return x
		}
		if x >= to {
			return x - (to - from)
		}
		return from
	}
	nrows := len(t.Rows)
	cells := t.takeCells()
	for i := range cells {
		pc := &cells[i]
		left, end := f(pc.col), f(pc.col+pc.colspan)
		pc.col = left
		pc.colspan = max(end-left, 0)
	}
	t.ColWidths = append(t.ColWidths[:from], t.ColWidths[to:]...)
	t.putCells(nrows, cells)
}

// MoveRows 把 [from, to) 行移到 to 前（to 按移动前的行号数）。调用方
// 保证没有合并单元格跨过范围边缘或 to 边界。
func (t *Table) MoveRows(from, to, dest int) {
	order := reordered(len(t.Rows), from, to, dest)
	t.Rows = permute(t.Rows, order)
	t.Repair()
}

// MoveCols 把 [from, to) 列移到 dest 前。
func (t *Table) MoveCols(from, to, dest int) {
	order := reordered(len(t.ColWidths), from, to, dest)
	cw := make([]float32, len(order))
	for i, idx := range order {
		cw[i] = t.ColWidths[idx]
	}
	t.ColWidths = cw
	for r := range t.Rows {
		t.Rows[r] = permute(t.Rows[r], order)
	}
	t.Repair()
}

// reordered 是 n 行在 [from, to) 移到 dest 前之后各行的原下标。
func reordered(n, from, to, dest int) []int {
	var rest []int
	for i := 0; i < n; i++ {
		if i < from || i >= to {
			rest = append(rest, i)
		}
	}
	at := 0
	for _, i := range rest {
		if i < dest {
			at++
		}
	}
	out := make([]int, 0, n)
	out = append(out, rest[:at]...)
	for i := from; i < to; i++ {
		out = append(out, i)
	}
	out = append(out, rest[at:]...)
	return out
}

func permute[T any](v []T, order []int) []T {
	old := make([]*T, len(v))
	for i := range v {
		old[i] = &v[i]
	}
	out := make([]T, 0, len(order))
	for _, i := range order {
		if i < len(old) && old[i] != nil {
			out = append(out, *old[i])
		}
	}
	return out
}

// Merge 把 r 并入其左上角单元格，后者按阅读顺序接收每个非空单元格的
// 内容。
func (t *Table) Merge(r CellRange) {
	r = t.Expand(r)
	cells := t.CellsIn(r)
	if len(cells) < 2 {
		return
	}
	var blocks []Block
	for _, rc := range cells {
		row, col := rc[0], rc[1]
		cell := t.Rows[row][col]
		t.Rows[row][col] = CoveredCell()
		empty := true
		for i := range cell.Blocks {
			b := &cell.Blocks[i]
			if !b.Text.IsEmpty() || len(b.Children) > 0 || !HasText(b.Kind) {
				empty = false
				break
			}
		}
		if !empty {
			blocks = append(blocks, cell.Blocks...)
		}
		if row == r.Top && col == r.Left {
			cell.Blocks = nil
			t.Rows[row][col] = cell
		}
	}
	origin := &t.Rows[r.Top][r.Left]
	if len(blocks) == 0 {
		blocks = []Block{BlockText("")}
	}
	origin.Blocks = blocks
	origin.Rowspan = uint32(r.Bottom - r.Top + 1)
	origin.Colspan = uint32(r.Right - r.Left + 1)
}

// Unmerge 拆开 (r, c) 处的合并单元格；内容留在左上角。
func (t *Table) Unmerge(r, c int) {
	cell := &t.Rows[r][c]
	rs, cs := int(cell.Rowspan), int(cell.Colspan)
	cell.Rowspan, cell.Colspan = 1, 1
	for rr := r; rr < r+rs; rr++ {
		for cc := c; cc < c+cs; cc++ {
			if t.Rows[rr][cc].IsCovered() {
				t.Rows[rr][cc] = EmptyCell()
			}
		}
	}
}
