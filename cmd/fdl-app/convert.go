package main

// fdl Document ⇄ mygo richtext.Text 转换层。
//
// 块级：Heading/List(Bullets/Numbers/Tasks)/Quote/CodeBlock/Indent/Align 一
// 一对应；fdl 的子块嵌套用 ParagraphStyle.Indent 表达，保存时按 Indent 重
// 建树。原子块（表格/图片/分割线…）整块序列化进 ParagraphStyle.ID，段落
// 显示占位文字，保存时原样还原。
//
// 行内：Marks ↔ Attributes（Bold→Weight）；行内对象（公式/附件/提及）显
// 示为占位文字，其 XML 存在 Attributes.Tag 里，保存时还原成 U+FFFC 对象。

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/richtext"

	fdl "github.com/nightgopher/fdl-go"
)

// fdlToRichText 把文档正文转成可编辑的 richtext.Text（标题单列，不进正文）。
func fdlToRichText(doc *fdl.Document) richtext.Text {
	var paras []richtext.Paragraph
	var walk func(blocks []fdl.Block, depth int, quote bool)
	walk = func(blocks []fdl.Block, depth int, quote bool) {
		for i := range blocks {
			b := &blocks[i]
			p := richtext.Paragraph{Style: richtext.ParagraphStyle{Indent: depth, Align: alignTo(b.Align), Quote: quote}}
			switch k := b.Kind.(type) {
			case fdl.Text:
			case fdl.Heading:
				p.Style.Heading = int(k.Level)
			case fdl.Bullet:
				p.Style.List = richtext.Bullets
			case fdl.Ordered:
				p.Style.List = richtext.Numbers
			case fdl.Todo:
				p.Style.List = richtext.Tasks
				p.Style.Checked = k.Done
			case fdl.Code:
				p.Style.CodeBlock = true
				p.Runs = []richtext.Run{{Text: b.Text.Text}}
				paras = append(paras, p)
				walk(b.Children, depth, quote)
				continue
			case fdl.Quote:
				// 容器：子段落都标 quote，保存时重组成容器。
				walk(b.Children, depth, true)
				continue
			default:
				// 原子块：整块存 ID，占位文字展示。
				wrap := blockWrap(b)
				p.Style.ID = "fdl:" + fdl.Serialize(&wrap)
				p.Runs = []richtext.Run{{Text: atomicLabel(b), Attributes: richtext.Attributes{Italic: true}}}
				paras = append(paras, p)
				walk(b.Children, depth, quote)
				continue
			}
			p.Runs = runsToRich(b.Text)
			paras = append(paras, p)
			walk(b.Children, depth+1, quote)
		}
	}
	walk(doc.Blocks, 0, false)
	if len(paras) == 0 {
		return richtext.TextOf(richtext.Paragraph{})
	}
	return richtext.TextOf(paras...)
}

// blockWrap 把块单独包成文档以便序列化。
func blockWrap(b *fdl.Block) fdl.Document {
	d := fdl.NewDocument()
	d.Blocks = []fdl.Block{*b}
	return d
}

// atomicLabel 是原子块的占位文字。
func atomicLabel(b *fdl.Block) string {
	switch k := b.Kind.(type) {
	case fdl.Divider:
		return "——— 分割线 ———"
	case fdl.Image:
		return "🖼 图片：" + k.Src
	case fdl.FileBlock:
		return "📎 附件：" + k.Name
	case fdl.SubPageList:
		return "📇 子页面列表"
	case fdl.Countdown:
		return "⏳ 倒计时"
	case fdl.Table:
		return "📊 表格"
	case fdl.Callout:
		return "💬 高亮块 " + k.Emoji
	case fdl.Grid:
		return "▦ 分栏"
	case fdl.Raw:
		return "⌘ " + k.Tag
	}
	return "▪ 块"
}

func alignTo(a fdl.Align) ui.Align {
	switch a {
	case fdl.AlignCenter:
		return ui.Center
	case fdl.AlignRight:
		return ui.End
	}
	return ui.Start
}

func alignFrom(a ui.Align) fdl.Align {
	switch a {
	case ui.Center:
		return fdl.AlignCenter
	case ui.End:
		return fdl.AlignRight
	}
	return fdl.AlignLeft
}

// runsToRich 把 fdl 行内文本转成 richtext runs。
func runsToRich(t fdl.RichText) []richtext.Run {
	var out []richtext.Run
	for _, sp := range t.Spans() {
		seg := t.Text[sp.Start:sp.End]
		if obj := sp.Marks.Inline; obj != nil {
			// 行内对象：占位文字 + Tag 存 XML，保存时还原。
			out = append(out, richtext.Run{Text: inlineLabel(obj), Attributes: richtext.Attributes{Tag: obj.XML}})
			continue
		}
		a := richtext.Attributes{}
		m := sp.Marks
		if m.Bold {
			a.Weight = 700
		}
		a.Italic = m.Italic
		a.Underline = m.Underline
		a.Strikethrough = m.Strike
		a.Code = m.Code
		if m.Link != nil {
			a.Link = *m.Link
		}
		if m.TextColor != nil {
			a.Color = colorOf(string(*m.TextColor), false)
		}
		if m.BgColor != nil {
			a.Background = colorOf(string(*m.BgColor), true)
		}
		out = append(out, richtext.Run{Text: seg, Attributes: a})
	}
	if len(out) == 0 {
		out = []richtext.Run{{Text: ""}}
	}
	return out
}

