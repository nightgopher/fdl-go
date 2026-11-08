package fdl

// Markdown 式快捷键与粘贴（对拍 editor/core.rs 的对应部分）。块级触
// 发、行内规则、autolink、分隔线、粘贴为块/文本/Markdown。

import (
	"strings"
	"unicode"
)

// markdownEnabled：哪些块里跑实时 Markdown 转换（引用里的段落就是纯文
// 本块）。
func (c *Core) markdownEnabled(id BlockID) bool {
	if id == TitleID {
		return false
	}
	switch c.KindOf(id).(type) {
	case Text, Heading, Bullet, Ordered, Todo:
		return true
	}
	return false
}

func (c *Core) parentKindIs(id BlockID, f func(BlockKind) bool) bool {
	if p := c.ParentOf(id); p != nil {
		return f(p.Kind)
	}
	return false
}

// blockTrigger 是行首触发（光标前的文本，含触发字符）；enter 为真表示
// 触发键是回车。
func (c *Core) blockTrigger(id BlockID, prefix string, enter bool) BlockKind {
	cur := c.KindOf(id)
	if cur == nil {
		return nil
	}
	inCell := c.IsInTable(id)
	inQuote := c.parentKindIs(id, func(k BlockKind) bool { _, ok := k.(Quote); return ok })
	inCallout := c.parentKindIs(id, func(k BlockKind) bool { _, ok := k.(Callout); return ok })
	code := func(lang string) BlockKind {
		if strings.ContainsFunc(lang, unicode.IsSpace) || inCallout {
			return nil
		}
		return Code{Lang: LangResolve(lang)}
	}
	if enter {
		if rest, ok := strings.CutPrefix(prefix, "```"); ok {
			return code(rest)
		}
		return nil
	}
	body, ok := strings.CutSuffix(prefix, " ")
	if !ok {
		return nil
	}
	switch body {
	case "-", "*", "+":
		return Bullet{}
	case "[]", "[ ]", "【】":
		return Todo{Done: false}
	case ">", "|", "》":
		if !inCell && !inQuote {
			return Quote{}
		}
	}
	if body != "" && len(body) <= 9 && allHashes(body) {
		level := uint8(len(body))
		if h, isHeading := cur.(Heading); isHeading && h.Level == level {
			return nil
		}
		return Heading{Level: level}
	}
	num, numOK := strings.CutSuffix(body, ".")
	if !numOK {
		num, numOK = strings.CutSuffix(body, "。")
	}
	if numOK && num != "" && allDigits(num) {
		n, ok := parseU32(num)
		if !ok {
			return nil
		}
		// 标题上输入 "1. " 改为开启标题自动编号。
		if h, isHeading := cur.(Heading); isHeading {
			return Heading{Level: h.Level, SeqAuto: true}
		}
		var seq *uint32
		if n != 1 {
			seq = &n
		}
		return Ordered{Seq: seq}
	}
	if lang, ok := strings.CutPrefix(body, "```"); ok {
		return code(lang)
	}
	return nil
}

func allHashes(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != '#' {
			return false
		}
	}
	return true
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func parseU32Default(s string, def uint32) uint32 {
	var n uint64
	for i := 0; i < len(s); i++ {
		d := uint64(s[i] - '0')
		if n > (0xFFFFFFFF-d)/10 {
			return def
		}
		n = n*10 + d
	}
	if n > 0xFFFFFFFF {
		return def
	}
	return uint32(n)
}

// MarkdownBlockShortcut 刚输入空格后调用。
func (c *Core) MarkdownBlockShortcut() bool {
	return c.markdownBlockTrigger(false)
}

// MarkdownEnterShortcut 回车、切块前调用（"```lang"+回车变代码块）。
func (c *Core) MarkdownEnterShortcut() bool {
	return c.MarkdownDivider() || c.markdownBlockTrigger(true)
}

