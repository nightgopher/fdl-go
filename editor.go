package fdl

// 编辑内核（对拍 editor/core.rs）：对文档的每次结构修改都经 Core，它负
// 责保持选区合法并记录撤销快照。位置是 (块 id, UTF-8 字节偏移)；标题用
// 保留 id TitleID 寻址。

import (
	"strings"
	"time"

	"unicode/utf8"

	"github.com/rivo/uniseg"
)

// TitleID 是标题的保留块 id（块 id 从 1 开始，0 不会冲突）。
const TitleID BlockID = 0

// Pos 是文档中的一个位置。
type Pos struct {
	Block  BlockID
	Offset int
}

func NewPos(block BlockID, offset int) Pos { return Pos{Block: block, Offset: offset} }

// Selection 是一段选区（anchor 到 head，可以反向）。
type Selection struct {
	Anchor Pos
	Head   Pos
}

func Caret(p Pos) Selection { return Selection{Anchor: p, Head: p} }

func (s Selection) IsCollapsed() bool { return s.Anchor == s.Head }

// SegKind 区分路径段：直接子块还是表格单元格里的块。
type SegKind uint8

const (
	SegChild SegKind = iota
	SegCell
)

// Seg 是路径的一段：根列表或上一块的子列表（Child）里第 Index 个，或
// 表格 (Row, Col) 单元格里第 Index 个。
type Seg struct {
	Kind  SegKind
	Row   int
	Col   int
	Index int
}

// Path 从根列表到一块的路径。
type Path []Seg

func pathEqual(a, b Path) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Unit 是文档顺序里一个可选中的停靠点。
type Unit struct {
	ID    BlockID
	Text  bool    // 有文本、光标可进入；原子块（分割线、图片…）则否
	Scope BlockID // 所在表格（选区不跨过它）；0 表示无
}

// ------------------------------------------------------------------ 树访问

func findPath(blocks []Block, id BlockID) (Path, bool) {
	for i := range blocks {
		if blocks[i].ID == id {
			return Path{{Kind: SegChild, Index: i}}, true
		}
		if p, ok := findPath(blocks[i].Children, id); ok {
			out := make(Path, 0, len(p)+1)
			out = append(out, Seg{Kind: SegChild, Index: i})
			out = append(out, p...)
			return out, true
		}
		if tb, isTable := blocks[i].Kind.(Table); isTable {
			for r := range tb.Rows {
				for c := range tb.Rows[r] {
					if p, ok := findPath(tb.Rows[r][c].Blocks, id); ok {
						first := p[0]
						if first.Kind == SegChild {
							first = Seg{Kind: SegCell, Row: r, Col: c, Index: first.Index}
						}
						p[0] = first
						out := make(Path, 0, len(p)+1)
						out = append(out, Seg{Kind: SegChild, Index: i})
						out = append(out, p...)
						return out, true
					}
				}
			}
		}
	}
	return nil, false
}

// pathIndex 给出每块（含表格单元格里的）的路径，供批量查询。
func pathIndex(blocks []Block) map[BlockID]Path {
	out := make(map[BlockID]Path)
	var walk func(list []Block, base Path, seg func(int) Seg)
	walk = func(list []Block, base Path, seg func(int) Seg) {
		for i := range list {
			p := make(Path, 0, len(base)+1)
			p = append(p, base...)
			p = append(p, seg(i))
			walk(list[i].Children, p, func(k int) Seg { return Seg{Kind: SegChild, Index: k} })
			if tb, ok := list[i].Kind.(Table); ok {
				for r := range tb.Rows {
					for c := range tb.Rows[r] {
						rr, cc := r, c
						walk(tb.Rows[r][c].Blocks, p, func(k int) Seg {
							return Seg{Kind: SegCell, Row: rr, Col: cc, Index: k}
						})
					}
				}
			}
			if _, seen := out[list[i].ID]; !seen {
				out[list[i].ID] = p
			}
		}
	}
	walk(blocks, nil, func(k int) Seg { return Seg{Kind: SegChild, Index: k} })
	return out
}

// insideOf 返回「id 是 ids 之一或嵌在其中」的判定函数。
func insideOf(paths map[BlockID]Path, ids []BlockID) func(BlockID) bool {
	var targets []Path
	for _, id := range ids {
		if p, ok := paths[id]; ok {
			targets = append(targets, p)
		}
	}
	return func(id BlockID) bool {
		p, ok := paths[id]
		if !ok {
			return false
		}
		for n := 1; n <= len(p); n++ {
			for _, t := range targets {
				if pathEqual(t, p[:n]) {
					return true
				}
			}
		}
		return false
	}
}

// blockIndex 给出每块（含单元格里的）的指针。
func blockIndex(doc *Document) map[BlockID]*Block {
	out := make(map[BlockID]*Block)
	var walk func(list []Block)
	walk = func(list []Block) {
		for i := range list {
			b := &list[i]
			if _, seen := out[b.ID]; !seen {
				out[b.ID] = b
			}
			walk(b.Children)
			if tb, ok := b.Kind.(Table); ok {
				for _, row := range tb.Rows {
					for j := range row {
						walk(row[j].Blocks)
					}
				}
			}
		}
	}
	walk(doc.Blocks)
	return out
}

// unitPositions 是每个 unit 在文档顺序中的位置。
func unitPositions(units []Unit) map[BlockID]int {
	out := make(map[BlockID]int, len(units))
	for i, u := range units {
		out[u.ID] = i
	}
	return out
}

func blockAt(blocks []Block, path Path) *Block {
	if len(path) == 0 || path[0].Kind != SegChild || path[0].Index >= len(blocks) {
		return nil
	}
	b := &blocks[path[0].Index]
	for _, seg := range path[1:] {
		switch seg.Kind {
		case SegChild:
			if seg.Index >= len(b.Children) {
				return nil
			}
			b = &b.Children[seg.Index]
		case SegCell:
			tb, ok := b.Kind.(Table)
			if !ok || seg.Row >= len(tb.Rows) || seg.Col >= len(tb.Rows[seg.Row]) || seg.Index >= len(tb.Rows[seg.Row][seg.Col].Blocks) {
				return nil
			}
			b = &tb.Rows[seg.Row][seg.Col].Blocks[seg.Index]
		}
	}
	return b
}

// siblingsAt 返回 path 处块所在的列表视图和它的下标。
func siblingsAt(blocks []Block, path Path) ([]Block, int, bool) {
	if len(path) == 0 {
		return nil, 0, false
	}
	last := path[len(path)-1]
	outer := path[:len(path)-1]
	if len(outer) == 0 {
		if last.Kind != SegChild {
			return nil, 0, false
		}
		return blocks, last.Index, true
	}
	parent := blockAt(blocks, outer)
	if parent == nil {
		return nil, 0, false
	}
	switch last.Kind {
	case SegChild:
		return parent.Children, last.Index, true
	case SegCell:
		if tb, ok := parent.Kind.(Table); ok && last.Row < len(tb.Rows) && last.Col < len(tb.Rows[last.Row]) {
			return tb.Rows[last.Row][last.Col].Blocks, last.Index, true
		}
	}
	return nil, 0, false
}

// containerMut 是可写版 siblingsAt：返回列表本身的指针，供增删。
func containerMut(blocks *[]Block, path Path) (*[]Block, int, bool) {
	if len(path) == 0 {
		return nil, 0, false
	}
	if len(path) == 1 {
		if path[0].Kind != SegChild {
			return nil, 0, false
		}
		return blocks, path[0].Index, true
	}
	parent := blockAt(*blocks, path[:len(path)-1])
	if parent == nil {
		return nil, 0, false
	}
	last := path[len(path)-1]
	switch last.Kind {
	case SegChild:
		return &parent.Children, last.Index, true
	case SegCell:
		if tb, ok := parent.Kind.(Table); ok && last.Row < len(tb.Rows) && last.Col < len(tb.Rows[last.Row]) {
			return &tb.Rows[last.Row][last.Col].Blocks, last.Index, true
		}
	}
	return nil, 0, false
}

func parentPath(path Path) (Path, bool) {
	if len(path) > 1 {
		return path[:len(path)-1], true
	}
	return nil, false
}

// MaxNestDepth 是一个容器（根或单元格）下文本块的最大嵌套。
const MaxNestDepth = 16

// nestDepth 从最近的容器算起的嵌套深度。
func nestDepth(path Path) int {
	n := 0
	for i := len(path) - 1; i >= 0 && path[i].Kind == SegChild; i-- {
		n++
	}
	return n
}

func segIndex(s Seg) int { return s.Index }

func withIndex(s Seg, i int) Seg {
	s.Index = i
	return s
}

// units 按文档顺序列出可选中的停靠点（折叠的标题藏起后续同级，直到同
// 级或更高级标题）。
func units(doc *Document) []Unit {
	out := []Unit{{ID: TitleID, Text: true}}
	var walk func(list []Block, scope BlockID)
	walk = func(list []Block, scope BlockID) {
		var hideBelow uint8
		hiding := false
		for i := range list {
			b := &list[i]
			if hiding {
				if h, ok := b.Kind.(Heading); ok && h.Level <= hideBelow {
					hiding = false
				} else {
					continue
				}
			}
			if HasText(b.Kind) {
				out = append(out, Unit{ID: b.ID, Text: true, Scope: scope})
				if h, ok := b.Kind.(Heading); ok && b.Folded {
					hideBelow, hiding = h.Level, true
				}
				if !b.Folded {
					walk(b.Children, scope)
				}
			} else if IsContainer(b.Kind) {
				walk(b.Children, scope)
			} else if tb, ok := b.Kind.(Table); ok {
				for _, row := range tb.Rows {
					for _, cell := range row {
						walk(cell.Blocks, b.ID)
					}
				}
			} else {
				out = append(out, Unit{ID: b.ID, Text: false, Scope: scope})
			}
		}
	}
	walk(doc.Blocks, 0)
	return out
}

// ------------------------------------------------------------------ 字素与词

func prevGrapheme(s string, offset int) int {
	if offset <= 0 {
		return 0
	}
	if offset > len(s) {
		offset = len(s)
	}
	state := -1
	pos, last := 0, 0
	for pos < offset {
		cluster, _, _, ns := uniseg.FirstGraphemeClusterInString(s[pos:offset], state)
		last = pos
		pos += len(cluster)
		state = ns
	}
	return last
}

func nextGrapheme(s string, offset int) int {
	if offset >= len(s) {
		return len(s)
	}
	cluster, _, _, _ := uniseg.FirstGraphemeClusterInString(s[offset:], -1)
	return offset + len(cluster)
}

func prevWord(s string, offset int) int {
	last := 0
	state := -1
	pos := 0
	for pos < offset {
		word, _, ns := uniseg.FirstWordInString(s[pos:], state)
		if strings.TrimSpace(word) != "" {
			last = pos
		}
		pos += len(word)
		state = ns
	}
	return last
}

func nextWord(s string, offset int) int {
	state := -1
	pos := 0
	for pos < len(s) {
		word, _, ns := uniseg.FirstWordInString(s[pos:], state)
		end := pos + len(word)
		if end > offset && strings.TrimSpace(word) != "" {
			return end
		}
		pos = end
		state = ns
	}
	return len(s)
}

func wordAt(s string, offset int) (int, int) {
	state := -1
	pos := 0
	for pos < len(s) {
		word, _, ns := uniseg.FirstWordInString(s[pos:], state)
		end := pos + len(word)
		if offset >= pos && offset < end {
			return pos, end
		}
		pos = end
		state = ns
	}
	return offset, offset
}

// DropPlace 是拖放的落点。
type DropPlace uint8

const (
	DropBefore DropPlace = iota
	DropAfter
	DropFirstChild
)

// ------------------------------------------------------------------ Core

type snapshot struct {
	doc  Document
	sel  Selection
	size int // doc 的粗略字节数，撤销预算用
}

const (
	undoSteps    = 500       // 撤销历史最多步数
	undoBytes    = 256 << 20 // 以及大约这么多内存
	undoMinSteps = 20        // 但再大也至少保留这么多步
)

// EquationRune 是代表行内对象的一个 U+FFFC。
const EquationRune = "￼" // "\ufffc"

// EditKind 是编辑的种类（撤销合并用）。
type EditKind uint8

const (
	EditTyping EditKind = iota
	EditDelete
	EditEquation
	EditOther
)

// keepOutsideHistory：图标是文件级元信息、折叠不进撤销：undo/redo 保持
// 它们为当前值而非快照值。
func keepOutsideHistory(now *Document, restored *Document) {
	restored.Icon = now.Icon
	folded := make(map[BlockID]bool)
	now.ForEachBlock(func(b *Block) { folded[b.ID] = b.Folded })
	var walk func(list []Block)
	walk = func(list []Block) {
		for i := range list {
			b := &list[i]
			if f, ok := folded[b.ID]; ok {
				b.Folded = f
			}
			walk(b.Children)
		}
	}
	walk(restored.Blocks)
}

// BlockSel 是块选择模式：从 anchor 到 head（可见顺序）的块。
type BlockSel struct {
	Anchor BlockID
	Head   BlockID
}

// CellSel 是单元格选区（在 tableops.go 定义实现细节）。
type CellSel = cellSel

type lastEdit struct {
	kind  EditKind
	at    time.Time
	block BlockID
}

// Core 是编辑内核。
type Core struct {
	Doc Document
	Sel Selection
	// BlockSel 非 nil 时整块被选中（Esc、手柄点击、⌘A、块边 Shift+↑）。
	BlockSel *BlockSel
	// CellSel 非 nil 时表格单元格被选中（拖选、行/列栏点击），期间光
	// 标停在锚点单元格内。
	CellSel *CellSel
	// PendingMarks 是下一次输入要带的样式（光标上的 ⌘B 等）。
	PendingMarks *Marks

	undo   []snapshot
	redo   []snapshot
	lastEd *lastEdit
	// Dirty 自上次保存后是否改动过。
	Dirty bool
	// Generation 每次文档改动（含折叠）递增。
	Generation uint64

	// findPath 记忆：条目用前对着树校验，编辑不必主动失效；过期条目触
	// 发重建。
	paths    map[BlockID]Path
	pathHits uint32
	pathMiss uint32 // 上次重建以来的未命中数
	pathNeed uint32 // 多少次未命中才重建

	// Notice 是给视图提示的消息（如「当前位置不支持粘贴表格」）。
	Notice string
}

// NewCore 建立编辑内核，光标落在标题（空时）或第一个块。
func NewCore(doc Document) *Core {
	first := TitleID
	if us := units(&doc); len(us) > 1 {
		first = us[1].ID
	}
	start := first
	if doc.Title.IsEmpty() {
		start = TitleID
	}
	return &Core{
		Doc:      doc,
		Sel:      Caret(NewPos(start, 0)),
		paths:    map[BlockID]Path{},
		pathHits: ^uint32(0),
		pathNeed: 1,
	}
}

// ------------------------------------------------------------------ 查找

