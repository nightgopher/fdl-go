package fdl

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// 导出为 Markdown（下载为 → Markdown）：DocxXML 文档模型 →
// CommonMark/GFM 文本。对拍上游 markdown.rs。

// ExportOptions 是导出 Markdown 的设置。
type ExportOptions struct {
	// LibraryRoot 是文档库根目录（解析本地图片用）；空串用 RootDir()。
	LibraryRoot string
	// SubPages 是子页面的标题（子页面列表显示的内容）。
	SubPages []string
	// Assets 是图片复制目标文件夹（相对 .md 文件），本地图片登记到
	// Files；空串表示仅文本（不含图片和附件）。
	Assets string
}

// ExportFile 是一条本地文件复制清单：源路径 + 相对 .md 文件的路径。
type ExportFile struct {
	Src string
	Rel string
}

// Export 是导出结果。
type Export struct {
	// Missing 是存在但导不出（库外、越界、缺失）的本地资源数。
	Missing int
	Text    string
	Files   []ExportFile
}

// ToMarkdown 按默认设置（图片复制到 assets/）导出纯 Markdown 文本。
func ToMarkdown(doc *Document) string {
	return ExportMarkdown(doc, &ExportOptions{Assets: "assets"}).Text
}

// ExportMarkdown 把文档导出为 Markdown。
func ExportMarkdown(doc *Document, opts *ExportOptions) Export {
	w := &mdWriter{opts: opts}
	var out strings.Builder
	if title := strings.TrimSpace(doc.Title.Text); title != "" {
		out.WriteString("# " + mdEscape(title) + "\n\n")
	}
	out.WriteString(w.blocks(doc.Blocks))
	return Export{
		Missing: w.missing,
		Text:    strings.TrimRightFunc(out.String(), unicode.IsSpace) + "\n",
		Files:   w.files,
	}
}

type mdWriter struct {
	opts    *ExportOptions
	files   []ExportFile
	missing int
}

// blocks 块之间空一行；相邻列表项保持紧凑。
func (w *mdWriter) blocks(bs []Block) string {
	var out strings.Builder
	prevList := false
	var seq uint32
	for i := range bs {
		b := &bs[i]
		list := IsList(b.Kind)
		if o, ok := b.Kind.(Ordered); ok {
			switch {
			case o.Seq != nil:
				seq = *o.Seq
			case prevList && seq > 0:
				seq++
			default:
				seq = 1
			}
		} else {
			seq = 0
		}
		s := w.block(b, seq)
		if s == "" && !list {
			continue
		}
		if out.Len() > 0 {
			if list && prevList {
				out.WriteByte('\n')
			} else {
				out.WriteString("\n\n")
			}
		}
		out.WriteString(s)
		prevList = list
	}
	return out.String()
}

func (w *mdWriter) block(b *Block, seq uint32) string {
	// 先算行内内容（对文件型行内对象有登记复制清单的副作用）。
	text := mdInline(&b.Text, w.fileLink)
	nested := func(head string) string {
		if len(b.Children) == 0 {
			return head
		}
		return head + "\n" + mdIndent(w.blocks(b.Children), "    ")
	}
	switch k := b.Kind.(type) {
	case Text:
		return nested(text)
	case Heading:
		level := int(k.Level)
		if level < 1 {
			level = 1
		}
		if level > 6 {
			level = 6
		}
		return nested(strings.Repeat("#", level) + " " + text)
	case Bullet:
		return nested("- " + text)
	case Ordered:
		return nested(strconv.FormatUint(uint64(seq), 10) + ". " + text)
	case Todo:
		box := " "
		if k.Done {
			box = "x"
		}
		return nested("- [" + box + "] " + text)
	case Code:
		lang := ""
		if !strings.EqualFold(k.Lang, "Plain Text") {
			lang = strings.ReplaceAll(strings.ToLower(k.Lang), " ", "")
		}
		fence := "```"
		if strings.Contains(b.Text.Text, "```") {
			fence = "````"
		}
		return fence + lang + "\n" + b.Text.Text + "\n" + fence
	case Quote:
		return mdIndent(w.blocks(b.Children), "> ")
	case Callout:
		return mdIndent(k.Emoji+" "+w.blocks(b.Children), "> ")
	case Divider:
		return "---"
	case Image:
		if w.opts.Assets == "" {
			return ""
		}
		alt := ""
		if k.Caption != nil {
			alt = *k.Caption
		}
		return "![" + mdEscape(alt) + "](" + strings.ReplaceAll(w.image(k.Src, w.opts.Assets), " ", "%20") + ")"
	case Table:
		return mdTable(&k, w.fileLink)
	case Grid, Column:
		return w.blocks(b.Children)
	case Countdown:
		return "" // 插件块：Markdown 没有对应物。
	case FileBlock:
		if url, ok := w.fileLink(k); ok {
			return "[" + mdEscape(k.Name) + "](" + url + ")"
		}
		return mdEscape(k.Name)
	case SubPageList:
		parts := make([]string, len(w.opts.SubPages))
		for i, t := range w.opts.SubPages {
			parts[i] = "- " + mdEscape(DisplayTitle(t))
		}
		return strings.Join(parts, "\n")
	case Raw:
		text, link := RawSummary(k.XML)
		if link != nil && LinkOK(*link) {
			if text == "" {
				text = *link
			}
			return "[" + mdEscape(text) + "](" + strings.ReplaceAll(*link, " ", "%20") + ")"
		}
		return strings.ReplaceAll(mdEscape(text), "\n", "<br>")
	}
	return ""
}

