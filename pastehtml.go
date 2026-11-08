package fdl

// 剪贴板 HTML → DocxXML 块（对拍 paste.rs 的 HTML 部分）。读取剪贴板本
// 身是调用方的事。

import (
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// HTMLToBlocks 把剪贴板 HTML 转成块。
func HTMLToBlocks(htmlText string) []Block {
	dom, err := html.Parse(strings.NewReader(htmlText))
	if err != nil {
		return nil
	}
	root := htmlFind(dom, "body")
	if root == nil {
		root = dom
	}
	var out []Block
	htmlContainer(root, &htmlCtx{}, &out)
	// 在别的应用富文本里输入的 Markdown 也（按行内规则）转换。
	if !htmlHasClass(dom, "lark-doc") {
		reparseMarkdown(out)
	}
	return out
}

func htmlHasClass(n *html.Node, class string) bool {
	return htmlHasOwnClass(n, class) || anyChild(n, func(c *html.Node) bool { return htmlHasClass(c, class) })
}

func htmlHasOwnClass(n *html.Node, class string) bool {
	c, ok := htmlAttr(n, "class")
	if !ok {
		return false
	}
	for _, f := range strings.Fields(c) {
		if f == class {
			return true
		}
	}
	return false
}

func anyChild(n *html.Node, f func(*html.Node) bool) bool {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if f(c) {
			return true
		}
	}
	return false
}

// htmlCallout 是高亮块：颜色吸附最近色板，emoji id（data-emoji-id）转
// 字符。
func htmlCallout(n *html.Node, ctx *htmlCtx) Block {
	st := htmlStyle(n)
	get := func(k string) (rgba, bool) {
		for _, d := range st {
			if d.k == k {
				return parseColor(d.v)
			}
		}
		return rgba{}, false
	}
	near := func(c rgba, names []string, f func(string) (Rgba, bool)) *ColorName {
		var best *ColorName
		var bestD int64 = -1
		for _, name := range names {
			p, ok := f(name)
			if !ok {
				continue
			}
			var d int64
			for _, pair := range [][2]uint8{{c.R, p.R}, {c.G, p.G}, {c.B, p.B}} {
				diff := int64(pair[0]) - int64(pair[1])
				d += diff * diff
			}
			if bestD < 0 || d < bestD {
				bestD = d
				nameCopy := name
				best = (*ColorName)(&nameCopy)
			}
		}
		return best
	}
	var bg, border *ColorName
	if c, ok := get("background-color"); ok {
		var names []string
		for _, n := range CalloutBGColors {
			if n != nil {
				names = append(names, *n)
			}
		}
		bg = near(c, names, CalloutBG)
	}
	if c, ok := get("border-color"); ok {
		border = near(c, Hues, CalloutBorder)
	}
	emoji := "💡"
	if n.Parent != nil {
		if id, ok := htmlAttr(n.Parent, "data-emoji-id"); ok {
			if e, found := EmojiByID(id); found {
				emoji = e
			}
		}
	}
	var kids []Block
	htmlContainer(n, ctx, &kids)
	if len(kids) == 0 {
		kids = append(kids, BlockText(""))
	}
	b := NewBlock(Callout{Emoji: emoji, Bg: bg, Border: border}, RichText{})
	b.Children = kids
	return b
}

type rgba = Rgba

// reparseMarkdown 把无样式的文本段按行内 Markdown 重读。
func reparseMarkdown(blocks []Block) {
	for i := range blocks {
		b := &blocks[i]
		reparseMarkdown(b.Children)
		if tb, ok := b.Kind.(Table); ok {
			for r := range tb.Rows {
				for c := range tb.Rows[r] {
					reparseMarkdown(tb.Rows[r][c].Blocks)
				}
			}
		}
		if !HasText(b.Kind) {
			continue
		}
		if _, isCode := b.Kind.(Code); isCode {
			continue
		}
		var out RichText
		for _, sp := range b.Text.Spans() {
			if sp.Marks.Equal(Marks{}) {
				t := mdInlineParse(b.Text.Text[sp.Start:sp.End])
				out.Append(&t)
			} else {
				t := b.Text.Slice(sp.Start, sp.End)
				out.Append(&t)
			}
		}
		b.Text = out
	}
}