// TextOf 返回块的富文本（原子块没有）。
func (c *Core) TextOf(id BlockID) *RichText {
	if id == TitleID {
		return &c.Doc.Title
	}
	p, ok := c.path(id)
	if !ok {
		return nil
	}
	if b := blockAt(c.Doc.Blocks, p); b != nil && HasText(b.Kind) {
		return &b.Text
	}
	return nil
}

func (c *Core) path(id BlockID) (Path, bool) {
	if p, ok := c.paths[id]; ok {
		if b := blockAt(c.Doc.Blocks, p); b != nil && b.ID == id {
			c.pathHits++
			out := append(Path{}, p...)
			return out, true
		}
	}
	// 重建代价高：上次重建没回本（每步都在改树结构的循环）时，先直接
	// 搜索一段时间，越退越远。
	if c.pathMiss+1 < c.pathNeed {
		c.pathMiss++
		return findPath(c.Doc.Blocks, id)
	}
	need := c.pathNeed * 2
	if need > 1<<16 {
		need = 1 << 16
	}
	if c.pathHits >= 64 {
		need = 1
	}
	c.pathMiss, c.pathNeed = 0, need
	c.paths = pathIndex(c.Doc.Blocks)
	c.pathHits = 0
	p, ok := c.paths[id]
	if !ok {
		return nil, false
	}
	return append(Path{}, p...), true
}

func (c *Core) textMut(id BlockID) *RichText {
	if id == TitleID {
		return &c.Doc.Title
	}
	p, ok := c.path(id)
	if !ok {
		return nil
	}
	if b := blockAt(c.Doc.Blocks, p); b != nil && HasText(b.Kind) {
		return &b.Text
	}
	return nil
}

// Block 返回块本身。
func (c *Core) Block(id BlockID) *Block {
	p, ok := c.path(id)
	if !ok {
		return nil
	}
	return blockAt(c.Doc.Blocks, p)
}

// BlockMut 返回可写块指针（每次调用递增 Generation）。
func (c *Core) BlockMut(id BlockID) *Block {
	c.Generation++
	p, ok := c.path(id)
	if !ok {
		return nil
	}
	return blockAt(c.Doc.Blocks, p)
}

// ParentOf 返回父块。
func (c *Core) ParentOf(id BlockID) *Block {
	p, ok := c.path(id)
	if !ok {
		return nil
	}
	pp, ok := parentPath(p)
	if !ok {
		return nil
	}
	return blockAt(c.Doc.Blocks, pp)
}

// IsInTable 报告块是否在表格单元格里。
func (c *Core) IsInTable(id BlockID) bool {
	p, ok := c.path(id)
	if !ok {
		return false
	}
	for _, s := range p {
		if s.Kind == SegCell {
			return true
		}
	}
	return false
}

// Units 按文档顺序返回停靠点。
func (c *Core) Units() []Unit { return units(&c.Doc) }

func (c *Core) unitIndex(us []Unit, id BlockID) int {
	for i := range us {
		if us[i].ID == id {
			return i
		}
	}
	return -1
}

// LenOf 返回块文本的字节数。
func (c *Core) LenOf(id BlockID) int {
	if t := c.TextOf(id); t != nil {
		return t.Len()
	}
	return 0
}

// Ordered 返回文档顺序的 (start, end)。
func (c *Core) Ordered() (Pos, Pos) {
	s := c.Sel
	if s.Anchor.Block == s.Head.Block {
		if s.Anchor.Offset <= s.Head.Offset {
			return s.Anchor, s.Head
		}
		return s.Head, s.Anchor
	}
	return c.OrderedIn(c.Units())
}

// OrderedIn 是自带 units 的 Ordered。
func (c *Core) OrderedIn(us []Unit) (Pos, Pos) {
	s := c.Sel
	if s.Anchor.Block == s.Head.Block {
		return c.Ordered()
	}
	a := c.unitIndex(us, s.Anchor.Block)
	h := c.unitIndex(us, s.Head.Block)
	if a < 0 {
		a = 0
	}
	if h < 0 {
		h = 0
	}
	if a <= h {
		return s.Anchor, s.Head
	}
	return s.Head, s.Anchor
}

// RangeIn 是选区在块 id 里覆盖的字节区间（未触及则 false）。
func (c *Core) RangeIn(id BlockID, us []Unit) (int, int, bool) {
	s, e := c.OrderedIn(us)
	if s.Block == e.Block {
		if s.Block != id {
			return 0, 0, false
		}
		return s.Offset, e.Offset, true
	}
	si := c.unitIndex(us, s.Block)
	ei := c.unitIndex(us, e.Block)
	i := c.unitIndex(us, id)
	if si < 0 || ei < 0 || i < 0 || i < si || i > ei {
		return 0, 0, false
	}
	length := c.LenOf(id)
	a, b := 0, length
	if i == si {
		a = s.Offset
	}
	if i == ei {
		b = e.Offset
	}
	return a, b, true
}

// AtomicSelected：被选区整个盖住的原子块（渲染成选中态）。
func (c *Core) AtomicSelected(id BlockID, us []Unit) bool {
	s, e := c.OrderedIn(us)
	if s.Block == id && e.Block == id {
		return true
	}
	si := c.unitIndex(us, s.Block)
	ei := c.unitIndex(us, e.Block)
	i := c.unitIndex(us, id)
	if si < 0 || ei < 0 || i < 0 {
		return false
	}
	return si != ei && i >= si && i <= ei
}

// SelectedText 是选中的纯文本。
func (c *Core) SelectedText() string {
	us := c.Units()
	s, e := c.OrderedIn(us)
	si := c.unitIndex(us, s.Block)
	ei := c.unitIndex(us, e.Block)
	if si < 0 {
		si = 0
	}
	if ei < 0 {
		ei = 0
	}
	index := blockIndex(&c.Doc)
	var out strings.Builder
	end := ei
	if end < si {
		end = si
	}
	for _, u := range us[si : end+1] {
		if !u.Text {
			continue
		}
		var t *RichText
		if u.ID == TitleID {
			t = &c.Doc.Title
		} else if b, ok := index[u.ID]; ok {
			t = &b.Text
		} else {
			continue
		}
		a, b := 0, t.Len()
		if u.ID == s.Block {
			a = s.Offset
		}
		if u.ID == e.Block {
			b = e.Offset
		}
		if out.Len() > 0 {
			out.WriteByte('\n')
		}
		lo := a
		if b < lo {
			lo = b
		}
		out.WriteString(t.Text[lo:b])
	}
	return out.String()
}

// ------------------------------------------------------------------ 撤销

// Checkpoint 在改动前记快照。同一块内连续输入在 700ms 内合并为一步
// （窗口滑动）。
func (c *Core) Checkpoint(kind EditKind) {
	c.Generation++
	now := time.Now()
	block := c.Sel.Head.Block
	coalesce := false
	if le := c.lastEd; le != nil {
		coalesce = le.kind == kind && kind != EditOther && le.block == block &&
			(kind == EditEquation || now.Sub(le.at) < 700*time.Millisecond)
	}
	if !coalesce {
		c.pushUndo(newSnapshot(c.Doc, c.Sel))
	}
	c.lastEd = &lastEdit{kind: kind, at: now, block: block}
	c.redo = nil
	c.Dirty = true
}

// Restore 把 doc 放回（阅读模式拒绝编辑时），历史之外。
func (c *Core) Restore(doc Document) {
	c.Generation++
	c.Doc = doc
	c.BlockSel = nil
	c.CellSel = nil
	c.lastEd = nil
	c.Dirty = false
	c.FixSelection()
}

// BreakCoalescing 停止与上一步合并。
func (c *Core) BreakCoalescing() { c.lastEd = nil }

func newSnapshot(doc Document, sel Selection) snapshot {
	// 深克隆：Document 按值传只是浅拷贝，切片会和当前文档共享底层数组。
	doc = doc.Clone()
	return snapshot{doc: doc, sel: sel, size: docBytes(&doc)}
}

func (c *Core) pushUndo(s snapshot) {
	c.undo = append(c.undo, s)
	total := 0
	for i := range c.undo {
		total += c.undo[i].size
	}
	for len(c.undo) > undoSteps || (total > undoBytes && len(c.undo) > undoMinSteps) {
		total -= c.undo[0].size
		c.undo = c.undo[1:]
	}
}

// Undo 撤销一步。
func (c *Core) Undo() bool {
	if len(c.undo) == 0 {
		return false
	}
	c.Generation++
	s := c.undo[len(c.undo)-1]
	c.undo = c.undo[:len(c.undo)-1]
	doc := s.doc.Clone()
	keepOutsideHistory(&c.Doc, &doc)
	c.redo = append(c.redo, newSnapshot(c.Doc, c.Sel))
	c.Doc = doc
	c.Sel = s.sel
	c.BlockSel = nil
	c.CellSel = nil
	c.lastEd = nil
	c.Dirty = true
	c.FixSelection()
	return true
}

// Redo 重做一步。
func (c *Core) Redo() bool {
	if len(c.redo) == 0 {
		return false
	}
	c.Generation++
	s := c.redo[len(c.redo)-1]
	c.redo = c.redo[:len(c.redo)-1]
	doc := s.doc.Clone()
	keepOutsideHistory(&c.Doc, &doc)
	back := newSnapshot(c.Doc, c.Sel)
	c.Doc = doc
	c.pushUndo(back)
	c.Sel = s.sel
	c.BlockSel = nil
	c.CellSel = nil
	c.lastEd = nil
	c.Dirty = true
	c.FixSelection()
	return true
}

// FixSelection 把选区夹到存在的块和合法字符边界上。
func (c *Core) FixSelection() {
	us := c.Units()
	fix := func(p Pos) Pos {
		if t := c.TextOf(p.Block); t != nil {
			o := min(p.Offset, t.Len())
			for o > 0 && o < len(t.Text) && !utf8.RuneStart(t.Text[o]) {
				o--
			}
			return NewPos(p.Block, o)
		}
		for i := range us {
			if us[i].ID == p.Block {
				return NewPos(p.Block, 0)
			}
		}
		if len(us) > 1 {
			return NewPos(us[1].ID, 0)
		}
		return NewPos(TitleID, 0)
	}
	c.Sel = Selection{Anchor: fix(c.Sel.Anchor), Head: fix(c.Sel.Head)}
}

// ------------------------------------------------------------------ 基础修改（无快照）

func (c *Core) ensureNonEmpty() {
	if len(c.Doc.Blocks) == 0 {
		c.Doc.Blocks = append(c.Doc.Blocks, BlockText(""))
	}
}

// removeLift 删除块，其子块提升到它的位置。
func (c *Core) removeLift(id BlockID) {
	p, ok := c.path(id)
	if !ok {
		return
	}
	list, i, ok := containerMut(&c.Doc.Blocks, p)
	if !ok {
		return
	}
	b := removeBlockAt(list, i)
	for k, ch := range b.Children {
		insertBlockAt(list, i+k, ch)
	}
}

func (c *Core) removeSubtree(id BlockID) *Block {
	p, ok := c.path(id)
	if !ok {
		return nil
	}
	list, i, ok := containerMut(&c.Doc.Blocks, p)
	if !ok {
		return nil
	}
	b := removeBlockAt(list, i)
	return &b
}

// InsertAfter 把 b 插到 id 后面（标题后面即正文开头）。
func (c *Core) InsertAfter(id BlockID, b Block) {
	if id == TitleID {
		insertBlockAt(&c.Doc.Blocks, 0, b)
		return
	}
	p, ok := c.path(id)
	if !ok {
		return
	}
	if list, i, ok := containerMut(&c.Doc.Blocks, p); ok {
		insertBlockAt(list, i+1, b)
	}
}

func (c *Core) insertAllAfter(id BlockID, blocks []Block) {
	anchor := id
	for _, b := range blocks {
		next := b.ID
		c.InsertAfter(anchor, b)
		anchor = next
	}
}

func (c *Core) insertBefore(id BlockID, b Block) {
	p, ok := c.path(id)
	if !ok {
		return
	}
	if list, i, ok := containerMut(&c.Doc.Blocks, p); ok {
		insertBlockAt(list, i, b)
	}
}

// normalize 清掉删除/移动留下的空容器。
func (c *Core) normalize() {
	var walk func(list *[]Block, inCell bool)
	walk = func(list *[]Block, inCell bool) {
		for i := range *list {
			b := &(*list)[i]
			walk(&b.Children, false)
			if tb, ok := b.Kind.(Table); ok {
				for r := range tb.Rows {
					for cc := range tb.Rows[r] {
						walk(&tb.Rows[r][cc].Blocks, true)
						if len(tb.Rows[r][cc].Blocks) == 0 && !tb.Rows[r][cc].IsCovered() {
							tb.Rows[r][cc].Blocks = append(tb.Rows[r][cc].Blocks, BlockText(""))
						}
					}
				}
			}
			if _, isGrid := b.Kind.(Grid); isGrid {
				dropEmptyColumns(&b.Children)
			}
		}
		retainBlocks(list, func(b *Block) bool {
			switch b.Kind.(type) {
			case Quote, Callout:
				return len(b.Children) > 0
			}
			return true
		})
		// 只剩一栏的 grid 溶解成其内容。
		i := 0
		for i < len(*list) {
			if _, isGrid := (*list)[i].Kind.(Grid); isGrid && len((*list)[i].Children) <= 1 {
				g := removeBlockAt(list, i)
				var content []Block
				for _, col := range g.Children {
					content = append(content, col.Children...)
				}
				n := len(content)
				insertBlocksAt(list, i, content)
				i += n
			} else {
				i++
			}
		}
		_ = inCell
	}
	walk(&c.Doc.Blocks, false)
	c.ensureNonEmpty()
}

