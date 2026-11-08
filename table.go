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