// htmlSkip 是不会成为文档文本的元素。
var htmlSkip = map[string]bool{
	"head": true, "title": true, "meta": true, "link": true, "style": true, "script": true, "noscript": true,
	"template": true, "iframe": true, "object": true, "embed": true, "video": true, "audio": true,
	"canvas": true, "svg": true, "math": true, "button": true, "select": true, "option": true, "textarea": true,
	"input": true, "map": true, "area": true, "source": true, "track": true,
}

// htmlBlock 是打断行内流的元素（内容另起段落）。
var htmlBlock = map[string]bool{
	"p": true, "div": true, "section": true, "article": true, "header": true, "footer": true, "main": true,
	"aside": true, "nav": true, "figure": true, "figcaption": true, "center": true, "address": true,
	"dl": true, "dt": true, "dd": true, "form": true, "fieldset": true, "legend": true, "details": true,
	"summary": true, "hgroup": true, "html": true, "body": true, "tbody": true, "thead": true, "tfoot": true,
	"tr": true, "td": true, "th": true, "caption": true,
}

type htmlCtx struct {
	marks    Marks
	align    Align
	hasAlign bool
}

func htmlTag(n *html.Node) (string, bool) {
	if n.Type != html.ElementNode {
		return "", false
	}
	return strings.ToLower(n.Data), true
}

func htmlAttr(n *html.Node, key string) (string, bool) {
	if n.Type != html.ElementNode {
		return "", false
	}
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val, true
		}
	}
	return "", false
}

func htmlFind(n *html.Node, name string) *html.Node {
	if t, ok := htmlTag(n); ok && t == name {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := htmlFind(c, name); found != nil {
			return found
		}
	}
	return nil
}

// htmlStyle 解析行内 style 属性，键值都小写。
func htmlStyle(n *html.Node) []styleDecl {
	v, ok := htmlAttr(n, "style")
	if !ok {
		return nil
	}
	var out []styleDecl
	for _, d := range strings.Split(v, ";") {
		k, v, ok := strings.Cut(d, ":")
		if !ok {
			continue
		}
		out = append(out, styleDecl{strings.ToLower(strings.TrimSpace(k)), strings.ToLower(strings.TrimSpace(v))})
	}
	return out
}

type styleDecl struct{ k, v string }

func styleGet(st []styleDecl, k string) (string, bool) {
	for _, d := range st {
		if d.k == k {
			return d.v, true
		}
	}
	return "", false
}

func fontPx(st []styleDecl) (float32, bool) {
	v, ok := styleGet(st, "font-size")
	if !ok {
		return 0, false
	}
	if n, ok := strings.CutSuffix(v, "px"); ok {
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 32)
		return float32(f), err == nil
	}
	if n, ok := strings.CutSuffix(v, "pt"); ok {
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 32)
		if err != nil {
			return 0, false
		}
		return float32(f) * 4 / 3, true
	}
	return 0, false
}

func styleHidden(st []styleDecl) bool {
	for _, d := range st {
		if (d.k == "display" && strings.HasPrefix(d.v, "none")) || (d.k == "visibility" && d.v == "hidden") {
			return true
		}
	}
	return false
}

func htmlAlignOf(n *html.Node, st []styleDecl) (Align, bool) {
	v, ok := styleGet(st, "text-align")
	if !ok {
		v, _ = htmlAttr(n, "align")
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "center", "-webkit-center":
		return AlignCenter, true
	case "right", "end", "-webkit-right":
		return AlignRight, true
	case "left", "start", "justify":
		return AlignLeft, true
	}
	return AlignLeft, false
}