// deleteSelectionInner 删除当前选区（如有），返回合并后的光标。
func (c *Core) deleteSelectionInner() Pos {
	s, e := c.Ordered()
	if s == e {
		return s
	}
	if s.Block == e.Block {
		if t := c.textMut(s.Block); t != nil {
			empty := RichText{}
			t.Replace(s.Offset, e.Offset, &empty)
		} else {
			// 单独选中的原子块。
			return c.deleteAtomic(s.Block)
		}
		c.Sel = Caret(s)
		return s
	}
	us := c.Units()
	si := c.unitIndex(us, s.Block)
	ei := c.unitIndex(us, e.Block)
	if si < 0 {
		si = 0
	}
	if ei < 0 {
		ei = 0
	}
	startUnit, endUnit := us[si], us[ei]
	// 结束块的尾巴并进开始块。
	var tail RichText
	if endUnit.Text {
		if t := c.TextOf(e.Block); t != nil {
			tail = t.Slice(e.Offset, t.Len())
		}
	}
	var middle []BlockID
	for _, u := range us[si+1 : ei] {
		middle = append(middle, u.ID)
	}
	if startUnit.Text {
		if c.IsCode(s.Block) {
			tail = Plain(tail.PlainText())
		}
		if t := c.textMut(s.Block); t != nil {
			length := t.Len()
			t.Replace(s.Offset, length, &tail)
		}
		for i := len(middle) - 1; i >= 0; i-- {
			c.removeLift(middle[i])
		}
		if e.Block != TitleID {
			// 结束块的子块：开始块能收则收，否则跟在后面。
			var kids []Block
			if b := c.BlockMut(e.Block); b != nil {
				kids = b.Children
				b.Children = nil
			}
			c.removeLift(e.Block)
			if sb := c.BlockMut(s.Block); sb != nil && Nests(sb.Kind) {
				sb.Children = append(kids, sb.Children...)
			} else {
				c.insertAllAfter(s.Block, kids)
			}
		}
		c.normalize()
		c.Sel = Caret(s)
		return s
	}
	// 选区起于原子块：删到结束块为止。
	for i := len(middle) - 1; i >= 0; i-- {
		c.removeLift(middle[i])
	}
	if endUnit.Text {
		if t := c.textMut(e.Block); t != nil {
			empty := RichText{}
			t.Replace(0, e.Offset, &empty)
		}
		c.removeSubtree(s.Block)
		c.normalize()
		p := NewPos(e.Block, 0)
		c.Sel = Caret(p)
		return p
	}
	c.removeSubtree(e.Block)
	return c.deleteAtomic(s.Block)
}

// deleteAtomic 删除原子块，光标落到邻居（或新段落）上。
func (c *Core) deleteAtomic(id BlockID) Pos {
	us := c.Units()
	i := c.unitIndex(us, id)
	if i < 0 {
		i = 0
	}
	p, ok := c.path(id)
	if !ok {
		return c.Sel.Head
	}
	inScope := us[i].Scope
	var next, prev BlockID
	if i+1 < len(us) && us[i+1].Text && us[i+1].Scope == inScope {
		next = us[i+1].ID
	}
	if i > 0 && us[i-1].Text && us[i-1].Scope == inScope && us[i-1].ID != TitleID {
		prev = us[i-1].ID
	}
	list, k, _ := containerMut(&c.Doc.Blocks, p)
	var pos Pos
	if n := next; n != 0 {
		removeBlockAt(list, k)
		pos = NewPos(n, 0)
	} else if n := prev; n != 0 {
		removeBlockAt(list, k)
		pos = NewPos(n, c.LenOf(n))
	} else {
		nb := BlockText("")
		nid := nb.ID
		(*list)[k] = nb
		pos = NewPos(nid, 0)
	}
	c.normalize()
	c.Sel = Caret(pos)
	return pos
}

// ------------------------------------------------------------------ 编辑命令

// DeleteSelection 删除当前选区（有选区时）。
func (c *Core) DeleteSelection() {
	if !c.Sel.IsCollapsed() {
		c.Checkpoint(EditOther)
		c.deleteSelectionInner()
	}
}

// InsertText 在选区处输入文本；文本块里的 \n 是软换行。
func (c *Core) InsertText(s string) {
	c.Checkpoint(EditTyping)
	p := c.deleteSelectionInner()
	if c.TextOf(p.Block) == nil {
		// 光标在原子块上：输入进下面新建的段落。
		b := BlockText("")
		id := b.ID
		c.InsertAfter(p.Block, b)
		p = NewPos(id, 0)
	}
	marks := Marks{}
	if pm := c.PendingMarks; pm != nil {
		c.PendingMarks = nil
		marks = *pm
	} else if t := c.TextOf(p.Block); t != nil {
		marks = t.MarksAt(p.Offset)
	}
	if c.IsCode(p.Block) || p.Block == TitleID {
		marks = Marks{}
	}
	if t := c.textMut(p.Block); t != nil {
		t.InsertPlain(p.Offset, s, marks)
	}
	c.Sel = Caret(NewPos(p.Block, p.Offset+len(s)))
}

// ReplaceInBlock 替换光标块里的区间（IME / 输入法路径）。
func (c *Core) ReplaceInBlock(id BlockID, start, end int, s string) {
	c.Checkpoint(EditTyping)
	marks := Marks{}
	if t := c.TextOf(id); t != nil {
		marks = t.MarksAt(start)
	}
	if pm := c.PendingMarks; pm != nil {
		c.PendingMarks = nil
		marks = *pm
	}
	if c.IsCode(id) || id == TitleID {
		marks = Marks{}
	}
	if t := c.textMut(id); t != nil {
		var ins RichText
		ins.Push(s, marks)
		e := min(end, t.Len())
		t.Replace(start, e, &ins)
	}
	c.Sel = Caret(NewPos(id, start+len(s)))
}

// InsertEquation 在光标处插入公式，返回其位置。
func (c *Core) InsertEquation(src string) *Pos {
	return c.insertObject(Equation{Src: src})
}

// InsertButton 在光标处插入按钮。
func (c *Core) InsertButton(b Button) *Pos {
	return c.insertObject(b)
}

// InsertReminder 在光标处插入日期提醒。
func (c *Core) InsertReminder(r Reminder) *Pos {
	return c.insertObject(r)
}

// InsertDocLink 在光标处插入 @文档。
func (c *Core) InsertDocLink(l InlineDocLink) *Pos {
	return c.insertObject(l)
}

func (c *Core) insertObject(kind InlineKind) *Pos {
	c.Checkpoint(EditOther)
	c.BreakCoalescing()
	p := c.deleteSelectionInner()
	if c.IsCode(p.Block) || p.Block == TitleID {
		return nil
	}
	t := c.TextOf(p.Block)
	if t == nil {
		return nil
	}
	marks := t.MarksAt(p.Offset)
	obj := ToInline(kind)
	marks.Inline = &obj
	tm := c.textMut(p.Block)
	if tm == nil {
		return nil
	}
	var ins RichText
	ins.Push(EquationRune, marks)
	tm.Replace(p.Offset, p.Offset, &ins)
	c.Sel = Caret(NewPos(p.Block, p.Offset+len(EquationRune)))
	return &p
}

// ObjectAt 返回 p 处的行内对象。
func (c *Core) ObjectAt(p Pos) *Inline {
	t := c.TextOf(p.Block)
	if t == nil {
		return nil
	}
	for _, sp := range t.Spans() {
		if sp.Start <= p.Offset && p.Offset < sp.End {
			return sp.Marks.Inline
		}
	}
	return nil
}

// setObject 用 kind 替换 p 处的对象（公式编辑的实时更新合并为一步）。
func (c *Core) setObject(p Pos, kind InlineKind) {
	obj := ToInline(kind)
	old := c.ObjectAt(p)
	if old == nil || old.Kind == obj.Kind {
		return
	}
	keep := c.Sel
	c.Sel = Caret(p)
	if _, isEq := obj.Kind.(Equation); isEq {
		c.Checkpoint(EditEquation)
	} else {
		c.Checkpoint(EditOther)
		c.BreakCoalescing()
	}
	c.Sel = keep
	if t := c.textMut(p.Block); t != nil {
		t.UpdateMarks(p.Offset, p.Offset+len(EquationRune), func(m *Marks) { m.Inline = &obj })
	}
}

// DocLinkAt 返回 p 处的 @文档。
func (c *Core) DocLinkAt(p Pos) (InlineDocLink, bool) {
	if o := c.ObjectAt(p); o != nil {
		return o.DocLink()
	}
	return InlineDocLink{}, false
}

// EquationSource 返回 p 处公式的 LaTeX 源码。
func (c *Core) EquationSource(p Pos) (string, bool) {
	if o := c.ObjectAt(p); o != nil {
		return o.Equation()
	}
	return "", false
}

// SetEquationSource 替换 p 处公式的源码。
func (c *Core) SetEquationSource(p Pos, src string) {
	if _, ok := c.EquationSource(p); ok {
		c.setObject(p, Equation{Src: src})
	}
}

// ButtonAt 返回 p 处的按钮。
func (c *Core) ButtonAt(p Pos) (Button, bool) {
	if o := c.ObjectAt(p); o != nil {
		return o.Button()
	}
	return Button{}, false
}

// SetButton 替换 p 处的按钮。
func (c *Core) SetButton(p Pos, b Button) {
	if _, ok := c.ButtonAt(p); ok {
		c.setObject(p, b)
	}
}

// ReminderAt 返回 p 处的日期提醒。
func (c *Core) ReminderAt(p Pos) (Reminder, bool) {
	if o := c.ObjectAt(p); o != nil {
		return o.Reminder()
	}
	return Reminder{}, false
}

// SetReminder 替换 p 处的日期提醒。
func (c *Core) SetReminder(p Pos, r Reminder) {
	if _, ok := c.ReminderAt(p); ok {
		c.setObject(p, r)
	}
}

// FileAt 返回 p 处的行内附件（文本视图）。
func (c *Core) FileAt(p Pos) (FileBlock, bool) {
	if o := c.ObjectAt(p); o != nil {
		return o.File()
	}
	return FileBlock{}, false
}

// SetFileView 设置附件块的视图。
func (c *Core) SetFileView(id BlockID, view FileView) {
	if f, ok := c.KindOf(id).(FileBlock); !ok || f.View == view {
		return
	}
	c.Checkpoint(EditOther)
	c.BreakCoalescing()
	if b := c.BlockMut(id); b != nil {
		if f, ok := b.Kind.(FileBlock); ok {
			f.View = view
			b.Kind = f
		}
	}
}

// FileToInline 文本视图：附件块变成持有行内附件的段落（对齐不变），光
// 标落在其后。
func (c *Core) FileToInline(id BlockID) {
	f, ok := c.KindOf(id).(FileBlock)
	if !ok {
		return
	}
	c.Checkpoint(EditOther)
	c.BreakCoalescing()
	b := c.BlockMut(id)
	if b == nil {
		return
	}
	obj := ToInline(InlineFile{File: f})
	var t RichText
	t.Push(EquationRune, Marks{Inline: &obj})
	b.Kind = Text{}
	b.Text = t
	c.BlockSel = nil
	c.Sel = Caret(NewPos(id, len(EquationRune)))
}

// InlineToFile 卡片/预览视图：行内附件移出段落成为其后的一块（段落空
// 时取而代之）。
func (c *Core) InlineToFile(p Pos, view FileView) *BlockID {
	f, ok := c.FileAt(p)
	if !ok {
		return nil
	}
	f.View = view
	c.Checkpoint(EditOther)
	c.BreakCoalescing()
	t := c.textMut(p.Block)
	if t == nil {
		return nil
	}
	empty := RichText{}
	t.Replace(p.Offset, p.Offset+len(EquationRune), &empty)
	b := c.Block(p.Block)
	if b == nil {
		return nil
	}
	_, isText := b.Kind.(Text)
	isEmpty := isText && b.Text.IsEmpty() && len(b.Children) == 0
	var id BlockID
	if isEmpty {
		bm := c.BlockMut(p.Block)
		if bm == nil {
			return nil
		}
		bm.Kind = f
		id = bm.ID
	} else {
		nb := NewBlock(f, RichText{})
		id = nb.ID
		c.InsertAfter(p.Block, nb)
	}
	c.BlockSel = nil
	c.Sel = Caret(NewPos(id, 0))
	return &id
}

// InsertBlocks 像 InsertBlock 一样插入一排块，返回最后一个的 id。
func (c *Core) InsertBlocks(blocks []Block) *BlockID {
	if len(blocks) == 0 {
		return nil
	}
	last := c.InsertBlock(blocks[0])
	for _, b := range blocks[1:] {
		id := b.ID
		c.InsertAfter(last, b)
		last = id
	}
	return &last
}

// IsCode 报告是否代码块。
func (c *Core) IsCode(id BlockID) bool {
	b := c.Block(id)
	if b == nil {
		return false
	}
	_, ok := b.Kind.(Code)
	return ok
}

// KindOf 返回块的类型。
func (c *Core) KindOf(id BlockID) BlockKind {
	if b := c.Block(id); b != nil {
		return b.Kind
	}
	return nil
}

// Enter 回车。
func (c *Core) Enter() {
	c.Checkpoint(EditOther)
	c.BreakCoalescing()
	p := c.deleteSelectionInner()
	if p.Block == TitleID {
		// 标题里回车进入正文，标题文本从光标处切开。
		tail := c.Doc.Title.SplitOff(p.Offset)
		first := BlockID(0)
		firstEmpty := false
		if len(c.Doc.Blocks) > 0 {
			first = c.Doc.Blocks[0].ID
			_, isText := c.Doc.Blocks[0].Kind.(Text)
			firstEmpty = isText && c.Doc.Blocks[0].Text.IsEmpty() && len(c.Doc.Blocks[0].Children) == 0
		}
		if tail.IsEmpty() && firstEmpty {
			c.Sel = Caret(NewPos(first, 0))
		} else {
			b := NewBlock(Text{}, tail)
			id := b.ID
			insertBlockAt(&c.Doc.Blocks, 0, b)
			c.Sel = Caret(NewPos(id, 0))
		}
		return
	}
	block := c.Block(p.Block)
	if block == nil {
		// 原子块：后面加一段。
		b := BlockText("")
		id := b.ID
		c.InsertAfter(p.Block, b)
		c.Sel = Caret(NewPos(id, 0))
		return
	}
	if !HasText(block.Kind) {
		b := BlockText("")
		id := b.ID
		c.InsertAfter(p.Block, b)
		c.Sel = Caret(NewPos(id, 0))
		return
	}
	if _, isCode := block.Kind.(Code); isCode {
		// 新行保持当前行缩进。
		before := block.Text.Text[:p.Offset]
		line := before
		if i := strings.LastIndexByte(before, '\n'); i >= 0 {
			line = before[i+1:]
		}
		indent := line[:leadingSpaces(line)]
		ins := "\n" + indent
		if t := c.textMut(p.Block); t != nil {
			t.InsertPlain(p.Offset, ins, Marks{})
		}
		c.Sel = Caret(NewPos(p.Block, p.Offset+len(ins)))
		return
	}
	empty := block.Text.IsEmpty()
	// 空列表项/待办：嵌套则降级，否则变文本。
	if empty && IsList(block.Kind) {
		if c.parentIsTextBlock(p.Block) {
			c.outdentOne(p.Block)
		} else if b := c.BlockMut(p.Block); b != nil {
			b.Kind = Text{}
		}
		c.Sel = Caret(NewPos(p.Block, 0))
		return
	}
	// 引用/高亮块里的末尾空段退出容器。
	if empty {
		if _, isText := block.Kind.(Text); isText && len(block.Children) == 0 {
			if parent := c.ParentOf(p.Block); parent != nil {
				_, isQuote := parent.Kind.(Quote)
				_, isCallout := parent.Kind.(Callout)
				if (isQuote || isCallout) && len(parent.Children) > 0 && parent.Children[len(parent.Children)-1].ID == p.Block && len(parent.Children) > 1 {
					if b := c.removeSubtree(p.Block); b != nil {
						c.InsertAfter(parent.ID, *b)
					}
					c.Sel = Caret(NewPos(p.Block, 0))
					return
				}
			}
			if c.parentIsTextBlock(p.Block) {
				c.outdentOne(p.Block)
				c.Sel = Caret(NewPos(p.Block, 0))
				return
			}
		}
	}
	newKind := cloneKindShallow(block.Kind)
	if h, ok := block.Kind.(Heading); ok {
		if block.Folded {
			// 折叠标题的小节里加段仍会被藏住；同级标题结束小节。
			newKind = Heading{Level: h.Level}
		} else {
			newKind = Text{}
		}
	} else if _, ok := block.Kind.(Ordered); ok {
		newKind = Ordered{}
	} else if _, ok := block.Kind.(Todo); ok {
		newKind = Todo{}
	}
	if p.Offset == 0 && !empty {
		// 光标在开头：上面压一个同类的空块。
		kind := cloneKindShallow(newKind)
		if _, isHeading := block.Kind.(Heading); isHeading {
			kind = Text{}
		}
		nb := NewBlock(kind, RichText{})
		nb.Align = block.Align
		c.insertBefore(p.Block, nb)
		c.Sel = Caret(NewPos(p.Block, 0))
		return
	}
	var tail RichText
	if t := c.textMut(p.Block); t != nil {
		tail = t.SplitOff(p.Offset)
	}
	kind := newKind
	if !tail.IsEmpty() {
		kind = resetForSplit(block.Kind)
	}
	nb := NewBlock(kind, tail)
	nb.Align = block.Align
	if !nb.Text.IsEmpty() {
		nb.Bg = clonePtr(block.Bg)
	}
	nid := nb.ID
	hasKids := len(block.Children) > 0 && !block.Folded
	if hasKids {
		// 新块成为第一个子块（outliner 式）。
		if b := c.BlockMut(p.Block); b != nil {
			insertBlockAt(&b.Children, 0, nb)
		}
	} else {
		// 折叠标题保住小节：新块落在小节之后。
		after := c.FoldEnd(p.Block)
		c.InsertAfter(after, nb)
	}
	c.Sel = Caret(NewPos(nid, 0))
}