func (c *Core) markdownBlockTrigger(enter bool) bool {
	p := c.Sel.Head
	if !c.Sel.IsCollapsed() || !c.markdownEnabled(p.Block) {
		return false
	}
	t := c.TextOf(p.Block)
	if t == nil {
		return false
	}
	prefix := t.Text[:p.Offset]
	kind := c.blockTrigger(p.Block, prefix, enter)
	if kind == nil {
		return false
	}
	// 触发字符是独立的一步撤销；转换又是一步，首个 ⌘Z 先还原字面前缀。
	c.Checkpoint(EditOther)
	c.BreakCoalescing()
	if tm := c.textMut(p.Block); tm != nil {
		empty := RichText{}
		tm.Replace(0, p.Offset, &empty)
	}
	if _, isQuote := kind.(Quote); isQuote {
		c.wrapBlocks([]BlockID{p.Block}, kind)
	} else {
		if _, isCode := kind.(Code); isCode {
			if b := c.BlockMut(p.Block); b != nil {
				b.Text = Plain(b.Text.Text)
			}
		}
		c.turnOne(p.Block, kind)
	}
	c.normalize()
	c.Sel = Caret(NewPos(p.Block, 0))
	return true
}

// MarkdownDivider：行首的 `---`（第三根横线时）和 `***`（空格或回车时）
// 变分割线；光标后的文字落到分割线下。
func (c *Core) MarkdownDivider() bool {
	p := c.Sel.Head
	if !c.Sel.IsCollapsed() || !c.markdownEnabled(p.Block) || c.IsInTable(p.Block) {
		return false
	}
	inCallout := c.parentKindIs(p.Block, func(k BlockKind) bool { _, ok := k.(Callout); return ok })
	t := c.TextOf(p.Block)
	if t == nil {
		return false
	}
	head := strings.TrimRight(t.Text[:p.Offset], " ")
	if inCallout || (head != "---" && head != "***") {
		return false
	}
	var tail RichText
	if tm := c.textMut(p.Block); tm != nil {
		tail = tm.SplitOff(p.Offset)
	}
	c.Checkpoint(EditOther)
	c.BreakCoalescing()
	id := p.Block
	path, ok := c.path(id)
	if !ok {
		return false
	}
	list, i, ok := containerMut(&c.Doc.Blocks, path)
	if !ok {
		return false
	}
	old := (*list)[i]
	(*list)[i] = NewBlock(Divider{}, RichText{})
	nb := NewBlock(Text{}, tail)
	nb.Children = old.Children
	nid := nb.ID
	insertBlockAt(list, i+1, nb)
	c.Sel = Caret(NewPos(nid, 0))
	return true
}

// Autolink：URL 后输入空格变链接（http(s)://…、www.…）。
func (c *Core) Autolink() bool {
	p := c.Sel.Head
	if !c.Sel.IsCollapsed() || !c.markdownEnabled(p.Block) {
		return false
	}
	t := c.TextOf(p.Block)
	if t == nil {
		return false
	}
	before, ok := strings.CutSuffix(t.Text[:p.Offset], " ")
	if !ok {
		return false
	}
	start := 0
	if i := strings.LastIndexFunc(before, func(r rune) bool { return isUnicodeSpace(r) }); i >= 0 {
		start = i + utf8LenAt(before, i)
	}
	word := strings.TrimRight(before[start:], ".,;:!?)]}'\"。，；：！？）】、")
	lower := strings.ToLower(word)
	rest := ""
	for _, pre := range []string{"https://", "http://", "www."} {
		if r, ok := strings.CutPrefix(lower, pre); ok {
			rest = r
			break
		}
	}
	if !(len(rest) > 1 && strings.ContainsFunc(rest, isAlnum)) {
		return false
	}
	end := start + len(word)
	seg := t.Slice(start, end)
	bad := false
	for _, r := range seg.Runs {
		if r.Marks.Link != nil || r.Marks.Code || r.Marks.Inline != nil {
			bad = true
			break
		}
	}
	if bad {
		return false
	}
	href := word
	if strings.HasPrefix(lower, "www.") {
		href = "https://" + word
	}
	c.Checkpoint(EditOther)
	c.BreakCoalescing()
	if tm := c.textMut(p.Block); tm != nil {
		h := href
		tm.UpdateMarks(start, end, func(m *Marks) { m.Link = &h })
	}
	return true
}

func isUnicodeSpace(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', '\f', '\v':
		return true
	}
	return unicode.IsSpace(r)
}