// htmlInlineCtx 是 ctx 加上某行内元素及其样式带来的样式。
func htmlInlineCtx(n *html.Node, t string, st []styleDecl, ctx *htmlCtx) htmlCtx {
	c := *ctx
	m := &c.marks
	switch t {
	case "b", "strong":
		m.Bold = true
	case "i", "em", "cite", "dfn", "var":
		m.Italic = true
	case "u", "ins":
		m.Underline = true
	case "s", "strike", "del":
		m.Strike = true
	case "code", "kbd", "samp", "tt":
		m.Code = true
	case "a":
		if h, ok := htmlAttr(n, "href"); ok {
			h = strings.TrimSpace(h)
			if LinkOK(h) {
				m.Link = &h
			}
		}
	}
	for _, d := range st {
		switch d.k {
		case "font-weight":
			switch d.v {
			case "bold", "bolder":
				m.Bold = true
			case "normal", "lighter":
				m.Bold = false
			default:
				if w, err := strconv.ParseUint(d.v, 10, 32); err == nil {
					m.Bold = w >= 700
				}
			}
		case "font-style":
			m.Italic = strings.HasPrefix(d.v, "italic") || strings.HasPrefix(d.v, "oblique")
		case "text-decoration", "text-decoration-line":
			m.Underline = m.Underline || strings.Contains(d.v, "underline")
			m.Strike = m.Strike || strings.Contains(d.v, "line-through")
		case "color": // 只有调色板里的颜色能留下。
			if c, ok := parseColor(d.v); ok {
				if found, name := paletteText(c); found {
					m.TextColor = name
				}
			}
		case "background-color", "background":
			if c, ok := parseColor(d.v); ok {
				if found, name := paletteBG(c); found {
					m.BgColor = name
				}
			}
		}
	}
	return c
}

// parseColor 解析 #rgb、#rrggbb、rgb()/rgba()。
func parseColor(v string) (Rgba, bool) {
	v = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(v), "!important"))
	if h, ok := strings.CutPrefix(v, "#"); ok {
		hex2 := func(s string) (uint8, bool) {
			n, err := strconv.ParseUint(s, 16, 8)
			return uint8(n), err == nil
		}
		switch len(h) {
		case 3:
			var d [3]uint8
			for i, c := range []byte(h) {
				x, ok := hex2(string(c))
				if !ok {
					return Rgba{}, false
				}
				d[i] = x * 17
			}
			return Rgba{R: d[0], G: d[1], B: d[2], A: 1}, true
		case 6:
			r, ok1 := hex2(h[0:2])
			g, ok2 := hex2(h[2:4])
			b, ok3 := hex2(h[4:6])
			if !ok1 || !ok2 || !ok3 {
				return Rgba{}, false
			}
			return Rgba{R: r, G: g, B: b, A: 1}, true
		}
		return Rgba{}, false
	}
	inner := v
	if r, ok := strings.CutPrefix(inner, "rgba("); ok {
		inner = r
	} else if r, ok := strings.CutPrefix(inner, "rgb("); ok {
		inner = r
	} else {
		return Rgba{}, false
	}
	inner, ok := strings.CutSuffix(inner, ")")
	if !ok {
		return Rgba{}, false
	}
	var parts []string
	for _, p := range strings.FieldsFunc(inner, func(r rune) bool { return r == ',' || r == ' ' || r == '/' }) {
		if p != "" {
			parts = append(parts, p)
		}
	}
	num := func(i int) (float32, bool) {
		if i >= len(parts) {
			return 0, false
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(parts[i]), 32)
		return float32(f), err == nil
	}
	r, ok1 := num(0)
	g, ok2 := num(1)
	b, ok3 := num(2)
	if !ok1 || !ok2 || !ok3 {
		return Rgba{}, false
	}
	a := float32(1)
	if len(parts) > 3 {
		av, ok := num(3)
		if !ok {
			return Rgba{}, false
		}
		a = av
	}
	return Rgba{R: f32ToU8(r), G: f32ToU8(g), B: f32ToU8(b), A: a}, true
}