// fileLink 是附件的链接；本地附件登记复制并做 URL 转义。
func (w *mdWriter) fileLink(f FileBlock) (string, bool) {
	dir := w.opts.Assets
	if dir == "" || f.SrcKind != "path" {
		return "", false
	}
	link := w.image(f.Src, dir)
	if link == "" {
		return "", false
	}
	link = strings.ReplaceAll(link, " ", "%20")
	link = strings.ReplaceAll(link, ")", "%29")
	link = strings.ReplaceAll(link, "(", "%28")
	return link, true
}

// image 给出图片的链接：远程保持远程，本地文件复制进 assets 文件夹
// （同名，冲突时编号）。
func (w *mdWriter) image(src, dir string) string {
	if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
		return src
	}
	root := w.opts.LibraryRoot
	if root == "" {
		root = RootDir()
	}
	path, ok := ExportPathIn(root, src)
	if !ok {
		w.missing++
		return ""
	}
	for _, f := range w.files {
		if f.Src == path {
			return f.Rel
		}
	}
	stem, ext := fileStemExt(path)
	rel := dir + "/" + stem + ext
	for n := 1; ; n++ {
		clash := false
		for _, f := range w.files {
			if f.Rel == rel {
				clash = true
				break
			}
		}
		if !clash {
			break
		}
		rel = dir + "/" + stem + "-" + strconv.Itoa(n) + ext
	}
	w.files = append(w.files, ExportFile{Src: path, Rel: rel})
	return rel
}

// fileStemExt 拆出文件主干和带点的扩展名（同 Rust file_stem/extension，
// 隐藏文件没有扩展名）。
func fileStemExt(p string) (string, string) {
	base := p[strings.LastIndexByte(p, '/')+1:]
	if i := strings.LastIndexByte(base, '.'); i > 0 {
		return base[:i], base[i:]
	}
	return base, ""
}

