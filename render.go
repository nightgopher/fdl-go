package fdl

// 块模型 → HTML（本地 Web 客户端用）。只做展示：公式显示源码，折叠展
// 开，编辑在 Markdown 层。

import (
	"fmt"
	"html"
	"strconv"
	"strings"
)

// RenderHTML 把文档渲染成正文 HTML（不含标题；标题由页面给 <h1>）。
func RenderHTML(doc *Document) string {
	var b htmlBuilder
	b.blocks(doc.Blocks, 0)
	return b.String()
}

type htmlBuilder struct{ b strings.Builder }

func (h *htmlBuilder) String() string { return h.b.String() }

func (h *htmlBuilder) esc(s string) string { return html.EscapeString(s) }

func (h *htmlBuilder) blocks(blocks []Block, depth int) {
	i := 0
	for i < len(blocks) {
		if _, isBullet := blocks[i].Kind.(Bullet); isBullet {
			i = h.list(blocks, i, false)
			continue
		}
		if _, isOrdered := blocks[i].Kind.(Ordered); isOrdered {
			i = h.list(blocks, i, true)
			continue
		}
		h.block(&blocks[i], depth)
		i++
	}
}

// list 渲染从 i 开始的同族列表，返回下一块下标。
func (h *htmlBuilder) list(blocks []Block, i int, ordered bool) int {
	tag := "ul"
	if ordered {
		tag = "ol"
	}
	h.b.WriteString("<" + tag + ">")
	for i < len(blocks) {
		_, isB := blocks[i].Kind.(Bullet)
		_, isO := blocks[i].Kind.(Ordered)
		if !(isB == !ordered && (isB || isO)) {
			break
		}
		b := &blocks[i]
		h.b.WriteString("<li>")
		h.inline(&b.Text)
		h.children(b.Children)
		h.b.WriteString("</li>")
		i++
	}
	h.b.WriteString("</" + tag + ">")
	return i
}

func (h *htmlBuilder) block(b *Block, depth int) {
	align := ""
	switch b.Align {
	case AlignCenter:
		align = ` style="text-align:center"`
	case AlignRight:
		align = ` style="text-align:right"`
	}
	switch k := b.Kind.(type) {
	case Text:
		h.b.WriteString("<p" + align + ">")
		h.inline(&b.Text)
		h.b.WriteString("</p>")
		h.children(b.Children)
	case Heading:
		level := k.Level
		if level < 1 {
			level = 1
		}
		if level > 6 {
			level = 6
		}
		h.b.WriteString("<h" + strconv.Itoa(int(level)) + align + ">")
		h.inline(&b.Text)
		h.b.WriteString("</h" + strconv.Itoa(int(level)) + ">")
		h.children(b.Children)
	case Todo:
		checked := ""
		if k.Done {
			checked = " checked"
		}
		h.b.WriteString(`<div class="todo` + func() string {
			if k.Done {
				return " done"
			}
			return ""
		}() + `"><input type="checkbox" disabled` + checked + ">")
		h.inline(&b.Text)
		h.b.WriteString("</div>")
		h.children(b.Children)
	case Code:
		lang := LangToToken(k.Lang)
		h.b.WriteString(`<pre class="code" data-lang="` + h.esc(lang) + `"><code>`)
		h.b.WriteString(h.esc(b.Text.Text))
		h.b.WriteString("</code></pre>")
	case Quote:
		h.b.WriteString("<blockquote>")
		h.blocks(b.Children, depth+1)
		h.b.WriteString("</blockquote>")
	case Callout:
		style := ""
		if k.Bg != nil {
			style += "background:" + colorValue(string(*k.Bg), true) + ";"
		}
		if k.Border != nil {
			style += "border-color:" + colorValue(string(*k.Border), false) + ";"
		}
		h.b.WriteString(`<div class="callout" style="` + style + `"><span class="emoji">` + h.esc(k.Emoji) + "</span><div>")
		h.blocks(b.Children, depth+1)
		h.b.WriteString("</div></div>")
	case Divider:
		h.b.WriteString("<hr>")
	case Image:
		alt := ""
		if k.Caption != nil {
			alt = *k.Caption
		}
		h.b.WriteString(`<img src="` + h.esc(imageSrcForWeb(k.Src)) + `" alt="` + h.esc(alt) + `" loading="lazy">`)
		if alt != "" {
			h.b.WriteString(`<div class="caption">` + h.esc(alt) + "</div>")
		}
	case Table:
		h.table(&k)
	case Grid:
		h.b.WriteString(`<div class="grid">`)
		for i := range b.Children {
			ratio := float32(0.5)
			if c, ok := b.Children[i].Kind.(Column); ok {
				ratio = c.Ratio
			}
			h.b.WriteString(fmt.Sprintf(`<div class="col" style="flex:0 0 %.1f%%">`, ratio*100))
			h.blocks(b.Children[i].Children, depth+1)
			h.b.WriteString("</div>")
		}
		h.b.WriteString("</div>")
	case Column:
		h.blocks(b.Children, depth+1)
	case SubPageList:
		h.b.WriteString(`<div class="subpages">子页面列表</div>`)
	case Countdown:
		h.b.WriteString(`<div class="countdown">⏳ ` + h.esc(k.Date+" "+k.Time) + "</div>")
	case FileBlock:
		h.b.WriteString(`<div class="file">📎 ` + h.esc(k.Name) + "</div>")
	case Raw:
		text, link := RawSummary(k.XML)
		if link != nil && LinkOK(*link) {
			h.b.WriteString(`<p><a href="` + h.esc(*link) + `" target="_blank">` + h.esc(text) + "</a></p>")
		} else if text != "" {
			h.b.WriteString("<p>" + h.esc(text) + "</p>")
		} else {
			h.b.WriteString(`<pre class="raw">` + h.esc(k.XML) + "</pre>")
		}
	}
}