func f32ToU8(v float32) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return uint8(v)
}

func colorSame(a, b Rgba) bool {
	return a.R == b.R && a.G == b.G && a.B == b.B && absF32(a.A-b.A) < 0.02
}

func absF32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// paletteText：(true, nil) 表示默认文字色。
func paletteText(c Rgba) (found bool, name *ColorName) {
	for _, f := range FontColors {
		v := hexColor(f.Hex)
		if colorSame(c, Rgba{R: v.R, G: v.G, B: v.B, A: 1}) {
			return true, (*ColorName)(f.Name)
		}
	}
	return false, nil
}

// paletteBG：(true, nil) 表示无背景。
func paletteBG(c Rgba) (found bool, name *ColorName) {
	if c.A == 0 {
		return true, nil
	}
	for _, n := range BGColors {
		if n == nil {
			continue
		}
		if p, ok := BGColor(*n); ok && colorSame(c, p) {
			cn := ColorName(*n)
			return true, &cn
		}
	}
	return false, nil
}

func htmlContainer(n *html.Node, ctx *htmlCtx, out *[]Block) {
	var para RichText
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		htmlNode(c, ctx, out, &para)
	}
	htmlFlush(&para, ctx, out)
}

func htmlNode(n *html.Node, ctx *htmlCtx, out *[]Block, para *RichText) {
	switch n.Type {
	case html.TextNode:
		htmlPushText(para, n.Data, &ctx.marks)
	case html.ElementNode:
		t, _ := htmlTag(n)
		st := htmlStyle(n)
		if htmlSkip[t] || styleHidden(st) {
			return
		}
		bctx := *ctx
		if a, ok := htmlAlignOf(n, st); ok {
			bctx.align, bctx.hasAlign = a, true
		}
		switch {
		case t == "br":
			htmlTrimEndSpaces(para)
			para.Push("\n", ctx.marks)
		case t == "sup" || t == "sub": // 上下标文字加括号。
			c := htmlInlineCtx(n, t, st, ctx)
			para.Push("(", c.marks)
			for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
				htmlNode(ch, &c, out, para)
			}
			para.Push(")", c.marks)
		case t == "img":
			htmlFlush(para, ctx, out)
			if b, ok := htmlImage(n); ok {
				*out = append(*out, b)
			}
		case t == "hr":
			htmlFlush(para, ctx, out)
			*out = append(*out, NewBlock(Divider{}, RichText{}))
		case t >= "h1" && t <= "h6" && len(t) == 2:
			htmlFlush(para, ctx, out)
			level := uint8(1)
			if l, err := strconv.Atoi(t[1:]); err == nil {
				level = uint8(l)
			}
			var kids []Block
			htmlContainer(n, &bctx, &kids)
			var text RichText
			i := 0
			for k := range kids {
				if !HasText(kids[k].Kind) {
					continue
				}
				if i > 0 {
					text.Push("\n", Marks{})
				}
				kid := kids[k].Text
				text.Append(&kid)
				i++
			}
			if !text.IsEmpty() {
				b := NewBlock(Heading{Level: level}, text)
				b.Align = bctx.align
				*out = append(*out, b)
			}
			for k := range kids {
				if !HasText(kids[k].Kind) {
					*out = append(*out, kids[k])
				}
			}
		case t == "ul" || t == "ol" || t == "menu":
			htmlFlush(para, ctx, out)
			htmlList(n, &bctx, out, t == "ol")
		case t == "li":
			htmlFlush(para, ctx, out)
			*out = append(*out, htmlItem(n, &bctx, false, nil))
		case t == "blockquote":
			htmlFlush(para, ctx, out)
			var kids []Block
			htmlContainer(n, &bctx, &kids)
			if len(kids) > 0 {
				b := NewBlock(Quote{}, RichText{})
				b.Children = kids
				*out = append(*out, b)
			}
		case t == "pre" && (htmlCodeLang(n) != nil || htmlFind(n, "code") != nil):
			htmlFlush(para, ctx, out)
			*out = append(*out, htmlCode(n))
		case t == "pre": // 不是代码的 <pre> 是保留换行的文本。
			htmlFlush(para, ctx, out)
			text := htmlRawText(n)
			lines := strings.Split(strings.TrimRight(strings.ReplaceAll(text, "\r\n", "\n"), "\n"), "\n")
			for _, line := range lines {
				b := NewBlock(Text{}, RichText{})
				b.Text.Push(strings.ReplaceAll(line, "\t", " "), ctx.marks)
				b.Align = bctx.align
				*out = append(*out, b)
			}
		case t == "table":
			htmlFlush(para, ctx, out)
			if b, ok := htmlTable(n, &bctx); ok {
				*out = append(*out, b)
			}
		case htmlHasOwnClass(n, "callout-block"): // 高亮块
			htmlFlush(para, ctx, out)
			*out = append(*out, htmlCallout(n, &bctx))
		case htmlBlock[t]:
			htmlFlush(para, ctx, out)
			before := len(*out)
			htmlContainer(n, &bctx, out)
			// 大字号（样式标题）读作一级标题。
			if px, ok := fontPx(st); ok && px >= 20 {
				for i := before; i < len(*out); i++ {
					if _, isText := (*out)[i].Kind.(Text); isText && len((*out)[i].Children) == 0 {
						(*out)[i].Kind = Heading{Level: 1}
					}
				}
			}
			// 空段是空行。
			if len(*out) == before && t == "p" {
				b := BlockText("")
				b.Align = bctx.align
				*out = append(*out, b)
			}
		default:
			c := htmlInlineCtx(n, t, st, ctx)
			for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
				htmlNode(ch, &c, out, para)
			}
		}
	default:
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			htmlNode(ch, ctx, out, para)
		}
	}
}