func leadingSpaces(s string) int {
	n := 0
	for n < len(s) && (s[n] == ' ' || s[n] == '\t') {
		n++
	}
	return n
}

func (c *Core) parentIsTextBlock(id BlockID) bool {
	if p := c.ParentOf(id); p != nil {
		return HasText(p.Kind)
	}
	return false
}

// Backspace 退格。
func (c *Core) Backspace() {
	if !c.Sel.IsCollapsed() {
		c.Checkpoint(EditDelete)
		c.deleteSelectionInner()
		return
	}
	p := c.Sel.Head
	if p.Offset > 0 {
		c.Checkpoint(EditDelete)
		t := c.textMut(p.Block)
		start := prevGrapheme(t.Text, p.Offset)
		empty := RichText{}
		t.Replace(start, p.Offset, &empty)
		c.Sel = Caret(NewPos(p.Block, start))
		return
	}
	if p.Block == TitleID {
		return
	}
	block := c.Block(p.Block)
	if block == nil || !HasText(block.Kind) {
		c.Checkpoint(EditOther)
		c.deleteAtomic(p.Block)
		return
	}
	c.Checkpoint(EditOther)
	c.BreakCoalescing()
	if _, isCode := block.Kind.(Code); isCode {
		if block.Text.IsEmpty() {
			if b := c.BlockMut(p.Block); b != nil {
				b.Kind = Text{}
			}
		}
		return
	}
	if _, isText := block.Kind.(Text); !isText {
		// 非文本块在偏移 0 处先降级成段落。
		if b := c.BlockMut(p.Block); b != nil {
			b.Kind = Text{}
		}
		return
	}
	// 缩进段落：降级。
	if c.parentIsTextBlock(p.Block) {
		c.outdentOne(p.Block)
		return
	}
	// 引用/高亮块里的第一段：提出来。
	if parent := c.ParentOf(p.Block); parent != nil {
		_, isQuote := parent.Kind.(Quote)
		_, isCallout := parent.Kind.(Callout)
		if (isQuote || isCallout) && len(parent.Children) > 0 && parent.Children[0].ID == p.Block {
			if b := c.removeSubtree(p.Block); b != nil {
				c.insertBefore(parent.ID, *b)
			}
			c.normalize()
			return
		}
	}
	c.mergeIntoPrevious(p.Block)
}

func (c *Core) mergeIntoPrevious(id BlockID) {
	us := c.Units()
	i := c.unitIndex(us, id)
	if i < 0 {
		return
	}
	if i == 0 {
		return
	}
	cur, prev := us[i], us[i-1]
	// 退格不出单元格。单元格首行为空且后面还有段时删掉首行、光标过去；
	// 单元格空了则选中表格，继续退格即删除表格。
	if p, ok := c.path(id); ok && len(p) > 0 {
		last := p[len(p)-1]
		if last.Kind == SegCell && last.Index == 0 {
			if c.LenOf(id) == 0 {
				if b := c.Block(id); b != nil && len(b.Children) == 0 {
					if _, isText := b.Kind.(Text); isText {
						table := c.tableScopeAt(p)
						p = append(Path{}, p...)
						p[len(p)-1].Index = 1
						if nb := blockAt(c.Doc.Blocks, p); nb != nil {
							if HasText(nb.Kind) {
								c.removeSubtree(id)
								c.normalize()
								c.Sel = Caret(NewPos(nb.ID, 0))
							}
						} else if table != 0 {
							c.SelectBlock(table)
						}
					}
				}
			}
			return
		}
	}
	if prev.Scope != cur.Scope {
		// 前一个 unit 是紧邻其前的表格里的单元格：空段先消失，然后
		// 选中表格，下一次退格删除它。
		if t := c.enclosingTableIn(prev.ID, cur.Scope); t != 0 {
			if c.LenOf(id) == 0 {
				if b := c.Block(id); b != nil && len(b.Children) == 0 {
					if _, isText := b.Kind.(Text); isText {
						c.removeSubtree(id)
						c.normalize()
						c.Sel = Caret(NewPos(prev.ID, c.LenOf(prev.ID)))
					} else {
						c.Sel = Caret(NewPos(id, 0))
					}
				}
			}
			c.SelectBlock(t)
			return
		}
		return
	}
	if !prev.Text {
		// 前一个是原子块（分割线、图片…）：空段直接消失，否则删原子块。
		if c.LenOf(id) == 0 {
			if b := c.Block(id); b != nil && len(b.Children) == 0 {
				c.removeSubtree(id)
				c.normalize()
				c.Sel = Caret(NewPos(prev.ID, 0))
			}
		} else {
			c.removeSubtree(prev.ID)
			c.normalize()
			c.Sel = Caret(NewPos(id, 0))
		}
		return
	}
	if c.IsCode(prev.ID) {
		// 不把文本并进代码；只挪光标。
		c.Sel = Caret(NewPos(prev.ID, c.LenOf(prev.ID)))
		return
	}
	var text RichText
	if t := c.TextOf(id); t != nil {
		text = t.Clone()
	}
	var kids []Block
	if b := c.BlockMut(id); b != nil {
		kids = b.Children
		b.Children = nil
	}
	at := c.LenOf(prev.ID)
	if t := c.textMut(prev.ID); t != nil {
		t.Append(&text)
	}
	c.removeSubtree(id)
	if pb := c.BlockMut(prev.ID); pb != nil && Nests(pb.Kind) {
		pb.Children = append(pb.Children, kids...)
	} else {
		c.insertAllAfter(prev.ID, kids)
	}
	c.normalize()
	c.Sel = Caret(NewPos(prev.ID, at))
}

// DeleteForward 前向删除。
func (c *Core) DeleteForward() {
	if !c.Sel.IsCollapsed() {
		c.Checkpoint(EditDelete)
		c.deleteSelectionInner()
		return
	}
	p := c.Sel.Head
	t := c.TextOf(p.Block)
	if t == nil {
		c.Checkpoint(EditOther)
		c.deleteAtomic(p.Block)
		return
	}
	length := t.Len()
	if p.Offset < length {
		c.Checkpoint(EditDelete)
		tm := c.textMut(p.Block)
		end := nextGrapheme(tm.Text, p.Offset)
		empty := RichText{}
		tm.Replace(p.Offset, end, &empty)
		return
	}
	us := c.Units()
	i := c.unitIndex(us, p.Block)
	if i < 0 || i+1 >= len(us) {
		return
	}
	next := us[i+1]
	if next.Scope != us[i].Scope {
		// 后面是表格：先选中它，下一次删除移除。
		if t := c.enclosingTableIn(next.ID, us[i].Scope); t != 0 {
			c.Checkpoint(EditOther)
			if p.Block != TitleID && length == 0 {
				if b := c.Block(p.Block); b != nil && len(b.Children) == 0 {
					if _, isText := b.Kind.(Text); isText {
						c.removeSubtree(p.Block)
						c.normalize()
						c.Sel = Caret(NewPos(next.ID, 0))
					}
				}
			}
			c.SelectBlock(t)
		}
		return
	}
	// Delete 也不把下一个单元格拉进来。
	if q, ok := c.path(next.ID); ok && len(q) > 0 && q[len(q)-1].Kind == SegCell && q[len(q)-1].Index == 0 {
		return
	}
	c.Checkpoint(EditOther)
	if !next.Text {
		c.removeSubtree(next.ID)
		c.normalize()
		c.Sel = Caret(p)
		return
	}
	if c.IsCode(next.ID) || c.IsCode(p.Block) {
		return
	}
	c.mergeIntoPrevious(next.ID)
	c.Sel = Caret(p)
}

// DeleteToStart 删到块首（删到视觉行首是 GUI 侧的事）。
func (c *Core) DeleteToStart() {
	p := c.Sel.Head
	if c.Sel.IsCollapsed() && p.Offset > 0 {
		c.Sel.Anchor = NewPos(p.Block, 0)
	}
	c.Backspace()
}

// DeleteWordBack 按词退格。
func (c *Core) DeleteWordBack() {
	p := c.Sel.Head
	if c.Sel.IsCollapsed() && p.Offset > 0 {
		if t := c.TextOf(p.Block); t != nil {
			c.Sel.Anchor = NewPos(p.Block, prevWord(t.Text, p.Offset))
		}
	}
	c.Backspace()
}

// ------------------------------------------------------------------ 缩进

// SelectedBlocks 是选区触及的最外层块（没有祖先也被选中）。
func (c *Core) SelectedBlocks() []BlockID {
	if c.BlockSel != nil {
		return c.BlockSelIDs()
	}
	if c.CellSel != nil {
		return c.CellBlocks()
	}
	us := c.Units()
	s, e := c.OrderedIn(us)
	si := c.unitIndex(us, s.Block)
	ei := c.unitIndex(us, e.Block)
	if si < 0 {
		si = 0
	}
	if ei < 0 {
		ei = 0
	}
	var ids []BlockID
	for _, u := range us[si : ei+1] {
		if u.ID != TitleID {
			ids = append(ids, u.ID)
		}
	}
	return c.topmostIn(ids, pathIndex(c.Doc.Blocks))
}

// Indent 把选中的块整体缩进一级。
func (c *Core) Indent() {
	ids := c.SelectedBlocks()
	if len(ids) == 0 {
		return
	}
	c.Checkpoint(EditOther)
	for _, id := range ids {
		c.indentOne(id)
	}
}

// CanIndent：Tab 能否把 id 嵌到前一个同级下面。
func (c *Core) CanIndent(id BlockID) bool {
	p, ok := c.path(id)
	if !ok || len(p) == 0 {
		return false
	}
	i := segIndex(p[len(p)-1])
	if i == 0 {
		return false
	}
	prevPath := append(Path{}, p...)
	prevPath[len(prevPath)-1] = withIndex(p[len(p)-1], i-1)
	prev := blockAt(c.Doc.Blocks, prevPath)
	if prev == nil {
		return false
	}
	me := blockAt(c.Doc.Blocks, p)
	_, isColumn := me.Kind.(Column)
	return Nests(prev.Kind) && !isColumn && nestDepth(p) < MaxNestDepth
}

// CanOutdent：Shift+Tab 能否把 id 提出父块。
func (c *Core) CanOutdent(id BlockID) bool {
	p, ok := c.path(id)
	if !ok {
		return false
	}
	pp, ok := parentPath(p)
	if !ok {
		return false
	}
	parent := blockAt(c.Doc.Blocks, pp)
	if parent == nil {
		return false
	}
	lastIsCell := p[len(p)-1].Kind == SegCell
	return HasText(parent.Kind) && !lastIsCell
}

func (c *Core) indentOne(id BlockID) bool {
	p, ok := c.path(id)
	if !ok || len(p) == 0 {
		return false
	}
	i := segIndex(p[len(p)-1])
	if i == 0 {
		return false
	}
	prevPath := append(Path{}, p...)
	prevPath[len(prevPath)-1] = withIndex(p[len(p)-1], i-1)
	prev := blockAt(c.Doc.Blocks, prevPath)
	if prev == nil {
		return false
	}
	// 可嵌套的父块：文本、标题和列表。
	canParent := Nests(prev.Kind)
	me := blockAt(c.Doc.Blocks, p)
	_, isColumn := me.Kind.(Column)
	if !canParent || isColumn || nestDepth(p) >= MaxNestDepth {
		return false
	}
	b := c.removeSubtree(id)
	if b == nil {
		return false
	}
	parent := blockAt(c.Doc.Blocks, prevPath)
	if parent == nil {
		return false
	}
	parent.Children = append(parent.Children, *b)
	return true
}

// Outdent 把选中的块整体降级。
func (c *Core) Outdent() {
	ids := c.SelectedBlocks()
	if len(ids) == 0 {
		return
	}
	c.Checkpoint(EditOther)
	for i := len(ids) - 1; i >= 0; i-- {
		c.outdentOne(ids[i])
	}
}

// outdentOne 把嵌套块移到父块后；后面的同级变成它的子块。
func (c *Core) outdentOne(id BlockID) bool {
	p, ok := c.path(id)
	if !ok {
		return false
	}
	pp, ok := parentPath(p)
	if !ok {
		return false
	}
	parent := blockAt(c.Doc.Blocks, pp)
	if parent == nil || !HasText(parent.Kind) || p[len(p)-1].Kind == SegCell {
		return false
	}
	parentID := parent.ID
	i := segIndex(p[len(p)-1])
	pb := blockAt(c.Doc.Blocks, pp)
	if pb == nil {
		return false
	}
	b := removeBlockAt(&pb.Children, i)
	var following []Block
	following = append(following, pb.Children[i:]...)
	pb.Children = pb.Children[:i]
	// 它们留在其下：作子块，或不能带子块时跟在后面。
	id = b.ID
	if Nests(b.Kind) {
		b.Children = append(b.Children, following...)
		c.InsertAfter(parentID, b)
	} else {
		c.InsertAfter(parentID, b)
		c.insertAllAfter(id, following)
	}
	return true
}

