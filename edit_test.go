package fdl

import (
	"fmt"
	"testing"
)

// 对拍 model.rs 的 rich_text_replace_and_marks。
func TestRichTextReplaceAndMarks(t *testing.T) {
	rt := Plain("hello world")
	rt.UpdateMarks(0, 5, func(m *Marks) { m.Bold = true })
	if len(rt.Runs) != 2 {
		t.Fatalf("runs = %+v, want 2 段", rt.Runs)
	}
	if !rt.AllMarks(0, 5, func(m Marks) bool { return m.Bold }) {
		t.Error("0..5 应全为加粗")
	}
	rt.Replace(5, 6, ptrRich(Plain(", ")))
	if rt.Text != "hello, world" {
		t.Errorf("text = %q", rt.Text)
	}
	if len(rt.Runs) != 2 {
		t.Errorf("runs = %+v, want 2 段", rt.Runs)
	}
	tail := rt.SplitOff(7)
	if tail.Text != "world" {
		t.Errorf("tail = %q", tail.Text)
	}
	if rt.Text != "hello, " {
		t.Errorf("head = %q", rt.Text)
	}
	if !rt.MarksAt(3).Bold {
		t.Error("marks_at(3) 应延续加粗")
	}
	if rt.MarksAt(7).Bold {
		t.Error("marks_at(7) 不应加粗")
	}
	if !rt.MarksAt(5).Bold {
		t.Error("marks_at(5) 应延续左侧加粗")
	}
	// 在 run 结束处不延续链接与行内代码。
	linked := Plain("ab")
	lnk := "https://x"
	linked.Push("c", Marks{Link: &lnk})
	if m := linked.MarksAt(2); m.Link != nil {
		t.Error("链接不应延续到光标右侧")
	}
	if m := linked.MarksAt(2); m.Bold {
		t.Error("无关样式不该出现")
	}
}

func ptrRich(rt RichText) *RichText { return &rt }

func gridTable(nr, nc int) Table {
	rows := make([][]TableCell, nr)
	for r := range rows {
		rows[r] = make([]TableCell, nc)
		for c := range rows[r] {
			rows[r][c] = TableCell{Blocks: []Block{BlockText(fmt.Sprintf("%d%d", r, c))}, Colspan: 1, Rowspan: 1}
		}
	}
	return Table{ColWidths: repeatF32(TableDefaultWidth, nc), Rows: rows}
}

func cellText(t *Table, r, c int) string {
	var parts []string
	for _, b := range t.Rows[r][c].Blocks {
		parts = append(parts, b.Text.Text)
	}
	return join(parts, "+")
}

func join(parts []string, sep string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += sep
		}
		out += p
	}
	return out
}