func utf8LenAt(s string, i int) int {
	// s[i] 是某字符首字节：返回该字符的字节长度。
	if i < 0 || i >= len(s) {
		return 1
	}
	b := s[i]
	switch {
	case b < 0x80:
		return 1
	case b&0xE0 == 0xC0:
		return 2
	case b&0xF0 == 0xE0:
		return 3
	default:
		return 4
	}
}

func isAlnum(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

type mdInlineRule struct {
	delim   string
	set     func(*Marks)
	bounded bool // 两侧都要空白/行界
}

// MarkdownInlineShortcut 行内 Markdown，每个字符输入后跑：闭合定界符
// 把 `**x**`、`__x__`、`*x*`、`_x_`、`***x***`、`~~x~~`、`~x~`（下划
// 线）和 “ `x` “ 变成样式，按固定优先级。
func (c *Core) MarkdownInlineShortcut() bool {
	p := c.Sel.Head
	if !c.Sel.IsCollapsed() || !c.markdownEnabled(p.Block) {
		return false
	}
	t := c.TextOf(p.Block)
	if t == nil {
		return false
	}
	before := t.Text[:p.Offset]
	after := t.Text[p.Offset:]
	rules := []mdInlineRule{
		{"***", func(m *Marks) { m.Bold = true; m.Italic = true }, false},
		{"___", func(m *Marks) { m.Bold = true; m.Italic = true }, true},
		{"**", func(m *Marks) { m.Bold = true }, false},
		{"__", func(m *Marks) { m.Bold = true }, true},
		{"*", func(m *Marks) { m.Italic = true }, false},
		{"_", func(m *Marks) { m.Italic = true }, true},
		{"~~", func(m *Marks) { m.Strike = true }, false},
		{"~", func(m *Marks) { m.Underline = true }, true},
		{"`", func(m *Marks) { m.Code = true }, false},
	}
	boundaryBefore := func(s string) bool {
		if s == "" {
			return true
		}
		r, _ := utf8DecodeLast(s)
		return isUnicodeSpace(r)
	}
	boundaryAfter := func(s string) bool {
		if s == "" {
			return true
		}
		r, _ := utf8DecodeFirst(s)
		return isUnicodeSpace(r)
	}
	// `$x$` 与 `$$x$$` 变行内公式。
	for _, delim := range []string{"$$", "$"} {
		if !strings.HasSuffix(before, delim) {
			continue
		}
		head := t.Slice(0, p.Offset)
		if n := len(head.Runs); n > 0 && head.Runs[n-1].Marks.Code {
			continue
		}
		innerEnd := len(before) - len(delim)
		open := strings.LastIndex(before[:innerEnd], delim)
		if open < 0 {
			continue
		}
		inner := before[open+len(delim) : innerEnd]
		if strings.TrimSpace(inner) == "" ||
			strings.HasPrefix(inner, "$") || strings.HasSuffix(inner, "$") ||
			strings.Contains(inner, EquationRune) ||
			strings.HasSuffix(before[:open], "$") ||
			!boundaryBefore(before[:open]) || !boundaryAfter(after) {
			continue
		}
		segBad := false
		for _, r := range t.Slice(open, p.Offset).Runs {
			if r.Marks.Code || r.Marks.Inline != nil {
				segBad = true
				break
			}
		}
		if segBad {
			continue
		}
		marks := t.MarksAt(open + len(delim))
		obj := ToInline(Equation{Src: inner})
		marks.Inline = &obj
		c.Checkpoint(EditOther)
		c.BreakCoalescing()
		var ins RichText
		ins.Push(EquationRune, marks)
		if tm := c.textMut(p.Block); tm != nil {
			tm.Replace(open, p.Offset, &ins)
		}
		c.Sel = Caret(NewPos(p.Block, open+len(EquationRune)))
		return true
	}
	for _, rule := range rules {
		if !strings.HasSuffix(before, rule.delim) {
			continue
		}
		innerEnd := len(before) - len(rule.delim)
		open := strings.LastIndex(before[:innerEnd], rule.delim)
		if open < 0 {
			continue
		}
		inner := before[open+len(rule.delim) : innerEnd]
		dc := rune(rule.delim[0])
		firstInner, _ := utf8DecodeFirst(inner)
		lastInner, _ := utf8DecodeLast(inner)
		if inner == "" || isUnicodeSpace(firstInner) || firstInner == dc || lastInner == dc {
			continue
		}
		// 定界符串两侧必须正好这么长。
		if strings.HasSuffix(before[:open], rule.delim[:1]) {
			continue
		}
		if rule.bounded && !(boundaryBefore(before[:open]) && boundaryAfter(after)) {
			continue
		}
		if dc != '`' {
			bad := false
			for _, r := range t.Slice(open, innerEnd).Runs {
				if r.Marks.Code {
					bad = true
					break
				}
			}
			if bad {
				continue
			}
		}
		core := t.Slice(open+len(rule.delim), innerEnd)
		already := true
		for _, r := range core.Runs {
			m := r.Marks
			rule.set(&m)
			if !m.Equal(r.Marks) {
				already = false
				break
			}
		}
		if already {
			// 已是这种格式：不动文字。
			continue
		}
		length := core.Len()
		core.UpdateMarks(0, length, rule.set)
		c.Checkpoint(EditOther)
		c.BreakCoalescing()
		if tm := c.textMut(p.Block); tm != nil {
			tm.Replace(open, p.Offset, &core)
		}
		end := open + length
		c.Sel = Caret(NewPos(p.Block, end))
		// 转换后的输入不带格式。
		if tt := c.TextOf(p.Block); tt != nil {
			m := tt.MarksAt(end)
			m.Bold = false
			m.Italic = false
			m.Underline = false
			m.Strike = false
			m.Code = false
			c.PendingMarks = &m
		}
		return true
	}
	return false
}

func utf8DecodeFirst(s string) (rune, int) {
	for i, r := range s {
		_ = i
		return r, utf8RuneLen(s)
	}
	return 0, 0
}

func utf8DecodeLast(s string) (rune, int) {
	if len(s) == 0 {
		return 0, 0
	}
	start := len(s) - 1
	for start > 0 && s[start]&0xC0 == 0x80 {
		start--
	}
	r, _ := utf8DecodeFirst(s[start:])
	return r, start
}

func utf8RuneLen(s string) int {
	for range s {
		break
	}
	// 第一个 rune 的字节长度
	if len(s) == 0 {
		return 0
	}
	b := s[0]
	switch {
	case b < 0x80:
		return 1
	case b&0xE0 == 0xC0:
		return 2
	case b&0xF0 == 0xE0:
		return 3
	default:
		return 4
	}
}

// ------------------------------------------------------------------ 粘贴

// PasteBlocks 把块粘贴到光标处。第一个纯段落并进光标前的文字，光标后
// 的文字并进最后一个。
func (c *Core) PasteBlocks(blocks []Block) {
	if len(blocks) == 0 {
		return
	}
	// 整张表粘进表格里是填单元格。
	if c.pasteTableMatrix(blocks) {
		return
	}
	c.Checkpoint(EditOther)
	c.BreakCoalescing()
	p := c.deleteSelectionInner()
	fresh := make([]Block, len(blocks))
	for i := range blocks {
		fresh[i] = blocks[i].FreshIDs()
	}
	blocks = fresh
	isPlain := func(b *Block) bool {
		_, isText := b.Kind.(Text)
		return isText && len(b.Children) == 0
	}
	if p.Block == TitleID {
		// 只有第一块的文字进标题；其余从头开始正文。
		first := blocks[0]
		blocks = blocks[1:]
		text := strings.ReplaceAll(first.Text.PlainText(), "\n", " ")
		if t := c.textMut(TitleID); t != nil {
			t.InsertPlain(p.Offset, text, Marks{})
		}
		c.Sel = Caret(NewPos(TitleID, p.Offset+len(text)))
		prev := TitleID
		for _, b := range blocks {
			id := b.ID
			c.InsertAfter(prev, b)
			prev = id
		}
		if prev != TitleID {
			c.normalize()
			c.CaretToEndOf(prev)
		}
		return
	}
	if c.IsCode(p.Block) {
		// 拍平成纯文本行，每层嵌进两个空格。
		var lines []string
		var flat func(list []Block, depth int)
		flat = func(list []Block, depth int) {
			for i := range list {
				b := &list[i]
				if HasText(b.Kind) {
					for _, line := range strings.Split(b.Text.PlainText(), "\n") {
						lines = append(lines, strings.Repeat("  ", depth)+line)
					}
				}
				if tb, ok := b.Kind.(Table); ok {
					for _, cell := range tb.Cells() {
						flat(cell.Cell.Blocks, depth)
					}
				}
				nested := 0
				if HasText(b.Kind) && !IsContainer(b.Kind) {
					nested = 1
				}
				flat(b.Children, depth+nested)
			}
		}
		flat(blocks, 0)
		text := strings.Join(lines, "\n")
		if t := c.textMut(p.Block); t != nil {
			t.InsertPlain(p.Offset, text, Marks{})
		}
		c.Sel = Caret(NewPos(p.Block, p.Offset+len(text)))
		return
	}
	if c.IsInTable(p.Block) {
		n := len(blocks)
		kept := blocks[:0]
		for i := range blocks {
			if _, isTable := blocks[i].Kind.(Table); !isTable {
				kept = append(kept, blocks[i])
			}
		}
		blocks = kept
		if len(blocks) < n {
			c.Notice = "当前位置不支持粘贴表格"
		}
		if len(blocks) == 0 {
			return
		}
	}
	cur := c.Block(p.Block)
	if cur == nil || !HasText(cur.Kind) {
		// 光标在原子块上：插到它后面。
		prev := p.Block
		last := blocks[len(blocks)-1].ID
		for _, b := range blocks {
			id := b.ID
			c.InsertAfter(prev, b)
			prev = id
		}
		c.normalize()
		c.CaretToEndOf(last)
		return
	}
	curClone := cur.Clone()
	var tail RichText
	if t := c.textMut(p.Block); t != nil {
		tail = t.SplitOff(p.Offset)
	}
	// 第一块并进头部。
	caret := NewPos(p.Block, p.Offset)
	if isPlain(&blocks[0]) {
		first := blocks[0]
		blocks = blocks[1:]
		if t := c.textMut(p.Block); t != nil {
			t.Append(&first.Text)
			caret = NewPos(p.Block, t.Len())
		}
	}
	prev := p.Block
	lastInserted := BlockID(0)
	for _, b := range blocks {
		id := b.ID
		c.InsertAfter(prev, b)
		prev = id
		lastInserted = id
	}
	headEmpty := c.LenOf(p.Block) == 0
	_, curIsText := curClone.Kind.(Text)
	if lastInserted != 0 {
		lastBlock := c.Block(lastInserted)
		_, lastIsTextKind := func() (BlockKind, bool) { k := lastBlock.Kind; return k, true }()
		_, isCode := lastBlock.Kind.(Code)
		lastIsText := HasText(lastBlock.Kind) && !isCode
		_ = lastIsTextKind
		if lastIsText {
			length := c.LenOf(lastInserted)
			if t := c.textMut(lastInserted); t != nil {
				t.Append(&tail)
			}
			caret = NewPos(lastInserted, length)
			// 空段落里粘贴非文本首块：替换掉它。
			if headEmpty && curIsText && len(curClone.Children) == 0 {
				c.removeSubtree(p.Block)
			}
		} else {
			if !tail.IsEmpty() {
				nb := NewBlock(resetForSplit(curClone.Kind), tail)
				c.InsertAfter(lastInserted, nb)
			}
			if headEmpty && curIsText && len(curClone.Children) == 0 {
				c.removeSubtree(p.Block)
			}
			c.normalize()
			c.CaretToEndOf(lastInserted)
			return
		}
	} else if t := c.textMut(p.Block); t != nil {
		t.Append(&tail)
	}
	c.normalize()
	c.Sel = Caret(caret)
}

// CaretToEndOf 把光标放到 id 子树里最后一个文本 unit 末尾（原子块则落
// 在它上面）。
func (c *Core) CaretToEndOf(id BlockID) {
	us := units(&c.Doc)
	inside := insideOf(pathIndex(c.Doc.Blocks), []BlockID{id})
	target := id
	for i := len(us) - 1; i >= 0; i-- {
		if us[i].ID == id || inside(us[i].ID) {
			target = us[i].ID
			break
		}
	}
	length := c.LenOf(target)
	c.Sel = Caret(NewPos(target, length))
}

// PasteText 粘贴纯文本：换行成段（代码块里保持字面）。
func (c *Core) PasteText(s string) {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	if !strings.Contains(s, "\n") || c.IsCode(c.Sel.Head.Block) {
		c.BreakCoalescing()
		c.InsertText(s)
		c.BreakCoalescing()
		return
	}
	blocks := make([]Block, 0, strings.Count(s, "\n")+1)
	for _, line := range strings.Split(s, "\n") {
		blocks = append(blocks, BlockText(line))
	}
	c.PasteBlocks(blocks)
}

// PasteMarkdown 把纯文本当 Markdown 粘贴：代码和标题里不解析；非空目
// 标只上行内规则（每行一段）；目标自己的类型关掉对应规则。
func (c *Core) PasteMarkdown(s string) {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	id := c.Sel.Head.Block
	if id == TitleID || c.IsCode(id) {
		c.PasteText(s)
		return
	}
	emptyTarget := c.Sel.IsCollapsed() && c.BlockSel == nil && c.LenOf(id) == 0
	if !emptyTarget {
		c.PasteBlocks(MarkdownInlineLines(s))
		return
	}
	var kind BlockKind
	if k := c.KindOf(id); k != nil {
		kind = k
	}
	_, kBullet := kind.(Bullet)
	_, kOrdered := kind.(Ordered)
	_, kTodo := kind.(Todo)
	off := Off{
		Bullet:      kBullet,
		Ordered:     kOrdered,
		Todo:        kTodo,
		Quote:       c.parentKindIs(id, func(k BlockKind) bool { _, ok := k.(Quote); return ok }),
		CodeDivider: c.parentKindIs(id, func(k BlockKind) bool { _, ok := k.(Callout); return ok }),
	}
	blocks := MarkdownToBlocks(s, off)
	if len(blocks) == 0 {
		c.PasteText(s)
		return
	}
	c.PasteBlocks(blocks)
}

// SelectedBlocksContent 把选中内容取成块（内部复制用）。
func (c *Core) SelectedBlocksContent() []Block {
	index := blockIndex(&c.Doc)
	if c.BlockSel != nil {
		var out []Block
		for _, id := range c.BlockSelIDs() {
			if b, ok := index[id]; ok {
				out = append(out, b.Clone())
			}
		}
		return out
	}
	us := c.Units()
	pos := unitPositions(us)
	s, e := c.OrderedIn(us)
	var out []Block
	if s.Block == e.Block {
		if t := c.TextOf(s.Block); t != nil {
			out = append(out, NewBlock(Text{}, t.Slice(s.Offset, e.Offset)))
		} else if b := c.Block(s.Block); b != nil {
			out = append(out, b.Clone())
		}
		return out
	}
	si, ei := pos[s.Block], pos[e.Block]
	if _, ok := pos[s.Block]; !ok {
		si = 0
	}
	if _, ok := pos[e.Block]; !ok {
		ei = 0
	}
	// 块自身文字里被选中的部分（对照 RangeIn）。
	rangeOf := func(id BlockID, length int) (int, int, bool) {
		i, ok := pos[id]
		if !ok {
			return 0, 0, false
		}
		if i < si || i > ei {
			return 0, 0, false
		}
		a, b := 0, length
		if i == si {
			a = s.Offset
		}
		if i == ei {
			b = e.Offset
		}
		return a, b, true
	}
	for _, id := range c.SelectedBlocks() {
		b, ok := index[id]
		if !ok {
			continue
		}
		cb := b.Clone()
		if a, z, ok := rangeOf(id, cb.Text.Len()); ok {
			cb.Text = cb.Text.Slice(a, z)
		}
		// 剪掉选区结束之后的子块。
		pruneAfter(&cb, e, pos)
		out = append(out, cb)
	}
	return out
}