// OrderedNumberOptions 返回编号选项：当前编号，和「承接上一项」的值
// （没有上一项时 hasCont 为 false）。
func (c *Core) OrderedNumberOptions(id BlockID) (num uint32, cont uint32, hasCont bool) {
	p, ok := c.path(id)
	if !ok {
		return 0, 0, false
	}
	list, i, ok := siblingsAt(c.Doc.Blocks, p)
	if !ok || i >= len(list) {
		return 0, 0, false
	}
	if _, isOrdered := list[i].Kind.(Ordered); !isOrdered {
		return 0, 0, false
	}
	previous := -1
	for j := i - 1; j >= 0; j-- {
		if _, isOrdered := list[j].Kind.(Ordered); isOrdered {
			previous = j
			break
		}
	}
	if previous >= 0 {
		return orderedNumber(list, i), satAdd32(orderedNumber(list, previous), 1), true
	}
	return orderedNumber(list, i), 0, false
}

func satAdd32(a, b uint32) uint32 {
	s := a + b
	if s < a { // 溢出
		return ^uint32(0)
	}
	return s
}

// SetOrderedNumber 设置有序项编号（承接上一项或固定值）。
func (c *Core) SetOrderedNumber(id BlockID, value uint32, continuePrevious bool) {
	if _, _, ok := c.OrderedNumberOptions(id); !ok || value == 0 {
		return
	}
	var seq *uint32
	if continuePrevious {
		p, ok := c.path(id)
		if !ok {
			return
		}
		list, i, ok := siblingsAt(c.Doc.Blocks, p)
		if ok && i > 0 {
			if _, isOrdered := list[i-1].Kind.(Ordered); isOrdered {
				seq = nil // 承接：清掉固定编号
			} else {
				seq = &value
			}
		} else if ok {
			seq = &value
		}
	} else {
		seq = &value
	}
	c.Checkpoint(EditOther)
	if b := c.BlockMut(id); b != nil {
		b.Kind = Ordered{Seq: seq}
	}
	c.BreakCoalescing()
}

// ------------------------------------------------------------------ 块类型变换

// kindTag 区分变体（对应 Rust 的 discriminant）。
func kindTag(k BlockKind) int {
	switch k.(type) {
	case Text:
		return 1
	case Heading:
		return 2
	case Bullet:
		return 3
	case Ordered:
		return 4
	case Todo:
		return 5
	case Code:
		return 6
	case Quote:
		return 7
	case Callout:
		return 8
	case Divider:
		return 9
	case Image:
		return 10
	case Table:
		return 11
	case Grid:
		return 12
	case Column:
		return 13
	case SubPageList:
		return 14
	case Countdown:
		return 15
	case FileBlock:
		return 16
	case Raw:
		return 17
	}
	return 0
}

// TurnInto 把 ids 换成 kind（引用/高亮块为包裹）。
func (c *Core) TurnInto(ids []BlockID, kind BlockKind) {
	var filtered []BlockID
	for _, id := range ids {
		if id != TitleID {
			filtered = append(filtered, id)
		}
	}
	if len(filtered) == 0 {
		return
	}
	c.Checkpoint(EditOther)
	c.BreakCoalescing()
	switch kind.(type) {
	case Quote, Callout:
		c.wrapBlocks(filtered, kind)
	default:
		for _, id := range filtered {
			c.turnOne(id, kind)
		}
	}
	c.normalize()
	c.FixSelection()
}

// ToggleTurnInto 是键盘切换：全部已是该类型时退回纯文本（引用则是退出）。
func (c *Core) ToggleTurnInto(ids []BlockID, kind BlockKind) {
	same := func(k BlockKind) bool {
		h1, ok1 := k.(Heading)
		h2, ok2 := kind.(Heading)
		if ok1 || ok2 {
			return ok1 && ok2 && h1.Level == h2.Level
		}
		return kindTag(k) == kindTag(kind)
	}
	_, wantQuote := kind.(Quote)
	already := len(ids) > 0
	for _, id := range ids {
		if wantQuote {
			p := c.ParentOf(id)
			var isQuote bool
			if p != nil {
				_, isQuote = p.Kind.(Quote)
			}
			if !isQuote {
				already = false
				break
			}
		} else if k := c.KindOf(id); k == nil || !same(k) {
			already = false
			break
		}
	}
	if !already {
		c.TurnInto(ids, kind)
		return
	}
	if wantQuote {
		c.Checkpoint(EditOther)
		for _, id := range ids {
			c.liftOutOfContainer(id)
		}
		c.normalize()
		c.FixSelection()
		return
	}
	c.TurnInto(ids, Text{})
}

// UnwrapContainer 把容器的子块提到它的位置。
func (c *Core) UnwrapContainer(children []BlockID) {
	c.Checkpoint(EditOther)
	for _, ch := range children {
		c.liftOutOfContainer(ch)
	}
	c.normalize()
	c.FixSelection()
}

// wrapBlocks 把相邻的同级块按段各自裹进一个引用/高亮块。
func (c *Core) wrapBlocks(ids []BlockID, kind BlockKind) {
	disc := kindTag(kind)
	// 已在同类容器里的块不动。
	var filtered []BlockID
	for _, id := range ids {
		if p := c.ParentOf(id); p != nil && kindTag(p.Kind) == disc {
			continue
		}
		if disc == kindTag(Quote{}) {
			if k := c.KindOf(id); k != nil {
				if _, isQuote := k.(Quote); isQuote {
					continue
				}
				if _, isTable := k.(Table); isTable {
					continue
				}
			}
		}
		filtered = append(filtered, id)
	}
	var groups [][]BlockID
	var last Path
	for _, id := range filtered {
		p, ok := c.path(id)
		if !ok {
			continue
		}
		adjacent := last != nil && len(last) == len(p) && pathEqual(last[:len(last)-1], p[:len(p)-1]) &&
			segIndex(last[len(last)-1])+1 == segIndex(p[len(p)-1])
		if adjacent {
			groups[len(groups)-1] = append(groups[len(groups)-1], id)
		} else {
			groups = append(groups, []BlockID{id})
		}
		last = p
	}
	for _, g := range groups {
		p, ok := c.path(g[0])
		if !ok {
			continue
		}
		list, i, ok := containerMut(&c.Doc.Blocks, p)
		if !ok {
			continue
		}
		inner := append([]Block{}, (*list)[i:i+len(g)]...)
		*list = append((*list)[:i], (*list)[i+len(g):]...)
		wrapper := NewBlock(cloneKindShallow(kind), RichText{})
		wrapper.Children = inner
		insertBlockAt(list, i, wrapper)
	}
}

// liftOutOfContainer 把块移出引用/高亮块（在中间时拆开容器）。
func (c *Core) liftOutOfContainer(id BlockID) {
	parent := c.ParentOf(id)
	if parent == nil {
		return
	}
	switch parent.Kind.(type) {
	case Quote, Callout:
	default:
		return
	}
	i := 0
	for j, ch := range parent.Children {
		if ch.ID == id {
			i = j
			break
		}
	}
	pp, ok := c.path(parent.ID)
	if !ok {
		return
	}
	list, k, ok := containerMut(&c.Doc.Blocks, pp)
	if !ok {
		return
	}
	before := removeBlockAt(list, k)
	kind := before.Kind
	afterKids := append([]Block{}, before.Children[i:]...)
	before.Children = before.Children[:i]
	me := afterKids[0]
	afterKids = afterKids[1:]
	at := k
	if len(before.Children) > 0 {
		insertBlockAt(list, at, before)
		at++
	}
	insertBlockAt(list, at, me)
	if len(afterKids) > 0 {
		rest := NewBlock(cloneKindShallow(kind), RichText{})
		rest.Children = afterKids
		insertBlockAt(list, at+1, rest)
	}
}

func (c *Core) turnOne(id BlockID, kind BlockKind) {
	if id == TitleID {
		return
	}
	switch kind.(type) {
	case Quote, Callout:
		// 已在这类容器里就不用再包。
		if p := c.ParentOf(id); p != nil && kindTag(p.Kind) == kindTag(kind) {
			return
		}
		p, ok := c.path(id)
		if !ok {
			return
		}
		list, i, ok := containerMut(&c.Doc.Blocks, p)
		if !ok {
			return
		}
		inner := removeBlockAt(list, i)
		wrapper := NewBlock(cloneKindShallow(kind), RichText{})
		wrapper.Children = []Block{inner}
		insertBlockAt(list, i, wrapper)
	default:
		b := c.BlockMut(id)
		if b == nil || !HasText(b.Kind) {
			return
		}
		if _, isCode := kind.(Code); isCode {
			b.Text = Plain(b.Text.PlainText())
		}
		// 折叠标题藏小节、其他折叠只藏子块：变换后不藏不露，直接去掉
		// 折叠。
		_, wasHeading := b.Kind.(Heading)
		_, isHeading := kind.(Heading)
		if wasHeading != isHeading {
			b.Folded = false
		}
		b.Kind = cloneKindShallow(kind)
		if !Nests(b.Kind) {
			kids := b.Children
			b.Children = nil
			c.insertAllAfter(id, kids)
		}
	}
}

// SetAlign 设置对齐。
func (c *Core) SetAlign(ids []BlockID, align Align) {
	c.Checkpoint(EditOther)
	for _, id := range ids {
		if b := c.BlockMut(id); b != nil {
			b.Align = align
		}
	}
}

// SetBlockBG 设置整块背景。
func (c *Core) SetBlockBG(ids []BlockID, bg *ColorName) {
	c.Checkpoint(EditOther)
	for _, id := range ids {
		if b := c.BlockMut(id); b != nil && TakesBG(b.Kind) {
			b.Bg = clonePtr(bg)
		}
	}
}

// ToggleTodo 切换待办勾选。
func (c *Core) ToggleTodo(id BlockID) {
	c.Checkpoint(EditOther)
	if b := c.BlockMut(id); b != nil {
		if td, ok := b.Kind.(Todo); ok {
			td.Done = !td.Done
			b.Kind = td
		}
	}
}

// ------------------------------------------------------------------ 折叠

// CanFold：标题总能折叠；带子块的段落、列表项和待办也能；表格和高亮块
// 里永不。
func (c *Core) CanFold(id BlockID) bool {
	p, ok := c.path(id)
	if !ok {
		return false
	}
	inCellOrCallout := false
	for _, s := range p {
		if s.Kind == SegCell {
			inCellOrCallout = true
		}
	}
	if !inCellOrCallout {
		for k := 1; k < len(p); k++ {
			if a := blockAt(c.Doc.Blocks, p[:k]); a != nil {
				if _, isCallout := a.Kind.(Callout); isCallout {
					inCellOrCallout = true
					break
				}
			}
		}
	}
	if inCellOrCallout {
		return false
	}
	b := blockAt(c.Doc.Blocks, p)
	if b == nil {
		return false
	}
	switch b.Kind.(type) {
	case Heading:
		return true
	case Text:
		return len(b.Children) > 0
	case Bullet, Ordered, Todo:
		return len(b.Children) > 0 || b.Folded
	}
	return false
}

// ToggleFold 折叠/展开（folded 属于文档，单次事务）。
func (c *Core) ToggleFold(id BlockID) {
	c.Generation++
	c.Dirty = true
	if b := c.BlockMut(id); b != nil {
		b.Folded = !b.Folded
	}
	c.fixSelectionAfterFold(id)
}

// FoldEnd 折叠标题藏住的最后一个块（没藏东西就是自己），让「其后」新
// 增的内容落在折叠小节下面。
func (c *Core) FoldEnd(id BlockID) BlockID {
	b := c.Block(id)
	if b == nil {
		return id
	}
	h, ok := b.Kind.(Heading)
	if !ok || !b.Folded {
		return id
	}
	p, ok := c.path(id)
	if !ok {
		return id
	}
	list, i, ok := siblingsAt(c.Doc.Blocks, p)
	if !ok {
		return id
	}
	// take_while：从 i+1 起直到遇到同级或更高级标题，取最后一个。
	last := id
	for j := i + 1; j < len(list); j++ {
		if h2, isHeading := list[j].Kind.(Heading); isHeading && h2.Level <= h.Level {
			break
		}
		last = list[j].ID
	}
	return last
}

// UnfoldTo 展开所有挡住 id 的折叠：折叠的祖先，和同列表里在它上面的
// 折叠标题。
func (c *Core) UnfoldTo(id BlockID) {
	c.Generation++
	// 折叠状态随文档保存，展开也是编辑。
	changed := false
	var goWalk func(list []Block, id BlockID, changed *bool)
	goWalk = func(list []Block, id BlockID, changed *bool) {
		idx := -1
		for i := range list {
			if _, ok := findPath(list[i:i+1], id); ok {
				idx = i
				break
			}
		}
		if idx < 0 {
			return
		}
		for {
			var hider struct {
				level uint8
				idx   int
			}
			found := false
			for j := 0; j < idx; j++ {
				if h, isHeading := list[j].Kind.(Heading); isHeading {
					if found && h.Level <= hider.level {
						found = false
					}
					if !found && list[j].Folded {
						hider.level, hider.idx, found = h.Level, j, true
					}
				}
			}
			if h, isHeading := list[idx].Kind.(Heading); found && isHeading && h.Level <= hider.level {
				found = false
			}
			if !found {
				break
			}
			list[hider.idx].Folded = false
			*changed = true
		}
		if list[idx].ID != id {
			b := &list[idx]
			if b.Folded {
				*changed = true
			}
			b.Folded = false
			goWalk(b.Children, id, changed)
			if tb, ok := b.Kind.(Table); ok {
				for r := range tb.Rows {
					for cc := range tb.Rows[r] {
						goWalk(tb.Rows[r][cc].Blocks, id, changed)
					}
				}
			}
		}
	}
	goWalk(c.Doc.Blocks, id, &changed)
	if changed {
		c.Dirty = true
	}
}

// IsHidden：折叠是否藏住了 id（折叠的祖先，或同一列表里在它或其祖先
// 之上、中间没有同级以上标题隔开的折叠标题）。
func (c *Core) IsHidden(id BlockID) bool {
	p, ok := c.path(id)
	if !ok {
		return false
	}
	for n := 1; n <= len(p); n++ {
		list, i, ok := siblingsAt(c.Doc.Blocks, p[:n])
		if !ok {
			return false
		}
		if n < len(p) && list[i].Folded {
			return true
		}
		stop := uint8(255)
		if h, isHeading := list[i].Kind.(Heading); isHeading {
			stop = h.Level
		}
		for j := i - 1; j >= 0; j-- {
			if h, isHeading := list[j].Kind.(Heading); isHeading && h.Level < stop {
				if list[j].Folded {
					return true
				}
				stop = h.Level
			}
		}
	}
	return false
}