// htmlPushText 折叠 HTML 空白：连续空白成一个空格，行首没有。
func htmlPushText(para *RichText, s string, marks *Marks) {
	var t strings.Builder
	space := para.IsEmpty() || strings.HasSuffix(para.Text, " ") || strings.HasSuffix(para.Text, "\n")
	for _, c := range s {
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' {
			if !space {
				t.WriteByte(' ')
				space = true
			}
			continue
		}
		if c == 0xa0 {
			t.WriteByte(' ')
		} else {
			t.WriteRune(c)
		}
		space = false
	}
	if t.Len() > 0 {
		para.Push(t.String(), *marks)
	}
}

func htmlTrimEndSpaces(t *RichText) {
	end := len(strings.TrimRight(t.Text, " "))
	if end < len(t.Text) {
		*t = t.Slice(0, end)
	}
}

func htmlFlush(para *RichText, ctx *htmlCtx, out *[]Block) {
	t := *para
	*para = RichText{}
	start := len(t.Text) - len(strings.TrimLeft(t.Text, " \n"))
	end := len(strings.TrimRight(t.Text, " \n"))
	if end <= start {
		return
	}
	// <br> 开始新块。
	t = t.Slice(start, end)
	at := 0
	for _, line := range strings.Split(t.Text, "\n") {
		b := NewBlock(Text{}, t.Slice(at, at+len(line)))
		b.Align = ctx.align
		*out = append(*out, b)
		at += len(line) + 1
	}
}

func htmlList(n *html.Node, ctx *htmlCtx, out *[]Block, ordered bool) {
	var start *uint32
	if s, ok := htmlAttr(n, "start"); ok {
		if v, err := strconv.ParseUint(strings.TrimSpace(s), 10, 32); err == nil && v != 1 {
			u := uint32(v)
			start = &u
		}
	}
	var items []Block
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		t, isEl := htmlTag(c)
		switch {
		case isEl && t == "li":
			var seq *uint32
			if len(items) == 0 {
				seq = start
			}
			items = append(items, htmlItem(c, ctx, ordered, seq))
		case isEl && (t == "ul" || t == "ol"): // 直接嵌在列表里的列表属于前一项。
			var kids []Block
			htmlList(c, ctx, &kids, t == "ol")
			if len(items) > 0 {
				items[len(items)-1].Children = append(items[len(items)-1].Children, kids...)
			} else {
				items = append(items, kids...)
			}
		case isEl:
			var kids []Block
			htmlContainer(c, ctx, &kids)
			items = append(items, kids...)
		}
	}
	*out = append(*out, items...)
}

