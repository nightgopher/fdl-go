package fdl

import "strings"

// Run 是一段连续文本的样式；Len 是 Text 里的字节数。
type Run struct {
	Len   int
	Marks Marks
}

// RichText 是纯文本加按长度分段的样式列表。Runs 恰好覆盖 Text
// （Len 之和 == len(Text)），没有零长度段，相邻段样式不同。
type RichText struct {
	Text string
	Runs []Run
}

// Span 是一个 run 覆盖的字节区间。
type Span struct {
	Start int
	End   int
	Marks Marks
}

// Plain 构造无样式文本。
func Plain(s string) RichText {
	if s == "" {
		return RichText{}
	}
	return RichText{Text: s, Runs: []Run{{Len: len(s)}}}
}

// Len 返回文本的字节数。
func (t RichText) Len() int { return len(t.Text) }

// IsEmpty 报告文本是否为空。
func (t RichText) IsEmpty() bool { return len(t.Text) == 0 }

// Push 追加文本；与末段样式相同则并入。
func (t *RichText) Push(s string, marks Marks) {
	if s == "" {
		return
	}
	t.Text += s
	if n := len(t.Runs); n > 0 && t.Runs[n-1].Marks.Equal(marks) {
		t.Runs[n-1].Len += len(s)
		return
	}
	t.Runs = append(t.Runs, Run{Len: len(s), Marks: marks})
}

// Spans 返回每个 run 覆盖的字节区间。
func (t RichText) Spans() []Span {
	out := make([]Span, len(t.Runs))
	off := 0
	for i, r := range t.Runs {
		out[i] = Span{Start: off, End: off + r.Len, Marks: r.Marks}
		off += r.Len
	}
	return out
}

// PlainText 把公式替换为 LaTeX 源码、其他行内对象删除后的文本。
func (t RichText) PlainText() string {
	if !strings.Contains(t.Text, "\ufffc") {
		return t.Text
	}
	var out strings.Builder
	for _, sp := range t.Spans() {
		seg := t.Text[sp.Start:sp.End]
		if sp.Marks.Inline != nil {
			if eq, ok := sp.Marks.Inline.Kind.(Equation); ok {
				for range seg {
					out.WriteString(eq.Src)
				}
				continue
			}
		}
		out.WriteString(strings.ReplaceAll(seg, "\ufffc", ""))
	}
	return out.String()
}

// floorCharBoundary 返回不超过 i 的最大 UTF-8 字符边界。
func floorCharBoundary(s string, i int) int {
	if i >= len(s) {
		return len(s)
	}
	if i <= 0 {
		return 0
	}
	for i > 0 && s[i]&0xC0 == 0x80 {
		i--
	}
	return i
}

// Slice 取 [start, end) 字节区间的子文本；区间回退到字符边界，过期
// 偏移不会 panic。
func (t RichText) Slice(start, end int) RichText {
	s, e := t.clamp(start, end)
	var out RichText
	for _, sp := range t.Spans() {
		a, b := max(sp.Start, s), min(sp.End, e)
		if a < b {
			out.Push(t.Text[a:b], sp.Marks)
		}
	}
	return out
}

// clamp 把区间夹进文本并回退到字符边界。
func (t RichText) clamp(start, end int) (int, int) {
	e := floorCharBoundary(t.Text, end)
	return min(floorCharBoundary(t.Text, start), e), e
}

// Append 把 other 追加到末尾。
func (t *RichText) Append(other *RichText) {
	off := 0
	for _, r := range other.Runs {
		t.Push(other.Text[off:off+r.Len], r.Marks)
		off += r.Len
	}
}

// typingMarks 是新输入文字继承的样式：链接、行内代码和行内对象不延续。
func typingMarks(m Marks) Marks {
	m.Link = nil
	m.Code = false
	m.Inline = nil
	return m
}

// MarksAt 返回在 offset 处输入应继承的样式（取到 offset 结束或包含
// offset 的 run，偏取左侧，和多数编辑器一致）。
func (t RichText) MarksAt(offset int) Marks {
	last := Marks{}
	for _, sp := range t.Spans() {
		switch {
		case sp.Start < offset && offset < sp.End:
			// 严格在 run 内部：链接和行内代码也延续。
			m := sp.Marks
			m.Inline = nil
			return m
		case sp.Start < offset && offset == sp.End:
			return typingMarks(sp.Marks)
		case sp.Start >= offset:
			if offset == 0 {
				return typingMarks(sp.Marks)
			}
			return typingMarks(last)
		}
		last = sp.Marks
	}
	return typingMarks(last)
}

// Replace 用 ins 替换 [start, end)。
func (t *RichText) Replace(start, end int, ins *RichText) {
	out := t.Slice(0, start)
	out.Append(ins)
	tail := t.Slice(end, t.Len())
	out.Append(&tail)
	*t = out
}

// InsertPlain 在 offset 处插入带 marks 的纯文本。
func (t *RichText) InsertPlain(offset int, s string, marks Marks) {
	var ins RichText
	ins.Push(s, marks)
	t.Replace(offset, offset, &ins)
}

// SplitOff 把 [offset, 末尾) 切走返回，自身只剩前缀。
func (t *RichText) SplitOff(offset int) RichText {
	tail := t.Slice(offset, t.Len())
	head := t.Slice(0, offset)
	*t = head
	return tail
}

// UpdateMarks 对与 [start, end) 重叠的每个 run 的样式应用 f。
func (t *RichText) UpdateMarks(start, end int, f func(*Marks)) {
	s, e := t.clamp(start, end)
	if s >= e {
		return
	}
	var out RichText
	for _, sp := range t.Spans() {
		a, b := sp.Start, sp.End
		ms := min(max(a, s), b)
		me := max(min(b, e), ms)
		out.Push(t.Text[a:ms], sp.Marks)
		m := sp.Marks
		f(&m)
		out.Push(t.Text[ms:me], m)
		out.Push(t.Text[me:b], sp.Marks)
	}
	*t = out
}

// AllMarks 报告与 [start, end) 重叠的每个 run 是否都满足 f（空区间查
// MarksAt）。
func (t RichText) AllMarks(start, end int, f func(Marks) bool) bool {
	if start >= end {
		return f(t.MarksAt(start))
	}
	for _, sp := range t.Spans() {
		if sp.Start < end && sp.End > start {
			if !f(sp.Marks) {
				return false
			}
		}
	}
	return true
}

// Clone 深拷贝（含样式里的指针）。
func (t RichText) Clone() RichText {
	runs := make([]Run, len(t.Runs))
	for i, r := range t.Runs {
		runs[i] = Run{Len: r.Len, Marks: cloneMarks(r.Marks)}
	}
	return RichText{Text: t.Text, Runs: runs}
}