// RevealCaret 展开挡住（块）选区两端的折叠。
func (c *Core) RevealCaret() {
	var ends []BlockID
	if bs := c.BlockSel; bs != nil {
		ends = []BlockID{bs.Anchor, bs.Head}
	} else {
		ends = []BlockID{c.Sel.Anchor.Block, c.Sel.Head.Block}
	}
	for _, id := range ends {
		if id != TitleID && c.IsHidden(id) {
			c.UnfoldTo(id)
		}
	}
}

func (c *Core) fixSelectionAfterFold(id BlockID) {
	us := c.Units()
	if bs := c.BlockSel; bs != nil {
		shown := map[BlockID]bool{}
		for _, b := range c.blockOrder(true) {
			shown[b] = true
		}
		if !shown[bs.Anchor] || !shown[bs.Head] {
			c.BlockSel = nil
			c.Sel = Caret(NewPos(id, c.LenOf(id)))
		}
	}
	contains := func(x BlockID) bool {
		for _, u := range us {
			if u.ID == x {
				return true
			}
		}
		return false
	}
	if !contains(c.Sel.Head.Block) || !contains(c.Sel.Anchor.Block) {
		// 光标被藏住了：挪到折叠块末尾。
		c.Sel = Caret(NewPos(id, c.LenOf(id)))
		c.FixSelection()
	}
}

// ------------------------------------------------------------------ 块插入/移动/分栏

// InsertBlock 在光标块后插入新块（空段落则顶替它），返回新块 id。
func (c *Core) InsertBlock(b Block) BlockID {
	c.Checkpoint(EditOther)
	id := b.ID
	at := c.Sel.Head.Block
	replace := false
	if at != TitleID {
		if x := c.Block(at); x != nil {
			_, isText := x.Kind.(Text)
			replace = isText && x.Text.IsEmpty() && len(x.Children) == 0
		}
	}
	if replace {
		p, ok := c.path(at)
		if !ok {
			return id
		}
		list, i, ok := containerMut(&c.Doc.Blocks, p)
		if !ok {
			return id
		}
		(*list)[i] = b
	} else {
		c.InsertAfter(at, b)
	}
	return id
}

// EnsureParagraphAfter 保证 id 后面有可编辑的块（插原子块后用）。
func (c *Core) EnsureParagraphAfter(id BlockID) BlockID {
	us := c.Units()
	i := c.unitIndex(us, id)
	if i >= 0 && i+1 < len(us) && us[i+1].Text && us[i+1].Scope == us[i].Scope {
		return us[i+1].ID
	}
	b := BlockText("")
	nid := b.ID
	c.InsertAfter(id, b)
	return nid
}

// InsertParagraphAfter 在 id 后插入空段（可撤销）。
func (c *Core) InsertParagraphAfter(id BlockID) BlockID {
	c.Checkpoint(EditOther)
	b := BlockText("")
	nid := b.ID
	c.InsertAfter(id, b)
	return nid
}

// DeleteBlock 删除块，光标落到后面的块上。
func (c *Core) DeleteBlock(id BlockID) {
	c.Checkpoint(EditOther)
	us := c.Units()
	i := c.unitIndex(us, id)
	if i < 0 {
		i = 0
	}
	c.removeSubtree(id)
	c.normalize()
	us2 := c.Units()
	at := min(max(i, 1), len(us2)-1)
	var target BlockID
	if at >= 0 && at < len(us2) {
		target = us2[at].ID
	} else if len(us2) > 0 {
		target = us2[len(us2)-1].ID
	}
	c.Sel = Caret(NewPos(target, 0))
}

// MoveBlock 拖放：把 id 移到 target 前/后。
func (c *Core) MoveBlock(id, target BlockID, after bool) {
	if id == target {
		return
	}
	// 不能把块拖进自己内部。
	p, ok1 := c.path(id)
	t, ok2 := c.path(target)
	if ok1 && ok2 {
		if len(t) >= len(p) && pathEqual(t[:len(p)], p) {
			return
		}
	}
	c.Checkpoint(EditOther)
	b := c.removeSubtree(id)
	if b == nil {
		return
	}
	if after {
		c.InsertAfter(target, *b)
	} else {
		c.insertBefore(target, *b)
	}
	c.normalize()
	c.FixSelection()
}

// MoveBlocks 拖放：把 ids（最外层、文档顺序）移到 target 旁或作其首
// 子块。没有变化时返回 false。
func (c *Core) MoveBlocks(ids []BlockID, target BlockID, place DropPlace) bool {
	tp, ok := c.path(target)
	if !ok {
		return false
	}
	for _, id := range ids {
		p, ok := c.path(id)
		if !ok || (len(tp) >= len(p) && pathEqual(tp[:len(p)], p)) {
			return false
		}
	}
	// 只有文本块和拖入能打开的容器能带子块。
	if place == DropFirstChild {
		k := c.KindOf(target)
		if k == nil {
			return false
		}
		nestsOrContainer := Nests(k)
		switch k.(type) {
		case Quote, Callout, Column:
			nestsOrContainer = true
		}
		if !nestsOrContainer {
			return false
		}
	}
	before := cloneBlockList(c.Doc.Blocks)
	var blocks []Block
	for _, id := range ids {
		if b := c.removeSubtree(id); b != nil {
			blocks = append(blocks, *b)
		}
	}
	var first BlockID
	if len(blocks) > 0 {
		first = blocks[0].ID
	}
	switch place {
	case DropBefore:
		for _, b := range blocks {
			c.insertBefore(target, b)
		}
	case DropAfter:
		c.insertAllAfter(target, blocks)
	case DropFirstChild:
		if t := c.BlockMut(target); t != nil {
			t.Children = append(blocks, t.Children...)
		}
	}
	if first == 0 || blocksEqual(c.Doc.Blocks, before) {
		c.Doc.Blocks = before
		return false
	}
	afterList := c.Doc.Blocks
	c.Doc.Blocks = before
	c.Checkpoint(EditOther)
	c.Doc.Blocks = afterList
	c.normalize()
	c.FixSelection()
	// 选区可能因此跨进/出表格。
	us := c.Units()
	scope := func(id BlockID) BlockID {
		for _, u := range us {
			if u.ID == id {
				return u.Scope
			}
		}
		return 0
	}
	if scope(c.Sel.Anchor.Block) != scope(c.Sel.Head.Block) {
		c.Sel = Caret(c.Sel.Head)
	}
	return true
}

// InsertBlocksAt 把新块（拖入的文件）放到 target 旁，一次编辑。
func (c *Core) InsertBlocksAt(blocks []Block, target BlockID, place DropPlace) {
	if len(blocks) == 0 {
		return
	}
	if _, ok := c.path(target); !ok {
		return
	}
	c.Checkpoint(EditOther)
	switch place {
	case DropBefore:
		for _, b := range blocks {
			c.insertBefore(target, b)
		}
	case DropAfter:
		c.insertAllAfter(target, blocks)
	case DropFirstChild:
		if t := c.BlockMut(target); t != nil {
			t.Children = append(blocks, t.Children...)
		}
	}
	c.normalize()
}

// 分栏上限与最小占比。
const (
	MaxColumns     = 10
	MinColumnRatio = 1.0 / 24.0
)

// ColumnRatios 返回 grid 各栏占比。
func (c *Core) ColumnRatios(grid BlockID) []float32 {
	g := c.Block(grid)
	if g == nil {
		return nil
	}
	out := make([]float32, 0, len(g.Children))
	for i := range g.Children {
		r := float32(0)
		if col, ok := g.Children[i].Kind.(Column); ok {
			r = col.Ratio
		}
		out = append(out, r)
	}
	return out
}

// AddColumn 加一栏：新栏拿 1/(n+1)，由之后仍宽于 1/24 的栏摊出。
func (c *Core) AddColumn(grid BlockID, at int) *BlockID {
	g := c.Block(grid)
	if g == nil {
		return nil
	}
	n := len(g.Children)
	if n >= MaxColumns {
		return nil
	}
	c.Checkpoint(EditOther)
	gm := c.BlockMut(grid)
	if gm == nil {
		return nil
	}
	for i := range gm.Children {
		if len(gm.Children[i].Children) == 0 {
			gm.Children[i].Children = append(gm.Children[i].Children, BlockText(""))
		}
	}
	m := float32(1) / float32(n+1)
	ratio := func(b *Block) float32 {
		if col, ok := b.Kind.(Column); ok {
			return col.Ratio
		}
		return 0
	}
	eligible := func(r float32) bool { return r*(1-m) > MinColumnRatio }
	var o float32
	for i := range gm.Children {
		if r := ratio(&gm.Children[i]); eligible(r) {
			o += r
		}
	}
	i := float32(0)
	if m < o {
		i = m / o
	}
	for ci := range gm.Children {
		if col, ok := gm.Children[ci].Kind.(Column); ok && eligible(col.Ratio) {
			col.Ratio *= 1 - i
			gm.Children[ci].Kind = col
		}
	}
	text := BlockText("")
	tid := text.ID
	col := NewBlock(Column{Ratio: m}, RichText{})
	col.Children = []Block{text}
	insertBlockAt(&gm.Children, min(at, n), col)
	c.Sel = Caret(NewPos(tid, 0))
	return &tid
}

// SetColumnRatios 提交一次栏宽调整。
func (c *Core) SetColumnRatios(grid BlockID, ratios []float32) {
	if cur := c.ColumnRatios(grid); floatSlicesEqual(cur, ratios) {
		return
	}
	c.Checkpoint(EditOther)
	if g := c.BlockMut(grid); g != nil {
		for i := range g.Children {
			if i >= len(ratios) {
				break
			}
			if col, ok := g.Children[i].Kind.(Column); ok {
				col.Ratio = ratios[i]
				g.Children[i].Kind = col
			}
		}
	}
}

// FocusColumnEnd 点击栏内容下方时光标进其末尾空段（没有则补一个）。
func (c *Core) FocusColumnEnd(col BlockID) {
	cb := c.Block(col)
	if cb == nil {
		return
	}
	reuse := BlockID(0)
	if n := len(cb.Children); n > 0 {
		last := &cb.Children[n-1]
		if _, isText := last.Kind.(Text); isText && last.Text.IsEmpty() {
			reuse = last.ID
		}
	}
	id := reuse
	if id == 0 {
		c.Checkpoint(EditOther)
		b := BlockText("")
		id = b.ID
		if cm := c.BlockMut(col); cm != nil {
			cm.Children = append(cm.Children, b)
		}
	}
	c.Sel = Caret(NewPos(id, 0))
}

// ------------------------------------------------------------------ 行内样式

// ToggleMark 切换选区的样式；光标上则记入 PendingMarks。
func (c *Core) ToggleMark(get func(Marks) bool, set func(*Marks, bool)) {
	if c.Sel.IsCollapsed() {
		p := c.Sel.Head
		m := Marks{}
		if c.PendingMarks != nil {
			m = *c.PendingMarks
		} else if t := c.TextOf(p.Block); t != nil {
			m = t.MarksAt(p.Offset)
		}
		set(&m, !get(m))
		c.PendingMarks = &m
		return
	}
	us := c.Units()
	type target struct {
		id          BlockID
		start, ends int
	}
	var targets []target
	for _, u := range us {
		if !u.Text || u.ID == TitleID || c.IsCode(u.ID) {
			continue
		}
		if s, e, ok := c.RangeIn(u.ID, us); ok && s < e {
			targets = append(targets, target{u.ID, s, e})
		}
	}
	if len(targets) == 0 {
		return
	}
	all := true
	for _, tg := range targets {
		if t := c.TextOf(tg.id); t != nil {
			if !t.AllMarks(tg.start, tg.ends, get) {
				all = false
				break
			}
		}
	}
	c.Checkpoint(EditOther)
	for _, tg := range targets {
		if t := c.textMut(tg.id); t != nil {
			t.UpdateMarks(tg.start, tg.ends, func(m *Marks) { set(m, !all) })
		}
	}
}

// ApplyMarks 对选区应用样式修改（颜色、链接）。
func (c *Core) ApplyMarks(f func(*Marks)) {
	us := c.Units()
	type target struct {
		id          BlockID
		start, ends int
	}
	var targets []target
	for _, u := range us {
		if !u.Text || u.ID == TitleID || c.IsCode(u.ID) {
			continue
		}
		if s, e, ok := c.RangeIn(u.ID, us); ok && s < e {
			targets = append(targets, target{u.ID, s, e})
		}
	}
	if len(targets) == 0 {
		return
	}
	c.Checkpoint(EditOther)
	for _, tg := range targets {
		if t := c.textMut(tg.id); t != nil {
			t.UpdateMarks(tg.start, tg.ends, f)
		}
	}
}

// ApplyMarksToBlocks 对整块应用样式（块菜单 → 颜色）。
func (c *Core) ApplyMarksToBlocks(ids []BlockID, f func(*Marks)) {
	c.Checkpoint(EditOther)
	for _, id := range ids {
		if t := c.textMut(id); t != nil {
			length := t.Len()
			t.UpdateMarks(0, length, f)
		}
	}
}

// ------------------------------------------------------------------ 块选择

// InBlockMode 是否处于块选择模式。
func (c *Core) InBlockMode() bool { return c.BlockSel != nil }

// ClearBlockSel 退出块选择模式。
func (c *Core) ClearBlockSel() { c.BlockSel = nil }

// SelectBlock 进入单块的块选择模式（Esc、手柄点击）。
func (c *Core) SelectBlock(id BlockID) {
	if id == TitleID || c.Block(id) == nil {
		return
	}
	c.BlockSel = &BlockSel{Anchor: id, Head: id}
	c.CellSel = nil
}

func (c *Core) selectRange(anchor, head BlockID) {
	c.BlockSel = &BlockSel{Anchor: anchor, Head: head}
	c.CellSel = nil
}

// blockOrder 按文档顺序列出块 id。visible 时跳过折叠藏住的；grid 的栏
// 只是布局、永不列出。
func (c *Core) blockOrder(visible bool) []BlockID {
	var out []BlockID
	var walk func(list []Block, visible bool)
	walk = func(list []Block, visible bool) {
		var hideBelow uint8
		hiding := false
		for i := range list {
			b := &list[i]
			if visible && hiding {
				if h, isHeading := b.Kind.(Heading); isHeading && h.Level <= hideBelow {
					hiding = false
				} else {
					continue
				}
			}
			if _, isColumn := b.Kind.(Column); !isColumn {
				out = append(out, b.ID)
			}
			if h, isHeading := b.Kind.(Heading); isHeading && b.Folded {
				hideBelow, hiding = h.Level, true
			}
			if !(visible && b.Folded) {
				walk(b.Children, visible)
			}
			if tb, ok := b.Kind.(Table); ok {
				for _, row := range tb.Rows {
					for _, cell := range row {
						walk(cell.Blocks, visible)
					}
				}
			}
		}
	}
	walk(c.Doc.Blocks, visible)
	return out
}