// htmlItem 是一个 <li>：第一段是项文字，其余是子块。
func htmlItem(li *html.Node, ctx *htmlCtx, ordered bool, seq *uint32) Block {
	var kind BlockKind = Bullet{}
	if done, ok := htmlCheckbox(li); ok {
		kind = Todo{Done: done}
	} else if ordered {
		if v, ok := htmlAttr(li, "value"); ok {
			if n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 32); err == nil {
				u := uint32(n)
				kind = Ordered{Seq: &u}
			} else {
				kind = Ordered{Seq: seq}
			}
		} else {
			kind = Ordered{Seq: seq}
		}
	}
	var kids []Block
	htmlContainer(li, ctx, &kids)
	var text RichText
	if len(kids) > 0 {
		if _, isText := kids[0].Kind.(Text); isText && len(kids[0].Children) == 0 {
			text = kids[0].Text
			kids = kids[1:]
		}
	}
	b := NewBlock(kind, text)
	b.Children = kids
	return b
}

// htmlCheckbox：项自带勾选框时返回是否勾选（GFM 任务列表）。
func htmlCheckbox(li *html.Node) (bool, bool) {
	// <li data-list="done">（勾选）/ "check"（未勾选）。
	switch v, _ := htmlAttr(li, "data-list"); v {
	case "done":
		return true, true
	case "check", "undone", "todo":
		return false, true
	}
	var walk func(n *html.Node) (bool, bool)
	walk = func(n *html.Node) (bool, bool) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			t, isEl := htmlTag(c)
			switch {
			case isEl && (t == "ul" || t == "ol"):
			case isEl && t == "input":
				if v, ok := htmlAttr(c, "type"); ok && strings.EqualFold(v, "checkbox") {
					_, checked := htmlAttr(c, "checked")
					return checked, true
				}
			case isEl:
				if v, ok := walk(c); ok {
					return v, true
				}
			}
		}
		return false, false
	}
	return walk(li)
}

func htmlRawText(n *html.Node) string {
	var out strings.Builder
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		switch n.Type {
		case html.TextNode:
			out.WriteString(n.Data)
		case html.ElementNode:
			t, _ := htmlTag(n)
			switch {
			case t == "br":
				out.WriteByte('\n')
			case htmlSkip[t]:
			default:
				block := t == "div" || t == "p" || t == "li" || t == "tr"
				if block && out.Len() > 0 && !strings.HasSuffix(out.String(), "\n") {
					out.WriteByte('\n')
				}
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					walk(c)
				}
			}
		default:
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
		}
	}
	walk(n)
	// \u{a0} → 空格。
	return strings.ReplaceAll(out.String(), "\u00a0", " ")
}

func htmlCode(pre *html.Node) Block {
	text := htmlRawText(pre)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.TrimSuffix(text, "\n")
	lang := LangPlain
	if l := htmlCodeLang(pre); l != nil {
		lang = LangResolve(*l)
	}
	return NewBlock(Code{Lang: lang}, Plain(text))
}