func inlineLabel(in *fdl.Inline) string {
	if src, ok := in.Equation(); ok {
		return "§" + src + "§"
	}
	if f, ok := in.File(); ok {
		return "📎" + f.Name
	}
	if l, ok := in.DocLink(); ok {
		return "@" + l.Title
	}
	if b, ok := in.Button(); ok {
		return " [[" + b.Text + "]] "
	}
	if r, ok := in.Reminder(); ok {
		return "⏰" + r.Label(" ")
	}
	return "⟨对象⟩"
}

func clampAlpha(a float32) float32 {
	if a < 0 {
		return 0
	}
	if a > 1 {
		return 1
	}
	return a
}

// colorOf 把 fdl 调色板名转成 UI 颜色。
func colorOf(name string, bg bool) ui.Color {
	if bg {
		if c, ok := fdl.BGColor(name); ok {
			return ui.RGBA(c.R, c.G, c.B, clampAlpha(c.A))
		}
	} else if c, ok := fdl.TextColor(name); ok {
		return ui.RGBA(c.R, c.G, c.B, clampAlpha(c.A))
	}
	return ui.Color{}
}

// richToFdl 把 richtext.Text 转回文档正文。连续 Quote 段落并成一个
// Quote 容器块；Indent 按最近的可嵌套前块挂回子级（挂不下就拍平）。
func richToFdl(t richtext.Text) []fdl.Block {
	var items []convItem
	for _, para := range t.Paragraphs() {
		s := para.Style
		if id, ok := strings.CutPrefix(s.ID, "fdl:"); ok {
			// 原子块原样还原。
			if doc, err := fdl.Parse(id); err == nil && len(doc.Blocks) > 0 {
				items = append(items, convItem{s.Indent, false, doc.Blocks[0]})
			}
			continue
		}
		if s.CodeBlock {
			var sb strings.Builder
			for _, r := range para.Runs {
				sb.WriteString(r.Text)
			}
			items = append(items, convItem{s.Indent, false, fdl.NewBlock(fdl.Code{Lang: "plaintext"}, fdl.Plain(sb.String()))})
			continue
		}
		var rt fdl.RichText
		for _, r := range para.Runs {
			if r.Attributes.Tag != "" {
				// 行内对象：丢占位文字，还原成 U+FFFC + Inline。
				if in := fdl.ParseInline(r.Attributes.Tag); in != nil {
					rt.Push("\ufffc", fdl.Marks{Inline: in})
					continue
				}
			}
			rt.Push(r.Text, marksFrom(r.Attributes))
		}
		var kind fdl.BlockKind = fdl.Text{}
		switch {
		case s.Heading > 0:
			kind = fdl.Heading{Level: uint8(min(s.Heading, 9))}
		case s.List == richtext.Bullets:
			kind = fdl.Bullet{}
		case s.List == richtext.Numbers:
			kind = fdl.Ordered{}
		case s.List == richtext.Tasks:
			kind = fdl.Todo{Done: s.Checked}
		}
		b := fdl.NewBlock(kind, rt)
		b.Align = alignFrom(s.Align)
		items = append(items, convItem{s.Indent, s.Quote, b})
	}

	// 连续 Quote 段落先并成 Quote 容器（组内按各自 depth 建子树）。
	var tree []convItem
	for i := 0; i < len(items); {
		if !items[i].quote {
			tree = append(tree, items[i])
			i++
			continue
		}
		var group []convItem
		depth := items[i].depth
		for i < len(items) && items[i].quote {
			group = append(group, items[i])
			i++
		}
		q := fdl.NewBlock(fdl.Quote{}, fdl.RichText{})
		gi := 0
		q.Children = buildTree(group, &gi, depth)
		tree = append(tree, convItem{depth, false, q})
	}
	ti := 0
	out := buildTree(tree, &ti, 0)
	if len(out) == 0 {
		out = append(out, fdl.BlockText(""))
	}
	return out
}

// convItem 是重建树时的一个节点。
type convItem struct {
	depth int
	quote bool
	b     fdl.Block
}

// buildTree 按 depth 重建嵌套（i 是共享游标）：更深的一串挂到前一个可
// 嵌套块下；挂不下（前块不可嵌套或就是根）就拍平在当前层。
func buildTree(items []convItem, i *int, depth int) []fdl.Block {
	var out []fdl.Block
	for *i < len(items) {
		it := items[*i]
		if it.depth < depth {
			break
		}
		if it.depth > depth {
			if len(out) > 0 && fdl.Nests(out[len(out)-1].Kind) {
				out[len(out)-1].Children = buildTree(items, i, it.depth)
				continue
			}
			// 挂不下：当当前层处理，不丢内容。
			out = append(out, it.b)
			*i++
			continue
		}
		out = append(out, it.b)
		*i++
	}
	return out
}

// marksFrom 把 Attributes 转回 Marks。
func marksFrom(a richtext.Attributes) fdl.Marks {
	m := fdl.Marks{
		Bold:      a.Weight >= 600,
		Italic:    a.Italic,
		Underline: a.Underline,
		Strike:    a.Strikethrough,
		Code:      a.Code,
	}
	if a.Link != "" {
		m.Link = &a.Link
	}
	return m
}