// CellStep 表格里的 Tab / Shift+Tab：光标到下一/上一单元格末尾；最后一
// 格的 Tab 加一行。表格外返回 false。
func (c *Core) CellStep(forward bool) bool {
	h := c.Sel.Head
	p, ok := c.path(h.Block)
	if !ok {
		return false
	}
	cellAt := -1
	for i := len(p) - 1; i >= 0; i-- {
		if p[i].Kind == SegCell {
			cellAt = i
			break
		}
	}
	if cellAt < 0 {
		return false
	}
	r, col := p[cellAt].Row, p[cellAt].Col
	tpath := append(Path{}, p[:cellAt]...)
	tb := blockAt(c.Doc.Blocks, tpath)
	table, isTable := tb.Kind.(Table)
	if !isTable {
		return false
	}
	var cells [][2]int
	for _, cr := range table.Cells() {
		cells = append(cells, [2]int{cr.Row, cr.Col})
	}
	k := -1
	for i, x := range cells {
		if x[0] == r && x[1] == col {
			k = i
			break
		}
	}
	if k < 0 {
		return false
	}
	var target [2]int
	if forward && k+1 < len(cells) {
		target = cells[k+1]
	} else if !forward && k > 0 {
		target = cells[k-1]
	} else if forward {
		c.Checkpoint(EditOther)
		tm := blockAt(c.Doc.Blocks, tpath)
		t2, ok := tm.Kind.(Table)
		if !ok {
			return false
		}
		n := len(t2.Rows)
		t2.InsertRow(n)
		tm.Kind = t2
		target = [2]int{n, 0}
	} else {
		return true
	}
	tb = blockAt(c.Doc.Blocks, tpath)
	table, isTable = tb.Kind.(Table)
	if !isTable {
		return false
	}
	// 单元格的最后一个停靠点：末块可能是容器或被折叠藏住。
	ids := make([]BlockID, 0, len(table.Rows[target[0]][target[1]].Blocks))
	for i := range table.Rows[target[0]][target[1]].Blocks {
		ids = append(ids, table.Rows[target[0]][target[1]].Blocks[i].ID)
	}
	inside := insideOf(pathIndex(c.Doc.Blocks), ids)
	var last BlockID
	allUnits := units(&c.Doc)
	for i := len(allUnits) - 1; i >= 0; i-- {
		if inside(allUnits[i].ID) {
			last = allUnits[i].ID
			break
		}
	}
	if last == 0 {
		return true
	}
	c.BreakCoalescing()
	c.Sel = Caret(NewPos(last, c.LenOf(last)))
	return true
}

func (c *Core) tableScope(id BlockID) BlockID {
	p, ok := c.path(id)
	if !ok {
		return 0
	}
	return c.tableScopeAt(p)
}

func (c *Core) tableScopeAt(p Path) BlockID {
	at := -1
	for i := len(p) - 1; i >= 0; i-- {
		if p[i].Kind == SegCell {
			at = i
			break
		}
	}
	if at < 0 {
		return 0
	}
	if b := blockAt(c.Doc.Blocks, p[:at]); b != nil {
		return b.ID
	}
	return 0
}

// enclosingTableIn：装着 id 且本身在 scope 编辑范围里的那张表。
func (c *Core) enclosingTableIn(id BlockID, scope BlockID) BlockID {
	t := c.tableScope(id)
	for t != 0 {
		outer := c.tableScope(t)
		if outer == scope {
			return t
		}
		t = outer
	}
	return 0
}

// topmostIn 去掉嵌在另一个选中块里的块。
func (c *Core) topmostIn(ids []BlockID, paths map[BlockID]Path) []BlockID {
	set := make(map[string]int) // pathKey → index in ids
	for _, id := range ids {
		if p, ok := paths[id]; ok {
			set[pathKey(p)] = 1
		}
	}
	var out []BlockID
	for _, id := range ids {
		p, ok := paths[id]
		if !ok {
			continue
		}
		covered := false
		for n := 1; n < len(p); n++ {
			if set[pathKey(p[:n])] == 1 {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, id)
		}
	}
	return out
}

func pathKey(p Path) string {
	var b strings.Builder
	for _, s := range p {
		switch s.Kind {
		case SegChild:
			b.WriteByte('c')
			b.WriteString(itoa(s.Index))
		case SegCell:
			b.WriteByte('t')
			b.WriteString(itoa(s.Row) + "," + itoa(s.Col) + "," + itoa(s.Index))
		}
		b.WriteByte(';')
	}
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	if neg {
		return "-" + string(d)
	}
	return string(d)
}

// BlockSelIDs 选中的块（文档顺序、只取最外层）。跨过折叠标题也带上它
// 藏住的块。
func (c *Core) BlockSelIDs() []BlockID {
	bs := c.BlockSel
	if bs == nil {
		return nil
	}
	vis := c.blockOrder(true)
	a, h := -1, -1
	for i, x := range vis {
		if x == bs.Anchor {
			a = i
		}
		if x == bs.Head {
			h = i
		}
	}
	if a < 0 || h < 0 {
		return nil
	}
	s, e := a, h
	if s > e {
		s, e = e, s
	}
	all := c.blockOrder(false)
	first := 0
	for i, x := range all {
		if x == vis[s] {
			first = i
			break
		}
	}
	nextVis := len(all)
	if e+1 < len(vis) {
		for i, x := range all {
			if x == vis[e+1] {
				nextVis = i
				break
			}
		}
	}
	paths := pathIndex(c.Doc.Blocks)
	lastPath := paths[vis[e]]
	end := 0
	for i, x := range all {
		if x == vis[e] {
			end = i
			break
		}
	}
	for end+1 < nextVis {
		// 只吸收被藏住的块；出了最后一个块的容器就停（如离开单元格）。
		p, ok := paths[all[end+1]]
		if !ok || !covers(lastPath[:len(lastPath)-1], p) {
			break
		}
		end++
	}
	return c.topmostIn(all[first:end+1], paths)
}

// covers：outer 路径是否就是 inner 或它的祖先。
func covers(outer, inner Path) bool {
	return len(outer) <= len(inner) && pathEqual(inner[:len(outer)], outer)
}

// TrailingLine 最下面的空行（没有就补一段）：块模式下 ↓ 到底离开最后的
// 代码块或表格的唯一途径。
func (c *Core) TrailingLine() BlockID {
	if n := len(c.Doc.Blocks); n > 0 {
		last := &c.Doc.Blocks[n-1]
		if _, isText := last.Kind.(Text); isText && last.Text.IsEmpty() && len(last.Children) == 0 {
			return last.ID
		}
	}
	c.Checkpoint(EditOther)
	b := BlockText("")
	id := b.ID
	c.Doc.Blocks = append(c.Doc.Blocks, b)
	return id
}

// BlockSelMove 块模式的 ↑/↓。extend 是 Shift。
func (c *Core) BlockSelMove(down, extend bool) {
	bs := c.BlockSel
	if bs == nil {
		return
	}
	scope := c.tableScope(bs.Head)
	paths := pathIndex(c.Doc.Blocks)
	var vis []BlockID
	for _, id := range c.blockOrder(true) {
		if p, ok := paths[id]; ok && c.tableScopeAt(p) == scope {
			vis = append(vis, id)
		}
	}
	i := -1
	for j, x := range vis {
		if x == bs.Head {
			i = j
			break
		}
	}
	if i < 0 {
		return
	}
	// 移动先收敛到当前范围的边缘。
	ids := c.BlockSelIDs()
	var next BlockID
	if down {
		last := bs.Head
		if !extend && len(ids) > 0 {
			last = ids[len(ids)-1]
		}
		lp := paths[last]
		li := i
		for j, x := range vis {
			if x == last {
				li = j
				break
			}
		}
		for j := li + 1; j < len(vis); j++ {
			p, ok := paths[vis[j]]
			if !ok {
				continue
			}
			if extend || !covers(lp, p) {
				next = vis[j]
				break
			}
		}
	} else {
		first := bs.Head
		if !extend && len(ids) > 0 {
			first = ids[0]
		}
		fi := i
		for j, x := range vis {
			if x == first {
				fi = j
				break
			}
		}
		if fi > 0 {
			next = vis[fi-1]
		}
	}
	switch {
	case next != 0 && extend:
		c.selectRange(bs.Anchor, next)
	case next != 0:
		c.SelectBlock(next)
	case down && !extend && scope == 0:
		id := c.TrailingLine()
		c.BlockSel = nil
		c.Sel = Caret(NewPos(id, 0))
	}
}

// BlockSelHorizontal ← 选父块（或折叠）；→ 选第一个子块（或展开）。
func (c *Core) BlockSelHorizontal(right bool) {
	bs := c.BlockSel
	if bs == nil {
		return
	}
	id := bs.Head
	b := c.Block(id)
	if b == nil {
		return
	}
	foldable := c.CanFold(id)
	if right {
		if b.Folded {
			c.ToggleFold(id)
		} else {
			var child *Block
			for i := range b.Children {
				if _, isColumn := b.Children[i].Kind.(Column); !isColumn {
					child = &b.Children[i]
					break
				}
			}
			if child == nil && len(b.Children) > 0 {
				if col := &b.Children[0]; len(col.Children) > 0 {
					child = &col.Children[0]
				}
			}
			if child != nil {
				c.SelectBlock(child.ID)
			}
		}
	} else if foldable && !b.Folded && HasText(b.Kind) {
		c.ToggleFold(id)
		c.SelectBlock(id)
	} else {
		p := c.ParentOf(id)
		isColumn := false
		if p != nil {
			_, isColumn = p.Kind.(Column)
		}
		// 栏不可选；去 grid。
		if isColumn {
			p = c.ParentOf(p.ID)
			isColumn = false
		}
		if p != nil {
			c.SelectBlock(p.ID)
		}
	}
}

// BlockSelToEdge ⌘⇧↑/⌘⇧↓：从当前顶层块（所在单元格/高亮块/栏/引用内）
// 选到开头/结尾。
func (c *Core) BlockSelToEdge(down bool) {
	cur := c.Sel.Head.Block
	if c.BlockSel != nil {
		cur = c.BlockSel.Head
	}
	if cur == TitleID {
		if len(c.Doc.Blocks) > 0 {
			c.selectRange(c.Doc.Blocks[0].ID, c.Doc.Blocks[len(c.Doc.Blocks)-1].ID)
		}
		return
	}
	p, ok := c.path(cur)
	if !ok {
		return
	}
	// 容器边界：最深的单元格/高亮块/栏/引用祖先。
	base := 0
	for n := len(p) - 1; n >= 1; n-- {
		if p[n].Kind == SegCell {
			base = n
			break
		}
		anc := blockAt(c.Doc.Blocks, p[:n])
		if anc == nil {
			break
		}
		switch anc.Kind.(type) {
		case Callout, Column, Quote:
			base = n
		}
		if base == n {
			break
		}
	}
	topBlock := blockAt(c.Doc.Blocks, p[:base+1])
	if topBlock == nil {
		return
	}
	top := topBlock.ID
	var siblings []BlockID
	if pp, has := parentPath(p[:base+1]); !has {
		for i := range c.Doc.Blocks {
			siblings = append(siblings, c.Doc.Blocks[i].ID)
		}
	} else {
		parent := blockAt(c.Doc.Blocks, pp)
		if parent == nil {
			return
		}
		if p[base].Kind == SegCell {
			if tb, isTable := parent.Kind.(Table); isTable {
				for i := range tb.Rows[p[base].Row][p[base].Col].Blocks {
					siblings = append(siblings, tb.Rows[p[base].Row][p[base].Col].Blocks[i].ID)
				}
			}
		} else {
			for i := range parent.Children {
				siblings = append(siblings, parent.Children[i].ID)
			}
		}
	}
	if len(siblings) == 0 {
		return
	}
	edge := siblings[0]
	if down {
		edge = siblings[len(siblings)-1]
	}
	c.selectRange(top, edge)
}

// ExtendIntoBlocks 块首 Shift+↑ / 块尾 Shift+↓：从文本选区切到块选择。
func (c *Core) ExtendIntoBlocks(down bool) {
	id := c.Sel.Head.Block
	if id == TitleID {
		return
	}
	c.SelectBlock(id)
	c.BlockSelMove(down, true)
}

// DeleteSelectedBlocks 块模式的 Backspace/Delete。
func (c *Core) DeleteSelectedBlocks() {
	ids := c.BlockSelIDs()
	if len(ids) == 0 {
		return
	}
	c.Checkpoint(EditOther)
	c.BreakCoalescing()
	us := c.Units()
	inside := insideOf(pathIndex(c.Doc.Blocks), ids)
	firstUnit := -1
	for i := range us {
		if inside(us[i].ID) {
			firstUnit = i
			break
		}
	}
	var prev *Unit
	if firstUnit >= 0 {
		for i := firstUnit - 1; i >= 0; i-- {
			if us[i].ID == TitleID || !inside(us[i].ID) {
				u := us[i]
				prev = &u
				break
			}
		}
	}
	for i := len(ids) - 1; i >= 0; i-- {
		c.removeSubtree(ids[i])
	}
	c.normalize()
	c.BlockSel = nil
	switch {
	case prev == nil:
		if len(c.Doc.Blocks) > 0 {
			c.caretToStart(c.Doc.Blocks[0].ID)
		}
	case prev.ID == TitleID:
		if len(c.Doc.Blocks) > 0 {
			c.caretToStart(c.Doc.Blocks[0].ID)
		}
	case prev.Text:
		c.Sel = Caret(NewPos(prev.ID, c.LenOf(prev.ID)))
	default:
		c.Sel = Caret(NewPos(prev.ID, 0))
		c.SelectBlock(prev.ID)
	}
	c.FixSelection()
}

func (c *Core) caretToStart(id BlockID) {
	us := c.Units()
	inside := insideOf(pathIndex(c.Doc.Blocks), []BlockID{id})
	target := id
	for _, u := range us {
		if inside(u.ID) {
			target = u.ID
			break
		}
	}
	c.Sel = Caret(NewPos(target, 0))
}

// EnterFromBlockSel 块模式回车：光标到最后一个选中文本块末尾，或在选
// 区后补一段。
func (c *Core) EnterFromBlockSel() {
	ids := c.BlockSelIDs()
	if len(ids) == 0 {
		return
	}
	last := ids[len(ids)-1]
	c.BlockSel = nil
	us := c.Units()
	inside := insideOf(pathIndex(c.Doc.Blocks), ids)
	var target *Unit
	for i := len(us) - 1; i >= 0; i-- {
		if us[i].Text && inside(us[i].ID) {
			u := us[i]
			target = &u
			break
		}
	}
	if target != nil {
		c.Sel = Caret(NewPos(target.ID, c.LenOf(target.ID)))
	} else {
		c.Checkpoint(EditOther)
		b := BlockText("")
		id := b.ID
		c.InsertAfter(last, b)
		c.Sel = Caret(NewPos(id, 0))
	}
}