// htmlCodeLang 从 <pre>、其中 <code> 或外层元素的 language-x / lang-x /
// highlight-source-x class 或 lang 属性取语言。
func htmlCodeLang(pre *html.Node) *string {
	nodes := []*html.Node{pre}
	for c := pre.FirstChild; c != nil; c = c.NextSibling {
		if t, ok := htmlTag(c); ok && t == "code" {
			nodes = append(nodes, c)
		}
	}
	if pre.Parent != nil {
		nodes = append(nodes, pre.Parent)
	}
	for _, n := range nodes {
		for _, key := range []string{"data-lark-language", "data-lang", "data-language"} {
			if v, ok := htmlAttr(n, key); ok && strings.TrimSpace(v) != "" {
				return &v
			}
		}
		classes, _ := htmlAttr(n, "class")
		fields := strings.Fields(classes)
		hasHljs := false
		for _, c := range fields {
			if c == "hljs" {
				hasHljs = true
			}
		}
		if hasHljs {
			for _, c := range fields {
				if c != "hljs" && !strings.HasPrefix(c, "hljs-") {
					return &c
				}
			}
		}
		for _, c := range fields {
			for _, p := range []string{"language-", "lang-", "highlight-source-", "data-lark-language-"} {
				if l, ok := strings.CutPrefix(c, p); ok && l != "" {
					return &l
				}
			}
		}
	}
	return nil
}

func htmlImage(n *html.Node) (Block, bool) {
	src, ok := htmlAttr(n, "src")
	if !ok {
		return Block{}, false
	}
	src = strings.TrimSpace(src)
	l := strings.ToLower(src)
	if !(strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://") || strings.HasPrefix(l, "data:image/")) {
		p, isFile := strings.CutPrefix(src, "file://")
		if !isFile {
			return Block{}, false
		}
		src = percentDecode(p)
	}
	b := NewBlock(Image{SrcKind: "src", Src: src}, RichText{})
	b.Align = AlignCenter
	return b, true
}

func percentDecode(s string) string {
	var out []byte
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+3], 16, 8); err == nil {
				out = append(out, byte(v))
				i += 2
				continue
			}
		}
		out = append(out, s[i])
	}
	return string(out)
}

func htmlWidthPx(n *html.Node) (float32, bool) {
	v, ok := styleGet(htmlStyle(n), "width")
	if !ok {
		v, ok = htmlAttr(n, "width")
		if !ok {
			return 0, false
		}
	}
	v = strings.TrimSpace(v)
	v = strings.TrimSuffix(v, "px")
	w, err := strconv.ParseFloat(strings.TrimSpace(v), 32)
	if err != nil || w <= 0 {
		return 0, false
	}
	return float32(w), true
}

// htmlColWidths 从 <col> 取列宽，否则从第一行的单列单元格。
func htmlColWidths(table *html.Node, trs []*html.Node, ncols int) []float32 {
	w := make([]*float32, ncols)
	var cols []*html.Node
	for c := table.FirstChild; c != nil; c = c.NextSibling {
		t, isEl := htmlTag(c)
		if !isEl {
			continue
		}
		if t == "colgroup" {
			for x := c.FirstChild; x != nil; x = x.NextSibling {
				if xt, ok := htmlTag(x); ok && xt == "col" {
					cols = append(cols, x)
				}
			}
		} else if t == "col" {
			cols = append(cols, c)
		}
	}
	i := 0
	for _, col := range cols {
		span := 1
		if s, ok := htmlAttr(col, "span"); ok {
			if n, err := strconv.Atoi(s); err == nil {
				span = n
			}
		}
		for k := 0; k < span; k++ {
			if i < ncols {
				if x, ok := htmlWidthPx(col); ok {
					v := x
					w[i] = &v
				} else {
					w[i] = nil
				}
			}
			i++
		}
	}
	if len(trs) > 0 {
		c := 0
		for td := trs[0].FirstChild; td != nil; td = td.NextSibling {
			t, isEl := htmlTag(td)
			if !isEl || (t != "td" && t != "th") {
				continue
			}
			span := 1
			if s, ok := htmlAttr(td, "colspan"); ok {
				if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n > 0 {
					span = n
				}
			}
			if span == 1 && c < ncols && w[c] == nil {
				if x, ok := htmlWidthPx(td); ok {
					v := x
					w[c] = &v
				}
			}
			c += span
		}
	}
	out := make([]float32, ncols)
	for i := range w {
		if w[i] == nil {
			out[i] = TableDefaultWidth
		} else {
			out[i] = maxF32(roundF32(*w[i]), TableMinWidth)
		}
	}
	return out
}