func (h *htmlBuilder) children(children []Block) {
	if len(children) == 0 {
		return
	}
	h.b.WriteString(`<div class="indent">`)
	h.blocks(children, 1)
	h.b.WriteString("</div>")
}

func (h *htmlBuilder) table(t *Table) {
	h.b.WriteString("<table><colgroup>")
	for _, w := range t.ColWidths {
		h.b.WriteString(fmt.Sprintf("<col style=\"width:%vpx\">", w))
	}
	h.b.WriteString("</colgroup>")
	head := min(t.HeaderRows, len(t.Rows))
	if head > 0 {
		h.b.WriteString("<thead>")
		h.rows(t.Rows[:head], t, true)
		h.b.WriteString("</thead>")
	}
	h.b.WriteString("<tbody>")
	h.rows(t.Rows[head:], t, false)
	h.b.WriteString("</tbody></table>")
}

func (h *htmlBuilder) rows(rows [][]TableCell, t *Table, head bool) {
	for _, row := range rows {
		h.b.WriteString("<tr>")
		for c := range row {
			cell := &row[c]
			if cell.IsCovered() {
				continue
			}
			tag := "td"
			if head || (c == 0 && t.HeaderCol) {
				tag = "th"
			}
			attrs := ""
			if cell.Bg != nil {
				attrs += ` style="background:` + colorValue(string(*cell.Bg), true) + `"`
			}
			if cell.Valign != nil {
				attrs += ` valign="` + h.esc(*cell.Valign) + `"`
			}
			if cell.Colspan > 1 {
				attrs += ` colspan="` + strconv.Itoa(int(cell.Colspan)) + `"`
			}
			if cell.Rowspan > 1 {
				attrs += ` rowspan="` + strconv.Itoa(int(cell.Rowspan)) + `"`
			}
			h.b.WriteString("<" + tag + attrs + ">")
			for i := range cell.Blocks {
				if i > 0 {
					h.b.WriteString("<br>")
				}
				h.b.WriteString(renderInlineHTML(&cell.Blocks[i].Text))
			}
			h.b.WriteString("</" + tag + ">")
		}
		h.b.WriteString("</tr>")
	}
}