// SelectAll ⌘A：块文本 → 所在单元格/表格/高亮块 → 整篇文档。
func (c *Core) SelectAll() {
	if c.BlockSel == nil {
		s, e := c.Ordered()
		if s.Block == e.Block && c.TextOf(s.Block) != nil {
			length := c.LenOf(s.Block)
			if length > 0 && !(s.Offset == 0 && e.Offset == length) {
				c.Sel = Selection{Anchor: NewPos(s.Block, 0), Head: NewPos(s.Block, length)}
				return
			}
		}
	}
	var current []BlockID
	if c.BlockSel != nil {
		current = c.BlockSelIDs()
	} else {
		s, e := c.Ordered()
		switch {
		case s.Block == TitleID:
			current = nil
		case s.Block == e.Block:
			current = []BlockID{s.Block}
		default:
			current = c.SelectedBlocks()
		}
	}
	paths := pathIndex(c.Doc.Blocks)
	var chains [][]stop
	for _, id := range current {
		if p, ok := paths[id]; ok {
			chains = append(chains, c.stopChain(p))
		}
	}
	var candidates []stop
	if len(chains) > 0 {
		for _, s := range chains[0] {
			inAll := true
			for _, o := range chains {
				if !containsStop(o, s) {
					inAll = false
					break
				}
			}
			if inAll {
				candidates = append(candidates, s)
			}
		}
	} else {
		candidates = []stop{{kind: stopRoot}}
	}
	for _, s := range candidates {
		content := c.stopContent(s)
		if stopIDsEqual(content, current) || len(content) == 0 {
			continue
		}
		// 止于最后一个可见块时也要带上折叠标题之后藏住的。
		vis := map[BlockID]bool{}
		for _, b := range c.blockOrder(true) {
			vis[b] = true
		}
		var shown []BlockID
		for _, id := range content {
			if vis[id] {
				shown = append(shown, id)
			}
		}
		if len(shown) > 0 {
			last := shown[len(shown)-1]
			c.selectRange(shown[0], last)
			return
		}
	}
}

// stop：SelectAll 的停靠点。
type stopKind uint8

const (
	stopCell stopKind = iota
	stopBlock
	stopRoot
)

type stop struct {
	kind  stopKind
	block BlockID
	r, c  int
}

func containsStop(list []stop, s stop) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func stopIDsEqual(a, b []BlockID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// stopChain 从 path 处的块向外：表格、高亮块、单元格、再到根。grid 的
// 栏、引用直接穿过。
func (c *Core) stopChain(path Path) []stop {
	var out []stop
	for n := len(path); n >= 1; n-- {
		b := blockAt(c.Doc.Blocks, path[:n])
		if b == nil {
			continue
		}
		switch b.Kind.(type) {
		case Table, Callout:
			out = append(out, stop{kind: stopBlock, block: b.ID})
		}
		if n >= 2 && path[n-1].Kind == SegCell {
			t := blockAt(c.Doc.Blocks, path[:n-1])
			if t != nil {
				out = append(out, stop{kind: stopCell, block: t.ID, r: path[n-1].Row, c: path[n-1].Col})
			}
		}
	}
	out = append(out, stop{kind: stopRoot})
	return out
}

func (c *Core) stopContent(s stop) []BlockID {
	switch s.kind {
	case stopRoot:
		out := make([]BlockID, 0, len(c.Doc.Blocks))
		for i := range c.Doc.Blocks {
			out = append(out, c.Doc.Blocks[i].ID)
		}
		return out
	case stopBlock:
		return []BlockID{s.block}
	default: // 单元格
		b := c.Block(s.block)
		if b == nil {
			return nil
		}
		if tb, ok := b.Kind.(Table); ok && s.r < len(tb.Rows) && s.c < len(tb.Rows[s.r]) {
			out := make([]BlockID, 0, len(tb.Rows[s.r][s.c].Blocks))
			for i := range tb.Rows[s.r][s.c].Blocks {
				out = append(out, tb.Rows[s.r][s.c].Blocks[i].ID)
			}
			return out
		}
		return nil
	}
}

// ------------------------------------------------------------------ 辅助

func insertBlockAt(list *[]Block, i int, b Block) {
	*list = append(*list, Block{})
	copy((*list)[i+1:], (*list)[i:])
	(*list)[i] = b
}

func insertBlocksAt(list *[]Block, i int, bs []Block) {
	*list = append(*list, make([]Block, len(bs))...)
	copy((*list)[i+len(bs):], (*list)[i:])
	copy((*list)[i:], bs)
}

func removeBlockAt(list *[]Block, i int) Block {
	b := (*list)[i]
	*list = append((*list)[:i], (*list)[i+1:]...)
	return b
}

func retainBlocks(list *[]Block, keep func(*Block) bool) {
	out := (*list)[:0]
	for i := range *list {
		if keep(&(*list)[i]) {
			out = append(out, (*list)[i])
		}
	}
	*list = out
}

func cloneBlockList(list []Block) []Block {
	if list == nil {
		return nil
	}
	out := make([]Block, len(list))
	for i := range list {
		out[i] = cloneBlock(list[i], false)
	}
	return out
}

func blocksEqual(a, b []Block) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !blockEqual(&a[i], &b[i]) {
			return false
		}
	}
	return true
}

func blockEqual(a, b *Block) bool {
	return a.ID == b.ID && a.Align == b.Align && a.Folded == b.Folded &&
		colorPtrEq(a.Bg, b.Bg) && a.Text.Text == b.Text.Text && runsEqual(a.Text.Runs, b.Text.Runs) &&
		kindEqual(a.Kind, b.Kind) && blocksEqual(a.Children, b.Children)
}

func colorPtrEq(a, b *ColorName) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func strPtrEq(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func runsEqual(a, b []Run) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Len != b[i].Len || !a[i].Marks.Equal(b[i].Marks) {
			return false
		}
	}
	return true
}

func kindEqual(a, b BlockKind) bool {
	if kindTag(a) != kindTag(b) {
		return false
	}
	switch ka := a.(type) {
	case Heading:
		kb := b.(Heading)
		return ka.Level == kb.Level && ka.SeqAuto == kb.SeqAuto
	case Ordered:
		kb := b.(Ordered)
		return seqPtrEq(ka.Seq, kb.Seq)
	case Todo:
		return a.(Todo) == b.(Todo)
	case Code:
		kb := b.(Code)
		return ka.Lang == kb.Lang && ka.Wrap == kb.Wrap && strPtrEq(ka.Caption, kb.Caption)
	case Callout:
		kb := b.(Callout)
		return ka.Emoji == kb.Emoji && colorPtrEq(ka.Bg, kb.Bg) && colorPtrEq(ka.Border, kb.Border) && colorPtrEq(ka.TextColor, kb.TextColor)
	case Image:
		kb := b.(Image)
		return ka.SrcKind == kb.SrcKind && ka.Src == kb.Src &&
			f32PtrEq(ka.Width, kb.Width) && f32PtrEq(ka.Height, kb.Height) &&
			strPtrEq(ka.Caption, kb.Caption) && cropPtrEq(ka.Crop, kb.Crop)
	case Table:
		kb := b.(Table)
		return tableEqual(&ka, &kb)
	case Column:
		return ka.Ratio == b.(Column).Ratio
	case Countdown:
		return ka == b.(Countdown)
	case FileBlock:
		return ka == b.(FileBlock)
	case Raw:
		kb := b.(Raw)
		return ka.Tag == kb.Tag && ka.XML == kb.XML
	}
	return true // Text、Quote、Divider、Grid、SubPageList 等纯值变体
}

func seqPtrEq(a, b *uint32) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func f32PtrEq(a, b *float32) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func cropPtrEq(a, b *[4]float32) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func tableEqual(a, b *Table) bool {
	if a.HeaderRows != b.HeaderRows || a.HeaderCol != b.HeaderCol || len(a.ColWidths) != len(b.ColWidths) {
		return false
	}
	for i := range a.ColWidths {
		if a.ColWidths[i] != b.ColWidths[i] {
			return false
		}
	}
	if len(a.Rows) != len(b.Rows) {
		return false
	}
	for r := range a.Rows {
		if len(a.Rows[r]) != len(b.Rows[r]) {
			return false
		}
		for cc := range a.Rows[r] {
			x, y := &a.Rows[r][cc], &b.Rows[r][cc]
			if x.Colspan != y.Colspan || x.Rowspan != y.Rowspan || !colorPtrEq(x.Bg, y.Bg) || !strPtrEq(x.Valign, y.Valign) || !blocksEqual(x.Blocks, y.Blocks) {
				return false
			}
		}
	}
	return true
}

func floatSlicesEqual(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func docBytes(d *Document) int {
	n := len(d.Title.Text)
	d.ForEachBlock(func(b *Block) {
		n += blockApproxSize + len(b.Text.Text) + len(b.Text.Runs)*runApproxSize
		if raw, ok := b.Kind.(Raw); ok {
			n += len(raw.XML)
		}
	})
	return n
}

const blockApproxSize = 128 // 与 Rust size_of::<Block>() 同用途的粗略估计
const runApproxSize = 96

// resetForSplit 是块从中间切开时后半段的类型。
func resetForSplit(k BlockKind) BlockKind {
	switch k.(type) {
	case Ordered:
		return Ordered{}
	case Todo:
		return Todo{}
	}
	return k
}

// OrderedNumber 计算有序项在兄弟列表 index 处的编号。
func orderedNumber(siblings []Block, index int) uint32 {
	var n uint32
	for i := index; i >= 0; i-- {
		o, isOrdered := siblings[i].Kind.(Ordered)
		if !isOrdered {
			break
		}
		if o.Seq != nil {
			s := *o.Seq
			if s == ^uint32(0) {
				return s
			}
			return s + n
		}
		n++
	}
	return n
}

// HeadingOrders 自动编号标题（seq="auto"）的编号：外层 "1."、下层
// "1.2.3"。每个容器（页面、高亮块、栏、单元格）各自计数，深度取它用
// 到的标题层级，所以 h2+h4 编号为 1. / 1.1。
func HeadingOrders(blocks []Block) map[BlockID]string {
	out := make(map[BlockID]string)
	type headingRef struct {
		id    BlockID
		level uint8
	}
	var gather func(list []Block, run *[]headingRef, cont *[][]Block)
	gather = func(list []Block, run *[]headingRef, cont *[][]Block) {
		for i := range list {
			b := &list[i]
			switch k := b.Kind.(type) {
			case Heading:
				if k.SeqAuto {
					*run = append(*run, headingRef{b.ID, k.Level})
				}
			case Callout, Column:
				*cont = append(*cont, b.Children)
				continue // 子块作为独立容器单独计数
			case Table:
				for _, row := range k.Rows {
					for _, cell := range row {
						*cont = append(*cont, cell.Blocks)
					}
				}
			}
			gather(b.Children, run, cont)
		}
	}
	stack := [][]Block{blocks}
	for len(stack) > 0 {
		list := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		var run []headingRef
		gather(list, &run, &stack)
		var levels []uint8
		for _, r := range run {
			levels = append(levels, r.level)
		}
		sortU8(levels)
		levels = dedupeU8(levels)
		var o []uint32
		for _, r := range run {
			d := 1
			for i, l := range levels {
				if l == r.level {
					d = i + 1
					break
				}
			}
			if len(o) < d {
				// 展开到 d 位，新位为 1（不递增）。
				o = append(o, make([]uint32, d-len(o))...)
				for i := range o {
					if o[i] == 0 {
						o[i] = 1
					}
				}
			} else {
				o = o[:d]
				o[d-1]++
			}
			var s string
			if d == 1 {
				s = u32str(o[0]) + "."
			} else {
				parts := make([]string, len(o))
				for i, v := range o {
					parts[i] = u32str(v)
				}
				s = strings.Join(parts, ".")
			}
			out[r.id] = s
		}
	}
	return out
}

func sortU8(v []uint8) {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j] < v[j-1]; j-- {
			v[j], v[j-1] = v[j-1], v[j]
		}
	}
}

func dedupeU8(v []uint8) []uint8 {
	if len(v) == 0 {
		return v
	}
	out := v[:1]
	for _, x := range v[1:] {
		if x != out[len(out)-1] {
			out = append(out, x)
		}
	}
	return out
}

func u32str(v uint32) string {
	return itoa(int(v))
}

// OrderedMarker 是嵌套 depth 层的有序项记号（1. → a. → i.）。
func OrderedMarker(n uint32, depth int) string {
	switch depth % 3 {
	case 0:
		return u32str(n) + "."
	case 1:
		return alphaNum(n) + "."
	default:
		return romanNum(n) + "."
	}
}

func alphaNum(n uint32) string {
	var s []byte
	for n > 0 {
		n--
		s = append(s, byte('a'+n%26))
		n /= 26
	}
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
	return string(s)
}

func romanNum(n uint32) string {
	table := []struct {
		v uint32
		r string
	}{
		{1000, "m"}, {900, "cm"}, {500, "d"}, {400, "cd"}, {100, "c"}, {90, "xc"},
		{50, "l"}, {40, "xl"}, {10, "x"}, {9, "ix"}, {5, "v"}, {4, "iv"}, {1, "i"},
	}
	var s strings.Builder
	for _, t := range table {
		for n >= t.v {
			s.WriteString(t.r)
			n -= t.v
		}
	}
	return s.String()
}

// dropEmptyColumns：最后一块没了的栏消失，其余按占比分掉它的宽度。
func dropEmptyColumns(cols *[]Block) {
	ratio := func(b *Block) float32 {
		if col, ok := b.Kind.(Column); ok {
			return col.Ratio
		}
		return 0
	}
	var total, gone float32
	for i := range *cols {
		r := ratio(&(*cols)[i])
		total += r
		if len((*cols)[i].Children) == 0 {
			gone += r
		}
	}
	allKept := true
	for i := range *cols {
		if len((*cols)[i].Children) == 0 {
			allKept = false
			break
		}
	}
	if allKept {
		return
	}
	retainBlocks(cols, func(b *Block) bool { return len(b.Children) > 0 })
	left := total - gone
	for i := range *cols {
		if col, ok := (*cols)[i].Kind.(Column); ok && left > 0 {
			col.Ratio /= left
			(*cols)[i].Kind = col
		}
	}
}

// pruneAfter 把 b 子树里选区结束之后的块剪掉。
func pruneAfter(b *Block, end Pos, pos map[BlockID]int) {
	ei, ok := pos[end.Block]
	if !ok {
		ei = int(^uint(0) >> 1)
	}
	var walk func(list *[]Block, ei int, end Pos, pos map[BlockID]int)
	walk = func(list *[]Block, ei int, end Pos, pos map[BlockID]int) {
		retainBlocks(list, func(c *Block) bool {
			i, ok := pos[c.ID]
			return !ok || i <= ei
		})
		for i := range *list {
			c := &(*list)[i]
			if c.ID == end.Block {
				length := c.Text.Len()
				to := min(end.Offset, length)
				c.Text = c.Text.Slice(0, to)
			}
			walk(&c.Children, ei, end, pos)
		}
	}
	walk(&b.Children, ei, end, pos)
}