// 对拍 model.rs 的 table_merge_insert_delete。
func TestTableMergeInsertDelete(t *testing.T) {
	tb := gridTable(3, 3)
	tb.Merge(CellRangeOf(0, 0, 1, 1))
	if tb.Rows[0][0].Rowspan != 2 || tb.Rows[0][0].Colspan != 2 {
		t.Fatalf("合并跨度 = %d×%d, want 2×2", tb.Rows[0][0].Rowspan, tb.Rows[0][0].Colspan)
	}
	if got := cellText(&tb, 0, 0); got != "00+01+10+11" {
		t.Errorf("合并内容 = %q", got)
	}
	if !tb.Rows[1][1].IsCovered() {
		t.Error("(1,1) 应被盖住")
	}
	if or, oc := tb.Origin(1, 1); or != 0 || oc != 0 {
		t.Errorf("origin(1,1) = (%d,%d)", or, oc)
	}
	// 触到合并单元格的范围长到把它整个框住。
	if got := tb.Expand(CellRangeOf(1, 1, 1, 2)); got != CellRangeOf(0, 0, 1, 2) {
		t.Errorf("expand = %+v", got)
	}
	// 在合并内部插行会撑高它；在边缘不会。
	tb.InsertRow(1)
	if len(tb.Rows) != 4 {
		t.Fatalf("rows = %d, want 4", len(tb.Rows))
	}
	if tb.Rows[0][0].Rowspan != 3 {
		t.Errorf("rowspan = %d, want 3", tb.Rows[0][0].Rowspan)
	}
	if tb.Rows[1][2].IsCovered() {
		t.Error("(1,2) 不应被盖住")
	}
	tb.InsertCol(0)
	if tb.Ncols() != 4 {
		t.Fatalf("ncols = %d, want 4", tb.Ncols())
	}
	if tb.Rows[0][1].Colspan != 2 {
		t.Errorf("colspan = %d, want 2", tb.Rows[0][1].Colspan)
	}
	// 删掉合并的首行，它整体下移一行。
	tb.DeleteRows(0, 1)
	if tb.Rows[0][1].Rowspan != 2 {
		t.Errorf("rowspan = %d, want 2", tb.Rows[0][1].Rowspan)
	}
	if got := cellText(&tb, 0, 1); got != "00+01+10+11" {
		t.Errorf("内容 = %q", got)
	}
	tb.DeleteCols(1, 2)
	if tb.Rows[0][1].Colspan != 1 {
		t.Errorf("colspan = %d, want 1", tb.Rows[0][1].Colspan)
	}
	tb.Unmerge(0, 1)
	if n := len(tb.Cells()); n != 9 {
		t.Errorf("可见单元格 = %d, want 9", n)
	}
}

func TestMoveRowsAndCols(t *testing.T) {
	tb := gridTable(2, 2)
	// 行 0 移到行 1 之后。
	tb.MoveRows(0, 1, 2)
	if got := cellText(&tb, 0, 0); got != "10" {
		t.Errorf("移动后 (0,0) = %q, want 10", got)
	}
	tb.MoveCols(0, 1, 2)
	if got := cellText(&tb, 0, 0); got != "11" {
		t.Errorf("移动后 (0,0) = %q, want 11", got)
	}
	if len(tb.ColWidths) != 2 {
		t.Errorf("ncols = %d", tb.Ncols())
	}
}

func TestFreshIDsDeepCopy(t *testing.T) {
	doc, err := Parse(`<title>T</title><ul><li>a<ul><li>b</li></ul></li></ul><table><tbody><tr><td><p>c</p></td></tr></tbody></table>`)
	if err != nil {
		t.Fatal(err)
	}
	orig := doc.Blocks[0]
	cp := orig.FreshIDs()
	// 结构相同、id 全新、互不影响。
	var collect func(b Block, ids *[]BlockID)
	collect = func(b Block, ids *[]BlockID) {
		*ids = append(*ids, b.ID)
		for i := range b.Children {
			collect(b.Children[i], ids)
		}
		if tb, ok := b.Kind.(Table); ok {
			for _, row := range tb.Rows {
				for _, cell := range row {
					for _, cb := range cell.Blocks {
						collect(cb, ids)
					}
				}
			}
		}
	}
	var a, b []BlockID
	collect(orig, &a)
	collect(cp, &b)
	if len(a) == 0 || len(a) != len(b) {
		t.Fatalf("结构不同: %d vs %d", len(a), len(b))
	}
	seen := map[BlockID]bool{}
	for i := range a {
		if a[i] == b[i] {
			t.Errorf("第 %d 个块 id 未更新", i)
		}
		if seen[b[i]] {
			t.Errorf("id %d 重复", b[i])
		}
		seen[b[i]] = true
	}
	// 深拷贝互不影响：改副本的样式指针不动原件。
	if tb, ok := cp.Kind.(Table); ok {
		c := ColorName("red")
		tb.Rows[0][0].Bg = &c
		if origTbl, ok := orig.Kind.(Table); ok && origTbl.Rows[0][0].Bg != nil {
			t.Error("深拷贝共享了指针")
		}
	}
}