func maxF32(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

func htmlTable(n *html.Node, ctx *htmlCtx) (Block, bool) {
	var trs []*html.Node
	var trHead []bool
	var rowsOf func(n *html.Node, head bool)
	rowsOf = func(n *html.Node, head bool) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			t, isEl := htmlTag(c)
			switch {
			case isEl && t == "thead":
				rowsOf(c, true)
			case isEl && (t == "tbody" || t == "tfoot"):
				rowsOf(c, head)
			case isEl && t == "tr":
				trs = append(trs, c)
				trHead = append(trHead, head)
			}
		}
	}
	rowsOf(n, false)
	var rows [][]TableCell
	var taken [][]bool
	var allTh []bool
	for r, tr := range trs {
		for len(rows) <= r {
			rows = append(rows, nil)
			taken = append(taken, nil)
		}
		c := 0
		th := true
		for td := tr.FirstChild; td != nil; td = td.NextSibling {
			t, isEl := htmlTag(td)
			if !isEl || (t != "td" && t != "th") {
				continue
			}
			if t != "th" {
				th = false
			}
			for c < len(taken[r]) && taken[r][c] {
				c++
			}
			colspan := 1
			if v, ok := htmlAttr(td, "colspan"); ok {
				if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
					colspan = min(max(n, 1), 64)
				}
			}
			rowspan := 1
			if v, ok := htmlAttr(td, "rowspan"); ok {
				if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
					rowspan = min(max(n, 1), len(trs)-r)
				}
			}
			for rr := r; rr < r+rowspan; rr++ {
				for len(rows) <= rr {
					rows = append(rows, nil)
					taken = append(taken, nil)
				}
				for len(taken[rr]) < c+colspan {
					taken[rr] = append(taken[rr], false)
				}
				for len(rows[rr]) < c+colspan {
					rows[rr] = append(rows[rr], CoveredCell())
				}
				for cc := c; cc < c+colspan; cc++ {
					taken[rr][cc] = true
				}
			}
			var blocks []Block
			cellCtx := htmlCtx{}
			if a, ok := htmlAlignOf(td, htmlStyle(td)); ok {
				cellCtx.align, cellCtx.hasAlign = a, true
			} else if ctx.hasAlign {
				cellCtx.align, cellCtx.hasAlign = ctx.align, true
			}
			htmlContainer(td, &cellCtx, &blocks)
			// 单元格不放嵌套表。
			kept := blocks[:0]
			for _, b := range blocks {
				if _, isTable := b.Kind.(Table); !isTable {
					kept = append(kept, b)
				}
			}
			blocks = kept
			if len(blocks) == 0 {
				blocks = append(blocks, BlockText(""))
			}
			rows[r][c] = TableCell{Blocks: blocks, Colspan: uint32(colspan), Rowspan: uint32(rowspan)}
			c += colspan
		}
		allTh = append(allTh, th && c > 0)
	}
	ncols := 0
	for _, row := range rows {
		ncols = max(ncols, len(row))
	}
	if ncols == 0 {
		return Block{}, false
	}
	for r := range rows {
		for len(rows[r]) < ncols {
			rows[r] = append(rows[r], CoveredCell())
		}
		for c := range rows[r] {
			if rows[r][c].IsCovered() && !(len(taken[r]) > c && taken[r][c]) {
				rows[r][c] = EmptyCell()
			}
		}
	}
	head := 0
	for _, h := range trHead {
		if !h {
			break
		}
		head++
	}
	headerRows := head
	if head == 0 && len(allTh) > 0 && allTh[0] {
		headerRows = 1
	}
	tb := Table{ColWidths: htmlColWidths(n, trs, ncols), HeaderRows: headerRows, Rows: rows}
	tb.Repair()
	return NewBlock(tb, RichText{}), true
}
