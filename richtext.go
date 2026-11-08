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
	e := floorCharBoundary(t.Text, end)
	s := min(floorCharBoundary(t.Text, start), e)
	var out RichText
	for _, sp := range t.Spans() {
		a, b := max(sp.Start, s), min(sp.End, e)
		if a < b {
			out.Push(t.Text[a:b], sp.Marks)
		}
	}
	return out
}
