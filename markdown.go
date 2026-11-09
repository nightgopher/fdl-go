package fdl

import (
	"os"
	"path/filepath"
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
	// InPlace 把 Markdown 文件按自身写回：无标题行，图片和附件链接保
	// 留源（Base 内的写成相对路径）。
	InPlace bool
	// Base 是库外 .md 文件的所在目录；空串表示库内文件。
	Base string
	// Name 是库外文件的文件名（无扩展名）。文件用到的库内图片附件复
	// 制到它旁边的 `<name>.assets/`。
	Name string
	// Copied 是已做过的复制：库内文件 → 相对 .md 文件的路径。
	Copied map[string]string
	// Original 是磁盘上的原文：没改的块按原样写回（含块间空行）。
	Original string
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
	if title := strings.TrimSpace(doc.Title.Text); title != "" && !opts.InPlace {
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

// blocks 块之间空一行；同类相邻列表项保持紧凑。
func (w *mdWriter) blocks(bs []Block) string {
	var out strings.Builder
	for _, p := range w.pieces(bs) {
		out.WriteString(p.sep)
		out.WriteString(p.text)
	}
	return out.String()
}

// mdPiece 是一个顶层块写出的文字，和它前面的分隔。
type mdPiece struct {
	sep  string
	text string
}

// pieces 是 blocks 的一块一段（空块跳过）。
func (w *mdWriter) pieces(bs []Block) []mdPiece {
	var out []mdPiece
	prevList := false
	var prev BlockKind
	var seq uint32
	for i := range bs {
		b := &bs[i]
		list := IsList(b.Kind)
		// 不同类的列表是两个列表；自带编号的项也另起一个（紧凑写回
		// 会被读成承接上一项）。
		sameKind := prev != nil && kindTag(prev) == kindTag(b.Kind)
		if o, isOrdered := b.Kind.(Ordered); isOrdered && o.Seq != nil {
			sameKind = false
		}
		prev = b.Kind
		if o, isOrdered := b.Kind.(Ordered); isOrdered {
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
		sep := "\n\n"
		if len(out) == 0 {
			sep = ""
		} else if list && prevList && sameKind {
			sep = "\n"
		}
		out = append(out, mdPiece{sep: sep, text: s})
		prevList = list
	}
	return out
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
		if w.opts.InPlace {
			alt := ""
			if k.Caption != nil {
				alt = *k.Caption
			}
			return "![" + mdEscape(alt) + "](" + linkTarget(w.keep(k.Src, true)) + ")"
		}
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
	if w.opts.InPlace {
		if f.Src != "" {
			return linkTarget(w.keep(f.Src, false)), true
		}
		return "", false
	}
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

// ---------------------------------------------------------------- .md 原位编辑

// linkTarget 是能活过解析的链接目标：空格和括号转义。
func linkTarget(s string) string {
	s = strings.ReplaceAll(s, " ", "%20")
	s = strings.ReplaceAll(s, "(", "%28")
	return strings.ReplaceAll(s, ")", "%29")
}

// keep 是写文件时该写的源：Base 内的写成相对；库内资产（assets/…）在
// 库内保持相对，库外变成本机文件旁的一份拷贝。
func (w *mdWriter) keep(src string, image bool) string {
	if (strings.Contains(src, "://") && !strings.HasPrefix(src, "file://")) || strings.HasPrefix(src, "data:") {
		return src
	}
	if w.opts.Base == "" {
		return src
	}
	raw := strings.TrimPrefix(src, "file://")
	root := w.opts.LibraryRoot
	if root == "" {
		root = RootDir()
	}
	abs := raw
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, raw)
	}
	if rel, err := filepath.Rel(w.opts.Base, abs); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(rel)
	}
	// 库内自己的图片或附件：文件旁的一份拷贝。
	assets := filepath.Join(root, "assets")
	if w.opts.Name != "" && withinDir(assets, abs) && isRegularFile(abs) {
		return w.beside(abs, w.opts.Base, w.opts.Name, image)
	}
	return abs
}

// beside 是库内文件落到本机文件旁的位置：图片是 `<name>.assets/<name>-<n>.<ext>`
// （粘贴的图以 uuid 命名），附件用自己的名字；不覆盖已有文件。
func (w *mdWriter) beside(src, base, name string, image bool) string {
	if rel, ok := w.opts.Copied[src]; ok {
		return rel
	}
	for _, f := range w.files {
		if f.Src == src {
			return f.Rel
		}
	}
	dir := strings.ReplaceAll(name, "/", "_") + ".assets"
	base_ := filepath.Base(src)
	ext := ""
	stem := base_
	if i := strings.LastIndexByte(base_, '.'); i > 0 {
		ext, stem = base_[i:], base_[:i]
	}
	hexDash := func(s string) bool {
		for i := 0; i < len(s); i++ {
			c := s[i]
			if !(c == '-' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return false
			}
		}
		return true
	}
	// 库内图片是粘贴的 `<uuid>`（无名），或来自文件的 `<8位hex>-<名字>`。
	own := ""
	hasOwn := false
	if len(stem) >= 9 && stem[8] == '-' && hexDash(stem[:9][:8]) && stem[9:] != "" && !(len(stem) == 36 && hexDash(stem)) {
		own, hasOwn = stem[9:], true
	} else if !image {
		own, hasOwn = stem, true
	}
	taken := func(rel string) bool {
		if _, err := os.Stat(filepath.Join(base, rel)); err == nil {
			return true
		}
		for _, f := range w.files {
			if f.Rel == rel {
				return true
			}
		}
		return false
	}
	for n := 1; ; n++ {
		var file string
		switch {
		case !hasOwn:
			file = name + "-" + strconv.Itoa(n) + ext
		case n == 1:
			file = own + ext
		default:
			file = own + "-" + strconv.Itoa(n) + ext
		}
		rel := dir + "/" + file
		if !taken(rel) {
			w.files = append(w.files, ExportFile{Src: src, Rel: rel})
			return rel
		}
	}
}

// FrontMatter 切出开头的 YAML front matter 块（--- … ---），原样保留、
// 原样写回。
func FrontMatter(text string) (string, string) {
	rest, ok := strings.CutPrefix(text, "---\n")
	if !ok {
		if r2, ok2 := strings.CutPrefix(text, "---\r\n"); ok2 {
			rest, ok = r2, true
		}
	}
	if !ok {
		return "", text
	}
	at := len(text) - len(rest)
	for _, line := range splitInclusive(rest, '\n') {
		at += len(line)
		if strings.TrimRightFunc(line, unicode.IsSpace) == "---" {
			tail := text[at:]
			blank := len(tail) - len(strings.TrimLeft(tail, "\n\r"))
			return text[:at+blank], text[at+blank:]
		}
	}
	return "", text
}

// splitInclusive 按 sep 切分，每段含自己的分隔符（最后一段可能没有）。
func splitInclusive(s string, sep byte) []string {
	var out []string
	for {
		i := strings.IndexByte(s, sep)
		if i < 0 {
			if s != "" {
				out = append(out, s)
			}
			return out
		}
		out = append(out, s[:i+1])
		s = s[i+1:]
	}
}

// MarkdownPercentDecode 解码链接里的 %20 之类。
func MarkdownPercentDecode(s string) string {
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

// ResolveImages 把相对和 file:// 的图片源解析成 Base 下的绝对路径。
func ResolveImages(blocks []Block, base string) {
	for i := range blocks {
		b := &blocks[i]
		ResolveImages(b.Children, base)
		if tb, ok := b.Kind.(Table); ok {
			for r := range tb.Rows {
				for c := range tb.Rows[r] {
					ResolveImages(tb.Rows[r][c].Blocks, base)
				}
			}
		}
		img, ok := b.Kind.(Image)
		if !ok {
			continue
		}
		src := img.Src
		if (strings.Contains(src, "://") && !strings.HasPrefix(src, "file://")) || strings.HasPrefix(src, "data:") {
			continue
		}
		var path string
		if p, isFile := strings.CutPrefix(src, "file://"); isFile {
			path = MarkdownPercentDecode(p)
		} else {
			dec := MarkdownPercentDecode(src)
			if filepath.IsAbs(dec) {
				path = dec
			} else {
				path = filepath.Join(base, dec)
			}
		}
		img.Src = path
		b.Kind = img
	}
}

func isRegularFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// ParseFile 把 Markdown 文件读成一页：title 是文件名（Markdown 文件没
// 有自己的标题），正文是 front matter 之后的每一行。base 是库外文件
// 的所在目录，相对图片按它解析。
func ParseFile(text, title, base string) Document {
	text = strings.TrimPrefix(text, "\ufeff")
	_, body := FrontMatter(text)
	blocks := MarkdownToBlocks(body, Off{File: true, Alt: true})
	if base != "" {
		ResolveImages(blocks, base)
	}
	if len(blocks) == 0 {
		blocks = append(blocks, BlockText(""))
	}
	return Document{Title: Plain(title), Blocks: blocks}
}

// WriteFile 把 doc 写回 Markdown 文件，跟在文件的 front 之后。
func WriteFile(doc *Document, front, base string) string {
	return WriteFileWith(doc, front, &ExportOptions{InPlace: true, Base: base}).Text
}

// WriteFileWith 是 WriteFile，并带回要在本机文件旁做的拷贝（Files，相
// 对文件所在目录）。
func WriteFileWith(doc *Document, front string, opts *ExportOptions) Export {
	o := ExportOptions{
		InPlace:     true,
		LibraryRoot: opts.LibraryRoot,
		Base:        opts.Base,
		Name:        opts.Name,
		Copied:      opts.Copied,
		Original:    opts.Original,
	}
	w := &mdWriter{opts: &o}
	ps := w.pieces(doc.Blocks)
	canonical := mdJoined(ps)
	body := canonical
	if o.Original != "" {
		for _, strict := range []bool{false, true} {
			if b := mdPreserve(o.Original, ps, &o, strict); mdReadsAs(b, canonical, &o) {
				body = b
				break
			}
		}
	}
	// 文件以 `---` 行开头会被读成 front matter。
	if fm, _ := FrontMatter(body); front == "" && fm != "" {
		if rest, ok := strings.CutPrefix(body, "---\n"); ok {
			body = "***\n" + rest
		}
	}
	text := front
	if strings.TrimSpace(body) != "" {
		text = front + body
	}
	return Export{Text: text, Files: w.files}
}

// mdJoined 是各块按写出形态直接拼接，以一个换行结尾。
func mdJoined(pieces []mdPiece) string {
	var sb strings.Builder
	for _, p := range pieces {
		sb.WriteString(p.sep)
		sb.WriteString(p.text)
	}
	return strings.TrimRightFunc(sb.String(), unicode.IsSpace) + "\n"
}

// mdReadsAs 报告 body 读回后再写出是否就是 canonical。
func mdReadsAs(body, canonical string, opts *ExportOptions) bool {
	blocks := MarkdownToBlocks(body, Off{File: true, Alt: true})
	if opts.Base != "" {
		ResolveImages(blocks, opts.Base)
	}
	w := &mdWriter{opts: opts}
	return mdJoined(w.pieces(blocks)) == canonical
}

// mdPreserve 把原文（整个文件）的正文改写成 pieces：与文件中某块写出
// 文字相同的块按它原来的行原样保留；两个保留块之间文件自己的空行也保
// 留。只有改过的块用写出形态。
func mdPreserve(original string, pieces []mdPiece, opts *ExportOptions, strict bool) string {
	_, body := FrontMatter(strings.TrimPrefix(original, "\ufeff"))
	crlf := strings.Contains(body, "\r\n")
	norm := strings.ReplaceAll(body, "\r\n", "\n")
	blocks, spans := MarkdownToBlocksWithLines(norm, Off{File: true, Alt: true})
	if len(blocks) != len(spans) {
		return mdJoined(pieces)
	}
	if opts.Base != "" {
		ResolveImages(blocks, opts.Base)
	}
	// 文件里的块按现在写出，各带它来自的行。
	type oldItem struct {
		text   string
		lo, hi int
	}
	var old []oldItem
	{
		w := &mdWriter{opts: opts}
		prevList := false
		var seq uint32
		for i := range blocks {
			b := &blocks[i]
			list := IsList(b.Kind)
			if o, isOrdered := b.Kind.(Ordered); isOrdered {
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
			old = append(old, oldItem{text: s, lo: spans[i][0], hi: spans[i][1]})
			prevList = list
		}
	}
	lines := strings.Split(norm, "\n")
	textOf := func(a, b int) string { return strings.Join(lines[a:b], "\n") }
	gap := func(a, b int) string {
		var sb strings.Builder
		sb.WriteByte('\n')
		for _, l := range lines[a:b] {
			sb.WriteString(l)
			sb.WriteByte('\n')
		}
		return sb.String()
	}
	oldTexts := make([]string, len(old))
	for i := range old {
		oldTexts[i] = old[i].text
	}
	newTexts := make([]string, len(pieces))
	for i := range pieces {
		newTexts[i] = pieces[i].text
	}
	matched := matchPieces(oldTexts, newTexts)
	used := map[int]bool{}
	for _, m := range matched {
		if m != nil {
			used[*m] = true
		}
	}
	// 每个块在文件里的位置：它自己，或（改过的）它替换掉的块（下一个
	// 未匹配的），改动周围的空行也保持文件里的样子。
	at := make([]*int, len(pieces))
	for j := range pieces {
		if matched[j] != nil {
			at[j] = matched[j]
			continue
		}
		next := 0
		if j > 0 {
			if at[j-1] == nil {
				continue
			}
			next = *at[j-1] + 1
		}
		if next < len(old) && !used[next] {
			v := next
			at[j] = &v
		}
	}
	var out strings.Builder
	for j, p := range pieces {
		m := matched[j]
		switch {
		case j == 0 && at[0] != nil && *at[0] == 0:
			for _, l := range lines[:old[0].lo] {
				out.WriteString(l)
				out.WriteByte('\n')
			}
		case j == 0:
		case at[j] != nil && *at[j] > 0 && at[j-1] != nil && *at[j-1] == *at[j]-1:
			// 文件的空行，前提是含义不变：写出的紧凑处保持紧凑（一个
			// 换行），宽松处保持宽松（至少一个空行）。否则 `- a` 和新
			// 段落 `b` 会连起来，或一个列表被读成两个。
			g := gap(old[*at[j]-1].hi, old[*at[j]].lo)
			gLines := strings.Count(g, "\n")
			tight := p.sep == "\n"
			edited := m == nil || matched[j-1] == nil
			keep := (gLines == 1) == tight
			if !strict {
				keep = !edited || (!tight && gLines >= 2)
			}
			if keep {
				out.WriteString(g)
			} else {
				out.WriteString(p.sep)
			}
		default:
			out.WriteString(p.sep)
		}
		if m != nil {
			out.WriteString(textOf(old[*m].lo, old[*m].hi))
		} else {
			out.WriteString(p.text)
		}
	}
	// 文件的最后一块仍是最后时，保留它后面的结尾。
	var lastAt *int
	if len(at) > 0 {
		lastAt = at[len(at)-1]
	}
	if lastAt != nil && len(old) > 0 && *lastAt+1 == len(old) {
		tail := strings.Join(lines[old[len(old)-1].hi:], "\n")
		out.WriteByte('\n')
		out.WriteString(tail)
		if !strings.HasSuffix(out.String(), "\n") {
			out.WriteByte('\n')
		}
	} else if out.Len() > 0 {
		trimmed := strings.TrimRightFunc(out.String(), unicode.IsSpace)
		out.Reset()
		out.WriteString(trimmed)
		out.WriteByte('\n')
	}
	res := out.String()
	if crlf {
		res = strings.ReplaceAll(res, "\n", "\r\n")
	}
	return res
}

// matchPieces 给每个新块找它对应的旧块（文字相等的最长公共子序列）。
// 先匹配 unchanged 的头尾，普通编辑只剩小中间；中间太大就不匹配。
func matchPieces(oldS, newS []string) []*int {
	out := make([]*int, len(newS))
	a := 0
	for a < len(oldS) && a < len(newS) && oldS[a] == newS[a] {
		v := a
		out[a] = &v
		a++
	}
	eo, en := len(oldS), len(newS)
	for eo > a && en > a && oldS[eo-1] == newS[en-1] {
		eo--
		en--
		v := eo
		out[en] = &v
	}
	o, n := oldS[a:eo], newS[a:en]
	if len(o) == 0 || len(n) == 0 || len(o)*len(n) > 4_000_000 {
		return out
	}
	// lcs[i][j] 是 o[i:] 与 n[j:] 的最长公共子序列长度。
	w := len(n) + 1
	lcs := make([]uint32, (len(o)+1)*w)
	for i := len(o) - 1; i >= 0; i-- {
		for j := len(n) - 1; j >= 0; j-- {
			if o[i] == n[j] {
				lcs[i*w+j] = lcs[(i+1)*w+j+1] + 1
			} else {
				lcs[i*w+j] = max(lcs[(i+1)*w+j], lcs[i*w+j+1])
			}
		}
	}
	i, j := 0, 0
	for i < len(o) && j < len(n) {
		switch {
		case o[i] == n[j]:
			v := a + i
			out[a+j] = &v
			i++
			j++
		case lcs[(i+1)*w+j] >= lcs[i*w+j+1]:
			i++
		default:
			j++
		}
	}
	return out
}