// mdTable 把表格写成 GFM 管道表；单元格里的多段用 <br> 连接。
func mdTable(t *Table, files func(FileBlock) (string, bool)) string {
	cols := 1
	for _, r := range t.Rows {
		cols = max(cols, len(r))
	}
	cell := func(c *TableCell) string {
		var parts []string
		for i := range c.Blocks {
			if s := mdInline(&c.Blocks[i].Text, files); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.ReplaceAll(strings.Join(parts, "<br>"), "|", `\|`)
	}
	row := func(r []TableCell) string {
		cells := make([]string, len(r))
		for i := range r {
			cells[i] = cell(&r[i])
		}
		for len(cells) < cols {
			cells = append(cells, "")
		}
		return "| " + strings.Join(cells, " | ") + " |"
	}
	var lines []string
	if len(t.Rows) > 0 {
		lines = append(lines, row(t.Rows[0]))
	} else {
		lines = append(lines, row(nil))
	}
	lines = append(lines, "|"+strings.Repeat(" --- |", cols))
	for _, r := range t.Rows[1:] {
		lines = append(lines, row(r))
	}
	return strings.Join(lines, "\n")
}

// mdIndent 给每行加前缀并去掉行尾空白（同 Rust lines() 语义：吃掉一
// 个行尾换行、行尾 \r）。
func mdIndent(s, prefix string) string {
	s = strings.TrimSuffix(s, "\n")
	blankPrefix := strings.TrimSpace(prefix) == ""
	ls := strings.Split(s, "\n")
	out := make([]string, len(ls))
	for i, l := range ls {
		l = strings.TrimSuffix(l, "\r")
		if l == "" && blankPrefix {
			out[i] = ""
		} else {
			out[i] = strings.TrimRightFunc(prefix+l, unicode.IsSpace)
		}
	}
	return strings.Join(out, "\n")
}

// mdEscape 转义 Markdown 行内特殊字符。
func mdEscape(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, c := range s {
		switch c {
		case '\\', '*', '_', '`', '[', ']', '<', '~':
			b.WriteByte('\\')
		}
		b.WriteRune(c)
	}
	return b.String()
}

// LinkOK 报告 h 是否是可写的链接协议（对拍 paste.rs）。
func LinkOK(h string) bool {
	l := strings.ToLower(h)
	for _, p := range []string{"http://", "https://", "mailto:", "ftp://"} {
		if strings.HasPrefix(l, p) {
			return true
		}
	}
	return false
}

// mdStyle 是 Markdown 能表达的行内样式（颜色等表达不了，合并掉）。
type mdStyle struct {
	bold      bool
	italic    bool
	strike    bool
	underline bool
	code      bool
	link      string
	hasLink   bool
}

func mdInline(t *RichText, files func(FileBlock) (string, bool)) string {
	type mdRun struct {
		md   mdStyle
		text string
	}
	var runs []mdRun
	for _, sp := range t.Spans() {
		seg := t.Text[sp.Start:sp.End]
		var s string
		switch {
		case sp.Marks.Inline != nil && isEquation(sp.Marks.Inline):
			src, _ := sp.Marks.Inline.Equation()
			s = strings.Repeat("$"+src+"$", utf8.RuneCountInString(seg))
		case sp.Marks.Inline != nil:
			if f, ok := sp.Marks.Inline.Kind.(InlineFile); ok {
				s = strings.Repeat(f.File.Name, utf8.RuneCountInString(seg))
			} else {
				// 打开超链接按钮导出成链接，其余按其文字。
				text, link := RawSummary(sp.Marks.Inline.XML)
				if _, isBtn := sp.Marks.Inline.Kind.(Button); isBtn && link != nil && LinkOK(*link) {
					s = "[" + text + "](" + *link + ")"
				} else {
					s = text
				}
				s = strings.Repeat(s, utf8.RuneCountInString(seg))
			}
		default:
			s = seg
		}
		md := mdStyle{
			bold:      sp.Marks.Bold,
			italic:    sp.Marks.Italic,
			strike:    sp.Marks.Strike,
			underline: sp.Marks.Underline,
			code:      sp.Marks.Code,
		}
		if sp.Marks.Inline != nil {
			if f, ok := sp.Marks.Inline.Kind.(InlineFile); ok {
				if u, ok2 := files(f.File); ok2 {
					md.link, md.hasLink = u, true
				}
			}
		}
		if !md.hasLink && sp.Marks.Link != nil && LinkOK(*sp.Marks.Link) {
			md.link, md.hasLink = *sp.Marks.Link, true
		}
		if n := len(runs); n > 0 && runs[n-1].md == md {
			runs[n-1].text += s
		} else {
			runs = append(runs, mdRun{md: md, text: s})
		}
	}
	var out strings.Builder
	for _, r := range runs {
		s := r.text
		if s == "" {
			continue
		}
		// 强调记号必须贴住文字：外侧空白留在强调外面。
		body := strings.TrimSpace(s)
		if body == "" {
			out.WriteString(s)
			continue
		}
		lead := s[:len(s)-len(strings.TrimLeftFunc(s, unicode.IsSpace))]
		trail := s[len(strings.TrimRightFunc(s, unicode.IsSpace)):]
		var x string
		if r.md.code {
			tick := "`"
			if strings.Contains(body, "`") {
				tick = "``"
			}
			x = tick + body + tick
		} else {
			x = mdEscape(body)
		}
		if r.md.underline {
			x = "<u>" + x + "</u>"
		}
		if r.md.strike {
			x = "~~" + x + "~~"
		}
		if r.md.italic {
			x = "*" + x + "*"
		}
		if r.md.bold {
			x = "**" + x + "**"
		}
		if r.md.hasLink {
			url := strings.ReplaceAll(r.md.link, " ", "%20")
			url = strings.ReplaceAll(url, ")", "%29")
			x = "[" + x + "](" + url + ")"
		}
		out.WriteString(lead)
		out.WriteString(x)
		out.WriteString(trail)
	}
	return strings.ReplaceAll(out.String(), "\n", "<br>")
}

func isEquation(in *Inline) bool {
	_, ok := in.Kind.(Equation)
	return ok
}