// renderInlineHTML 渲染一段行内文本（表格单元格里复用）。
func renderInlineHTML(t *RichText) string {
	var out strings.Builder
	for _, sp := range t.Spans() {
		seg := t.Text[sp.Start:sp.End]
		if obj := sp.Marks.Inline; obj != nil {
			out.WriteString(inlineObjectHTML(obj))
			continue
		}
		s := hEsc(strings.ReplaceAll(seg, "\n", "<br>"))
		if sp.Marks.Code {
			s = "<code>" + s + "</code>"
		}
		if sp.Marks.Bold {
			s = "<b>" + s + "</b>"
		}
		if sp.Marks.Italic {
			s = "<em>" + s + "</em>"
		}
		if sp.Marks.Underline {
			s = "<u>" + s + "</u>"
		}
		if sp.Marks.Strike {
			s = "<del>" + s + "</del>"
		}
		if c := sp.Marks.TextColor; c != nil {
			s = `<span style="color:` + colorValue(string(*c), false) + `">` + s + "</span>"
		}
		if c := sp.Marks.BgColor; c != nil {
			s = `<span style="background:` + colorValue(string(*c), true) + `">` + s + "</span>"
		}
		if l := sp.Marks.Link; l != nil {
			s = `<a href="` + hEsc(*l) + `" target="_blank">` + s + "</a>"
		}
		out.WriteString(s)
	}
	return out.String()
}

func (h *htmlBuilder) inline(t *RichText) { h.b.WriteString(renderInlineHTML(t)) }

// inlineObjectHTML 是行内对象的展示形态。
func inlineObjectHTML(in *Inline) string {
	switch k := in.Kind.(type) {
	case Equation:
		return `<code class="math">` + hEsc("$"+k.Src+"$") + "</code>"
	case InlineFile:
		return `<span class="file-inline">📎 ` + hEsc(k.File.Name) + "</span>"
	case InlineDocLink:
		return `<span class="doclink">@` + hEsc(k.Title) + "</span>"
	case Button:
		return `<span class="button">` + hEsc(k.Text) + "</span>"
	case Reminder:
		return `<span class="reminder">⏰ ` + hEsc(k.Label(" ")) + "</span>"
	}
	text, link := RawSummary(in.XML)
	if link != nil {
		return `<a href="` + hEsc(*link) + `" target="_blank">` + hEsc(text) + "</a>"
	}
	return hEsc(text)
}

func hEsc(s string) string { return html.EscapeString(s) }

// colorValue 把调色板色名转成 CSS 颜色；未知名字原样返回（作为 CSS 颜色
// 或自定义值）。
func colorValue(name string, bg bool) string {
	if bg {
		if c, ok := BGColor(bgCanon(name)); ok {
			return cssColor(c)
		}
		if c, ok := BlockBG(name); ok {
			return cssColor(c)
		}
	} else if c, ok := TextColor(name); ok {
		return cssColor(c)
	}
	return name
}

// bgCanon 把 medium-red 一类读成面板名 red。
func bgCanon(name string) string {
	if rest, ok := strings.CutPrefix(name, "medium-"); ok && rest != "gray" {
		return rest
	}
	return name
}

func cssColor(c Rgba) string {
	if c.A >= 0.99 {
		return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
	}
	return fmt.Sprintf("rgba(%d,%d,%d,%.2f)", c.R, c.G, c.B, c.A)
}

// imageSrcForWeb：远程图片直接用；库内相对路径（assets/…）走本地文件
// 服务。
func imageSrcForWeb(src string) string {
	if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") || strings.HasPrefix(src, "data:") {
		return src
	}
	if p, ok := strings.CutPrefix(src, "file://"); ok {
		if filepathIsAbs(p) {
			return p
		}
	}
	return "/library/" + strings.TrimPrefix(src, "/")
}

func filepathIsAbs(p string) bool { return len(p) > 0 && p[0] == '/' }
