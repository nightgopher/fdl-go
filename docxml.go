package fdl

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------- DOM

// 解析经由一个小 DOM，未知元素才能作为 Raw 块 / 行内对象原样再输出。

type node struct {
	el   *element // 非 nil 时是元素节点
	text string   // el == nil 时的文本
}

type attribute struct {
	key string
	val string
}

type element struct {
	name     string
	attrs    []attribute
	children []node
}

func (e *element) attr(k string) (string, bool) {
	for _, a := range e.attrs {
		if a.key == k {
			return a.val, true
		}
	}
	return "", false
}

func (e *element) hasAttr(k string) bool {
	_, ok := e.attr(k)
	return ok
}

func (e *element) attrOr(k, def string) string {
	if v, ok := e.attr(k); ok {
		return v
	}
	return def
}

func (e *element) attrIs(k, v string) bool {
	x, ok := e.attr(k)
	return ok && x == v
}

// qname 还原带前缀的名字（没有声明命名空间时 Go 会拆成 Space:Local）。
func qname(n xml.Name) string {
	if n.Space != "" {
		return n.Space + ":" + n.Local
	}
	return n.Local
}

func newElement(name xml.Name, attrs []xml.Attr) *element {
	el := &element{name: qname(name)}
	for _, a := range attrs {
		el.attrs = append(el.attrs, attribute{key: qname(a.Name), val: a.Value})
	}
	return el
}

func parseDOM(xmlText string) ([]node, error) {
	dec := xml.NewDecoder(strings.NewReader(xmlText))
	root := &element{name: "#root"}
	stack := []*element{root}
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			stack = append(stack, newElement(t.Name, t.Attr))
		case xml.EndElement:
			if len(stack) < 2 {
				return nil, errors.New("fdl: unbalanced xml")
			}
			el := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			parent := stack[len(stack)-1]
			parent.children = append(parent.children, node{el: el})
		case xml.CharData:
			parent := stack[len(stack)-1]
			parent.children = append(parent.children, node{text: string(t)})
		}
	}
	if len(stack) != 1 {
		return nil, fmt.Errorf("fdl: unclosed element <%s>", stack[len(stack)-1].name)
	}
	return root.children, nil
}

// ---------------------------------------------------------------- 解析

func isBlockTag(name string) bool {
	switch name {
	case "p", "h1", "h2", "h3", "h4", "h5", "h6", "h7", "h8", "h9", "ul", "ol", "li",
		"checkbox", "blockquote", "hr", "pre", "img", "table", "grid", "column",
		"callout", "whiteboard", "source", "figure", "sheet", "task", "chat_card",
		"bookmark", "html5-block", "okr", "sub-page-list", "countdown":
		return true
	}
	return false
}

// isInlineObject：也能单独做顶层元素的行内对象（各自成一段）。
func isInlineObject(name string) bool { return name == "button" }

// Parse 把 DocxXML（飞书/Lark 文档 XML）解析为 Document。
func Parse(xmlText string) (Document, error) {
	nodes, err := parseDOM(xmlText)
	if err != nil {
		return Document{}, err
	}
	doc := NewDocument()
	doc.Blocks = nil
	top := nodes
	// 只解包已知的文档包装元素；未知块保留其属性。
	for {
		var els []*element
		hasText := false
		for i := range top {
			if top[i].el != nil {
				els = append(els, top[i].el)
			} else if strings.TrimSpace(top[i].text) != "" {
				hasText = true
			}
		}
		unwrapped := false
		if len(els) == 1 && !hasText {
			switch els[0].name {
			case "document", "doc", "fragment":
				top = els[0].children
				unwrapped = true
			}
		}
		if !unwrapped {
			break
		}
	}
	for _, n := range top {
		if e := n.el; e != nil && e.name == "title" {
			doc.Title = inlineText(e.children)
			doc.Icon = attrNonEmpty(e, "icon")
			if c, ok := e.attr("cover"); ok && c != "" {
				cov := &Cover{Src: c}
				if v, ok := e.attr("cover-x"); ok {
					if f, err := parseFloat32(v); err == nil {
						cov.X = f
					}
				}
				if v, ok := e.attr("cover-y"); ok {
					if f, err := parseFloat32(v); err == nil {
						cov.Y = f
					}
				}
				doc.Cover = cov
			}
			continue
		}
		parseBlockNode(n, &doc.Blocks)
	}
	if len(doc.Blocks) == 0 {
		doc.Blocks = append(doc.Blocks, BlockText(""))
	}
	return doc, nil
}

func parseAlign(e *element) Align {
	v, _ := e.attr("align")
	switch v {
	case "center":
		return AlignCenter
	case "right":
		return AlignRight
	default:
		return AlignLeft
	}
}

func parseBlockNode(n node, out *[]Block) {
	if n.el == nil {
		// 顶层散落的文本：非空白就保留成一个段落。
		if strings.TrimSpace(n.text) != "" {
			*out = append(*out, BlockText(strings.TrimSpace(n.text)))
		}
		return
	}
	parseBlock(n.el, out)
}

func parseBlocks(children []node) []Block {
	var out []Block
	var pending []node
	flush := func() {
		keep := false
		for _, n := range pending {
			if n.el != nil {
				keep = true
			} else if strings.TrimSpace(n.text) != "" {
				keep = true
			}
		}
		if keep {
			b := NewBlock(Text{}, inlineText(pending))
			trimRich(&b.Text)
			out = append(out, b)
		}
		pending = pending[:0]
	}
	for _, n := range children {
		if e := n.el; e != nil && (isBlockTag(e.name) || e.name == "title") {
			flush()
			parseBlock(e, &out)
		} else {
			pending = append(pending, n)
		}
	}
	flush()
	return out
}

func nonEmptyBlocks(v []Block) []Block {
	if len(v) == 0 {
		return []Block{BlockText("")}
	}
	return v
}

func parseBlock(e *element, out *[]Block) {
	name := e.name
	var block Block
	switch {
	case name == "p":
		// 段落或标题里嵌套的块是它的（缩进）子级。
		inline, nested := splitInlineAndBlocks(e.children)
		block = NewBlock(Text{}, inlineText(inline))
		block.Children = nested
	case name == "h1" || name == "h2" || name == "h3" || name == "h4" ||
		name == "h5" || name == "h6" || name == "h7" || name == "h8" || name == "h9":
		inline, nested := splitInlineAndBlocks(e.children)
		block = NewBlock(Heading{Level: name[1] - '0', SeqAuto: e.attrIs("seq", "auto")}, inlineText(inline))
		block.Children = nested
	case name == "ul" || name == "ol":
		for _, c := range e.children {
			if li := c.el; li != nil {
				if li.name == "li" {
					*out = append(*out, parseLi(li, name == "ol"))
				} else {
					parseBlock(li, out)
				}
			}
		}
		return
	case name == "li":
		*out = append(*out, parseLi(e, false))
		return
	case name == "checkbox":
		inline, nested := splitInlineAndBlocks(e.children)
		block = NewBlock(Todo{Done: e.attrIs("done", "true")}, inlineText(inline))
		block.Children = nested
	case name == "blockquote":
		block = NewBlock(Quote{}, RichText{})
		block.Children = nonEmptyBlocks(parseBlocks(e.children))
	case name == "hr":
		block = NewBlock(Divider{}, RichText{})
	case name == "sub-page-list":
		block = NewBlock(SubPageList{}, RichText{})
	case name == "countdown":
		block = NewBlock(parseCountdown(e), RichText{})
	case name == "pre":
		var codeEl *element
		for _, c := range e.children {
			if cc := c.el; cc != nil && cc.name == "code" {
				codeEl = cc
				break
			}
		}
		src := e.children
		if codeEl != nil {
			src = codeEl.children
		}
		var caption *string
		if v, ok := e.attr("caption"); ok {
			caption = &v
		}
		block = NewBlock(Code{
			Lang:    LangFromToken(e.attrOr("lang", "")),
			Caption: caption,
			Wrap:    e.attrIs("wrap", "true"),
		}, Plain(codeText(src)))
	case name == "img":
		srcKind, src := "src", ""
		for _, k := range []string{"path", "href", "src"} {
			if v, ok := e.attr(k); ok {
				srcKind, src = k, v
				break
			}
		}
		block = NewBlock(Image{
			SrcKind: srcKind,
			Src:     src,
			Width:   attrF32(e, "width"),
			Height:  attrF32(e, "height"),
			Caption: attrNonEmpty(e, "caption"),
			Crop:    parseCropAttr(e),
		}, RichText{})
	case name == "callout":
		k := Callout{Emoji: e.attrOr("emoji", "💡")}
		if v, ok := e.attr("background-color"); ok {
			c := ColorName(v)
			k.Bg = &c
		}
		if v, ok := e.attr("border-color"); ok {
			c := ColorName(v)
			k.Border = &c
		}
		if v, ok := e.attr("text-color"); ok {
			c := ColorName(v)
			k.TextColor = &c
		}
		block = NewBlock(k, RichText{})
		block.Children = nonEmptyBlocks(parseBlocks(e.children))
	case name == "grid":
		var cols []Block
		for _, c := range e.children {
			col := c.el
			if col == nil || col.name != "column" {
				continue
			}
			ratio := float32(0.5)
			if v, ok := col.attr("width-ratio"); ok {
				if f, err := parseFloat32(v); err == nil {
					ratio = f
				}
			}
			cb := NewBlock(Column{Ratio: ratio}, RichText{})
			cb.Children = nonEmptyBlocks(parseBlocks(col.children))
			cols = append(cols, cb)
		}
		block = NewBlock(Grid{}, RichText{})
		block.Children = cols
	case name == "table":
		block = NewBlock(parseTable(e), RichText{})
	case name == "source":
		block = NewBlock(fileOf(e, ViewCard), RichText{})
	case name == "figure" && figureSource(e) != nil:
		view := ViewCard
		if v, ok := e.attr("view-type"); ok && strings.EqualFold(v, "preview") {
			view = ViewPreview
		}
		block = NewBlock(fileOf(figureSource(e), view), RichText{})
	case isInlineObject(name):
		block = NewBlock(Text{}, inlineText([]node{{el: e}}))
	default:
		block = NewBlock(Raw{Tag: e.name, XML: serializeEl(e)}, RichText{})
	}
	block.Align = parseAlign(e)
	// 图片默认居中。
	if name == "img" && !e.hasAttr("align") {
		block.Align = AlignCenter
	}
	block.Folded = e.attrIs("folded", "true")
	block.Bg = parseBg(e, block.Kind)
	*out = append(*out, block)
}

func parseCountdown(e *element) Countdown {
	c := NewCountdown()
	if v, ok := e.attr("duration"); ok {
		if f, err := parseFloat64(v); err == nil {
			c.Duration = f
		}
	}
	if v, ok := e.attr("start-time"); ok {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			c.Start = n
		}
	}
	if v, ok := e.attr("notify"); ok {
		c.Notify = v != "false"
	}
	if v, ok := e.attr("color"); ok {
		for _, k := range CountdownColors {
			if strings.EqualFold(k, v) {
				c.Color = strings.ToUpper(v)
				break
			}
		}
	}
	c.ByDate = e.attrIs("timing", "date")
	c.Dhms = [4]uint32{attrU32Or(e, "days"), attrU32Or(e, "hours"), attrU32Or(e, "minutes"), attrU32Or(e, "seconds")}
	c.Date = e.attrOr("date", "")
	c.Time = e.attrOr("time", "")
	return c
}

func parseLi(li *element, ordered bool) Block {
	inline, nested := splitInlineAndBlocks(li.children)
	text := inlineText(inline)
	// 只含一个 <p> 的 <li> 用该段的文字做项文字。
	if text.IsEmpty() && len(nested) > 0 {
		if _, ok := nested[0].Kind.(Text); ok {
			text = nested[0].Text
			nested = nested[1:]
		}
	}
	var kind BlockKind = Bullet{}
	if ordered {
		var seq *uint32
		if v, ok := li.attr("seq"); ok {
			if n, err := strconv.ParseUint(v, 10, 32); err == nil {
				u := uint32(n)
				seq = &u
			}
		}
		kind = Ordered{Seq: seq}
	}
	b := NewBlock(kind, text)
	b.Align = parseAlign(li)
	b.Folded = li.attrIs("folded", "true")
	b.Bg = parseBg(li, b.Kind)
	b.Children = nested
	return b
}

// parseBg 解析块背景；callout 自己的 background-color 是它的填充色。
func parseBg(e *element, kind BlockKind) *ColorName {
	if v, ok := e.attr("background-color"); ok && TakesBG(kind) {
		c := ColorName(v)
		return &c
	}
	return nil
}

// splitInlineAndBlocks 把子节点拆成行内内容和嵌套块。段落里的
// `<source>` 是行内附件（文本视图），不算块。
func splitInlineAndBlocks(children []node) ([]node, []Block) {
	isBlk := func(n node) bool {
		return n.el != nil && isBlockTag(n.el.name) && n.el.name != "source"
	}
	var inline []node
	var blocks []Block
	for i := range children {
		n := children[i]
		switch {
		case isBlk(n):
			parseBlock(n.el, &blocks)
		case n.el == nil && i+1 < len(children) && isBlk(children[i+1]):
			// 嵌套块前面的空格和 Tab 是排版：保留文本、去掉尾随空白。
			inline = append(inline, node{text: strings.TrimRight(n.text, " \t")})
		case n.el == nil && len(blocks) > 0 && strings.TrimSpace(n.text) == "":
			// 嵌套块之后的纯空白丢弃。
		default:
			inline = append(inline, n)
		}
	}
	return inline, blocks
}

func parseTable(e *element) Table {
	var colWidths []float32
	var rows [][]TableCell
	var taken [][]bool
	growTo := func(n int) {
		for len(rows) < n {
			rows = append(rows, nil)
			taken = append(taken, nil)
		}
	}
	for _, n := range e.children {
		if cg := n.el; cg != nil && cg.name == "colgroup" {
			for _, cn := range cg.children {
				col := cn.el
				if col == nil {
					continue
				}
				w := float32(100)
				if v, ok := col.attr("width"); ok {
					if f, err := parseFloat32(trimSuffixAll(v, "px")); err == nil {
						w = f
					}
				}
				span := 1
				if v, ok := col.attr("span"); ok {
					if n, err := strconv.Atoi(v); err == nil && n > 0 {
						span = n
					}
				}
				for i := 0; i < span; i++ {
					colWidths = append(colWidths, w)
				}
			}
		}
	}
	var trs []*element
	var trHead []bool
	var collect func(el *element, head bool)
	collect = func(el *element, head bool) {
		for _, n := range el.children {
			if c := n.el; c != nil {
				switch c.name {
				case "thead":
					collect(c, true)
				case "tbody", "tfoot":
					collect(c, false)
				case "tr":
					trs = append(trs, c)
					trHead = append(trHead, head)
				}
			}
		}
	}
	collect(e, false)
	// 按 HTML 占位规则放单元格：每个单元格取本行下一个空槽，其跨度
	// 盖住的槽变成 covered。
	var kinds [][3]bool // 每行：在 thead 里、全是 th、以 th 开头
	for r, tr := range trs {
		growTo(r + 1)
		allTh, any := true, false
		// 本行第一个槽若被上面的单元格盖住，按盖住它的那个单元格算。
		firstTh := len(taken[r]) > 0 && taken[r][0]
		c := 0
		for _, n := range tr.children {
			td := n.el
			if td == nil || (td.name != "td" && td.name != "th") {
				continue
			}
			for c < len(taken[r]) && taken[r][c] {
				c++
			}
			if c == 0 {
				firstTh = td.name == "th"
			}
			if td.name != "th" {
				allTh = false
			}
			any = true
			colspan := 1
			if v, ok := td.attr("colspan"); ok {
				if n, err := strconv.Atoi(v); err == nil && n > 0 {
					colspan = n
				}
			}
			rowspan := 1
			if v, ok := td.attr("rowspan"); ok {
				if n, err := strconv.Atoi(v); err == nil {
					rowspan = min(max(n, 1), len(trs)-r)
				}
			}
			for rr := r; rr < r+rowspan; rr++ {
				growTo(rr + 1)
				for cc := c; cc < c+colspan; cc++ {
					for len(taken[rr]) <= cc {
						taken[rr] = append(taken[rr], false)
					}
					taken[rr][cc] = true
					for len(rows[rr]) <= cc {
						rows[rr] = append(rows[rr], CoveredCell())
					}
				}
			}
			var bg *ColorName
			if v, ok := td.attr("background-color"); ok {
				b := ColorName(v)
				bg = &b
			}
			var valign *string
			if v, ok := td.attr("vertical-align"); ok {
				valign = &v
			}
			rows[r][c] = TableCell{
				Blocks:  nonEmptyBlocks(parseBlocks(td.children)),
				Bg:      bg,
				Valign:  valign,
				Colspan: uint32(colspan),
				Rowspan: uint32(rowspan),
			}
			c += colspan
		}
		kinds = append(kinds, [3]bool{trHead[r], any && allTh, firstTh})
	}
	headerRows := 0
	for _, k := range kinds {
		if !k[0] {
			break
		}
		headerRows++
	}
	if headerRows == 0 && len(kinds) > 0 && kinds[0][1] {
		headerRows = 1
	}
	body := kinds[min(headerRows, len(kinds)):]
	headerCol := len(body) > 0
	for _, k := range body {
		if !k[2] {
			headerCol = false
			break
		}
	}
	ncols := 0
	for _, row := range rows {
		ncols = max(ncols, len(row))
	}
	for len(colWidths) < ncols {
		colWidths = append(colWidths, TableDefaultWidth)
	}
	// 没被认领的槽（参差行）恢复成空单元格。
	for r := range rows {
		for c := range rows[r] {
			if rows[r][c].IsCovered() && !(len(taken[r]) > c && taken[r][c]) {
				rows[r][c] = EmptyCell()
			}
		}
	}
	t := Table{ColWidths: colWidths, HeaderRows: headerRows, HeaderCol: headerCol, Rows: rows}
	t.Repair()
	return t
}

// parseCrop 解析 crop="l,t,r,b"：全图比例、有序、都在 0..1。
func parseCrop(v string) ([4]float32, bool) {
	var ns []float32
	for _, p := range strings.Split(v, ",") {
		if f, err := parseFloat32(strings.TrimSpace(p)); err == nil {
			ns = append(ns, f)
		}
	}
	if len(ns) != 4 {
		return [4]float32{}, false
	}
	c := [4]float32{ns[0], ns[1], ns[2], ns[3]}
	for _, x := range c {
		if !(x >= 0 && x <= 1) {
			return [4]float32{}, false
		}
	}
	if !(c[0] < c[2] && c[1] < c[3]) || c == [4]float32{0, 0, 1, 1} {
		return [4]float32{}, false
	}
	return c, true
}

func parseCropAttr(e *element) *[4]float32 {
	v, ok := e.attr("crop")
	if !ok {
		return nil
	}
	c, ok := parseCrop(v)
	if !ok {
		return nil
	}
	return &c
}

// figureSource 返回 `<figure>` 里的 `<source>`。
func figureSource(e *element) *element {
	for _, n := range e.children {
		if c := n.el; c != nil && c.name == "source" {
			return c
		}
	}
	return nil
}

func fileOf(e *element, view FileView) FileBlock {
	srcKind, src := "path", ""
	for _, k := range []string{"path", "token"} {
		if v, ok := e.attr(k); ok {
			srcKind, src = k, v
			break
		}
	}
	name := ""
	if v, ok := e.attr("name"); ok {
		name = v
	} else if srcKind == "path" {
		// 本地路径以文件名自称。
		if i := strings.LastIndex(src, "/"); i >= 0 {
			name = src[i+1:]
		}
	}
	return FileBlock{SrcKind: srcKind, Src: src, Name: name, View: view}
}

// ToInline 构造一个 Kind 类型的行内对象，拼出规范 XML 再读回一遍，
// 两个部分都和重新载入时一致。
func ToInline(kind InlineKind) Inline {
	var x string
	switch k := kind.(type) {
	case Equation:
		x = "<latex>" + escape(k.Src, false) + "</latex>"
	case InlineFile:
		x = sourceXML(k.File)
	case Button:
		x = `<button action="` + k.Action.Name() + `"`
		if k.Action == ActionOpenLink && k.Src != "" {
			x += ` src="` + escapeAttr(k.Src, false) + `"`
		}
		if k.Color != "" {
			x += ` background-color="` + escapeAttr(k.Color, false) + `"`
		}
		x += ">" + escape(k.Text, false) + "</button>"
	case Reminder:
		x = `<time expire-time="` + strconv.FormatInt(k.Expire, 10) +
			`" notify-time="` + strconv.FormatInt(k.Notify, 10) +
			`" should-notify="` + strconv.FormatBool(k.ShouldNotify) + `"`
		if k.WholeDay {
			x += ` is-whole-day="true"`
		}
		x += ">" + escape(k.Label(" "), false) + "</time>"
	case InlineDocLink:
		x = `<cite type="doc" doc-id="` + escapeAttr(k.ID, false) + `" file-type="docx">` + escape(k.Title, false) + `</cite>`
	case OtherInline:
		x = "<" + k.Tag + "/>"
	}
	if nodes, err := parseDOM(x); err == nil && len(nodes) > 0 && nodes[0].el != nil {
		return inlineOf(nodes[0].el)
	}
	return Inline{Kind: kind, XML: x}
}

// inlineOf 读入一个行内元素：原样保留，能理解则解析出 Kind。
func inlineOf(e *element) Inline {
	var kind InlineKind
	switch {
	case e.name == "latex":
		kind = Equation{Src: codeText(e.children)}
	case e.name == "source":
		kind = InlineFile{File: fileOf(e, ViewCard)}
	case e.name == "button":
		action := ActionOpenLink
		switch e.attrOr("action", "") {
		case "DuplicatePage":
			action = ActionDuplicatePage
		case "FollowPage":
			action = ActionFollowPage
		}
		kind = Button{
			Text:   inlineText(e.children).PlainText(),
			Action: action,
			Src:    e.attrOr("src", ""),
			Color:  e.attrOr("background-color", ButtonDefaultColor),
		}
	case e.name == "time":
		if r, ok := reminderOf(e); ok {
			kind = r
		}
	case e.name == "cite" && e.attrIs("type", "doc"):
		if id, ok := e.attr("doc-id"); ok {
			kind = InlineDocLink{ID: id, Title: codeText(e.children)}
		}
	}
	if kind == nil {
		kind = OtherInline{Tag: e.name}
	}
	return Inline{Kind: kind, XML: serializeEl(e)}
}

func reminderOf(e *element) (Reminder, bool) {
	v, ok := e.attr("expire-time")
	if !ok {
		return Reminder{}, false
	}
	expire, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil {
		return Reminder{}, false
	}
	notify := expire
	if v, ok := e.attr("notify-time"); ok {
		if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			notify = n
		}
	}
	wholeDay := false
	if v, ok := e.attr("is-whole-day"); ok {
		wholeDay = v == "true"
	} else {
		_, _, _, h, mi := civil(uint64(max(expire, 0)) / 1000)
		wholeDay = h == 23 && mi == 59
	}
	return Reminder{Expire: expire, Notify: notify, ShouldNotify: !e.attrIs("should-notify", "false"), WholeDay: wholeDay}, true
}

// RawSummary 是导出时对一个原样保留元素能展示的内容：其文本，和第一
// 个链接类属性（href、url、src、link）。
func RawSummary(xmlText string) (string, *string) {
	nodes, err := parseDOM(xmlText)
	if err != nil {
		return "", nil
	}
	var link *string
	for _, n := range nodes {
		if e := n.el; e != nil {
			for _, k := range []string{"href", "url", "src", "link"} {
				if v, ok := e.attr(k); ok {
					link = &v
					break
				}
			}
			if link != nil {
				break
			}
		}
	}
	return strings.TrimSpace(codeText(nodes)), link
}

// codeText 拼元素的文本内容；`<br/>` 是换行。
func codeText(nodes []node) string {
	var s strings.Builder
	for _, n := range nodes {
		if n.el == nil {
			s.WriteString(n.text)
			continue
		}
		if n.el.name == "br" {
			s.WriteByte('\n')
			continue
		}
		s.WriteString(codeText(n.el.children))
	}
	return s.String()
}

func inlineText(nodes []node) RichText {
	var out RichText
	walkInline(nodes, Marks{}, &out)
	return out
}

// asciiWS 与 Rust 的 char::is_ascii_whitespace 一致（不含 \v）。
const asciiWS = " \t\n\r\f"

func walkInline(nodes []node, marks Marks, out *RichText) {
	for _, n := range nodes {
		if n.el == nil {
			t := n.text
			// 源码排版（换行加缩进）不是内容；<br/> 是，边缘的普通
			// 空格也是。尾随：从正文后第一个换行起丢弃。
			bodyEnd := len(strings.TrimRight(t, asciiWS))
			end := len(t)
			if k := strings.IndexByte(t[bodyEnd:], '\n'); k >= 0 {
				end = bodyEnd + k
			}
			t = t[:end]
			// 前导：到正文前最后一个换行为止丢弃。
			bodyStart := len(t) - len(strings.TrimLeft(t, asciiWS))
			start := 0
			if k := strings.LastIndexByte(t[:bodyStart], '\n'); k >= 0 {
				start = k + 1
			}
			out.Push(strings.NewReplacer("\n", "", "\r", "").Replace(t[start:]), marks)
			continue
		}
		e := n.el
		m := marks
		switch e.name {
		case "br":
			out.Push("\n", marks)
			continue
		case "b", "strong":
			m.Bold = true
		case "em", "i":
			m.Italic = true
		case "u":
			m.Underline = true
		case "del", "s":
			m.Strike = true
		case "code":
			m.Code = true
		case "a":
			tv, hasType := e.attr("type")
			if !hasType || tv != "url-preview" || e.hasAttr("href") {
				if hv, ok := e.attr("href"); ok {
					m.Link = &hv
				} else {
					m.Link = nil
				}
			}
		case "span":
			if v, ok := e.attr("text-color"); ok {
				c := ColorName(v)
				m.TextColor = &c
			}
			if v, ok := e.attr("background-color"); ok {
				c := ColorName(v)
				m.BgColor = &c
			}
		default:
			// 行内对象（公式、提及……）各占一个 U+FFFC，元素原样保存。
			in := inlineOf(e)
			m.Inline = &in
			out.Push("\ufffc", m)
			continue
		}
		walkInline(e.children, m, out)
	}
}

func trimRich(t *RichText) {
	start := len(t.Text) - len(strings.TrimLeft(t.Text, " \t"))
	end := len(strings.TrimRight(t.Text, " \t"))
	if start > 0 || end < len(t.Text) {
		*t = t.Slice(start, max(end, start))
	}
}

// ---------------------------------------------------------------- 序列化

// Serialize 把 Document 写成 DocxXML。
func Serialize(doc *Document) string {
	w := &xmlWriter{}
	w.b.WriteString("<title")
	if doc.Icon != nil {
		w.b.WriteString(` icon="` + w.escAttr(*doc.Icon) + `"`)
	}
	if doc.Cover != nil {
		w.b.WriteString(` cover="` + w.escAttr(doc.Cover.Src) + `"`)
		if doc.Cover.X != 0 {
			w.b.WriteString(` cover-x="` + fmtF32(doc.Cover.X) + `"`)
		}
		if doc.Cover.Y != 0 {
			w.b.WriteString(` cover-y="` + fmtF32(doc.Cover.Y) + `"`)
		}
	}
	w.b.WriteByte('>')
	w.writeInline(doc.Title)
	w.b.WriteString("</title>\n")
	w.writeBlocks(doc.Blocks, 0)
	return w.b.String()
}

// xmlWriter 累积输出。写表格单元格时 inCell 为真：块的排版换行不写，
// 内容里的换行写成 &#10;，免得和排版混淆。
type xmlWriter struct {
	b      strings.Builder
	inCell bool
}

func (w *xmlWriter) esc(s string) string     { return escape(s, w.inCell) }
func (w *xmlWriter) escAttr(s string) string { return escapeAttr(s, w.inCell) }
func (w *xmlWriter) indent(depth int) {
	if w.inCell {
		return // 单元格里的块写在一行内，不缩进
	}
	for i := 0; i < depth; i++ {
		w.b.WriteString("  ")
	}
}

// escape 只处理 <、>、& 和（单元格内）换行；属性再加 "。
func escape(s string, inCell bool) string {
	need := false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '<', '>', '&':
			need = true
		case '\n':
			if inCell {
				need = true
			}
		}
	}
	if !need {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '&':
			b.WriteString("&amp;")
		case '\n':
			if inCell {
				b.WriteString("&#10;")
			} else {
				b.WriteByte('\n')
			}
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

func escapeAttr(s string, inCell bool) string {
	return strings.ReplaceAll(escape(s, inCell), `"`, "&quot;")
}

func alignAttr(a Align) string {
	switch a {
	case AlignCenter:
		return ` align="center"`
	case AlignRight:
		return ` align="right"`
	default:
		return ""
	}
}

// foldedAttr：文本、标题、列表和待办块记得自己被折叠。
func foldedAttr(folded bool) string {
	if folded {
		return ` folded="true"`
	}
	return ""
}

func (w *xmlWriter) bgAttr(b *Block) string {
	if b.Bg != nil && TakesBG(b.Kind) {
		return ` background-color="` + w.escAttr(string(*b.Bg)) + `"`
	}
	return ""
}

func (w *xmlWriter) writeBlocks(blocks []Block, depth int) {
	i := 0
	for i < len(blocks) {
		if _, ok := blocks[i].Kind.(Bullet); ok {
			i = w.writeList(blocks, i, depth, false)
			continue
		}
		if _, ok := blocks[i].Kind.(Ordered); ok {
			i = w.writeList(blocks, i, depth, true)
			continue
		}
		w.writeBlock(&blocks[i], depth)
		i++
	}
}

// writeList 写出从 i 开始的同族列表（ul/ol），返回下一块索引。
func (w *xmlWriter) writeList(blocks []Block, i, depth int, ordered bool) int {
	tag := "ul"
	if ordered {
		tag = "ol"
	}
	w.indent(depth)
	w.b.WriteString("<" + tag + ">\n")
	for i < len(blocks) {
		_, isB := blocks[i].Kind.(Bullet)
		_, isO := blocks[i].Kind.(Ordered)
		_, isT := blocks[i].Kind.(Todo)
		if !(isO == ordered && (isB || isO || isT) && !isT) {
			break
		}
		li := &blocks[i]
		w.indent(depth + 1)
		seq := ""
		if o, ok := li.Kind.(Ordered); ok && o.Seq != nil {
			seq = ` seq="` + strconv.FormatUint(uint64(*o.Seq), 10) + `"`
		}
		w.b.WriteString("<li" + seq + alignAttr(li.Align) + w.bgAttr(li) + foldedAttr(li.Folded) + ">")
		w.writeInline(li.Text)
		// 空项的第一个子段落读回时会变成项自己的文字（parseLi），要写明。
		if li.Text.IsEmpty() && len(li.Children) > 0 {
			if _, ok := li.Children[0].Kind.(Text); ok {
				w.b.WriteString("<p></p>")
			}
		}
		if len(li.Children) > 0 {
			w.b.WriteByte('\n')
			w.writeBlocks(li.Children, depth+2)
			w.indent(depth + 1)
		}
		w.b.WriteString("</li>\n")
		i++
	}
	w.indent(depth)
	w.b.WriteString("</" + tag + ">\n")
	return i
}

// writeTextWithChildren：先行内内容，再嵌套子级，最后闭合标签。
func (w *xmlWriter) writeTextWithChildren(b *Block, close string, depth int) {
	w.writeInline(b.Text)
	if len(b.Children) > 0 {
		w.b.WriteByte('\n')
		w.writeBlocks(b.Children, depth+1)
		w.indent(depth)
	}
	w.b.WriteString(close)
	w.b.WriteByte('\n')
}

func (w *xmlWriter) writeBlock(b *Block, depth int) {
	w.indent(depth)
	al := alignAttr(b.Align) + w.bgAttr(b)
	fo := foldedAttr(b.Folded && HasText(b.Kind))
	switch k := b.Kind.(type) {
	case Text:
		w.b.WriteString("<p" + al + fo + ">")
		w.writeTextWithChildren(b, "</p>", depth)
	case Heading:
		seq := ""
		if k.SeqAuto {
			seq = ` seq="auto"`
		}
		w.b.WriteString("<h" + strconv.Itoa(int(k.Level)) + seq + al + fo + ">")
		w.writeTextWithChildren(b, "</h"+strconv.Itoa(int(k.Level))+">", depth)
	case Todo:
		w.b.WriteString(`<checkbox done="` + strconv.FormatBool(k.Done) + `"` + al + fo + ">")
		w.writeTextWithChildren(b, "</checkbox>", depth)
	case Code:
		w.b.WriteString(`<pre lang="` + w.escAttr(LangToToken(k.Lang)) + `"`)
		if k.Caption != nil {
			w.b.WriteString(` caption="` + w.escAttr(*k.Caption) + `"`)
		}
		if k.Wrap {
			w.b.WriteString(` wrap="true"`)
		}
		w.b.WriteString("><code>")
		w.b.WriteString(w.esc(b.Text.Text))
		w.b.WriteString("</code></pre>\n")
	case Quote:
		w.b.WriteString("<blockquote" + w.bgAttr(b) + ">\n")
		w.writeBlocks(b.Children, depth+1)
		w.indent(depth)
		w.b.WriteString("</blockquote>\n")
	case Callout:
		w.b.WriteString(`<callout emoji="` + w.escAttr(k.Emoji) + `"`)
		if k.Bg != nil {
			w.b.WriteString(` background-color="` + w.escAttr(string(*k.Bg)) + `"`)
		}
		if k.Border != nil {
			w.b.WriteString(` border-color="` + w.escAttr(string(*k.Border)) + `"`)
		}
		if k.TextColor != nil {
			w.b.WriteString(` text-color="` + w.escAttr(string(*k.TextColor)) + `"`)
		}
		w.b.WriteString(">\n")
		w.writeBlocks(b.Children, depth+1)
		w.indent(depth)
		w.b.WriteString("</callout>\n")
	case Divider:
		w.b.WriteString("<hr/>\n")
	case SubPageList:
		w.b.WriteString("<sub-page-list/>\n")
	case Countdown:
		timing := "duration"
		if k.ByDate {
			timing = "date"
		}
		w.b.WriteString(`<countdown duration="` + fmtNum(float32(k.Duration)) +
			`" start-time="` + strconv.FormatUint(k.Start, 10) +
			`" notify="` + strconv.FormatBool(k.Notify) +
			`" color="` + w.escAttr(k.Color) +
			`" timing="` + timing + `"`)
		if k.Dhms != [4]uint32{} {
			d, h, m, s := k.Dhms[0], k.Dhms[1], k.Dhms[2], k.Dhms[3]
			w.b.WriteString(` days="` + strconv.FormatUint(uint64(d), 10) +
				`" hours="` + strconv.FormatUint(uint64(h), 10) +
				`" minutes="` + strconv.FormatUint(uint64(m), 10) +
				`" seconds="` + strconv.FormatUint(uint64(s), 10) + `"`)
		}
		if k.Date != "" {
			w.b.WriteString(` date="` + w.escAttr(k.Date) + `" time="` + w.escAttr(k.Time) + `"`)
		}
		w.b.WriteString("/>\n")
	case FileBlock:
		view := "Card"
		if k.View == ViewPreview {
			view = "Preview"
		}
		w.b.WriteString(`<figure view-type="` + view + `"` + al + ">" + w.sourceXML(k) + "</figure>\n")
	case Image:
		w.b.WriteString("<img " + k.SrcKind + `="` + w.escAttr(k.Src) + `"`)
		if k.Width != nil {
			w.b.WriteString(` width="` + fmtNum(*k.Width) + `"`)
		}
		if k.Height != nil {
			w.b.WriteString(` height="` + fmtNum(*k.Height) + `"`)
		}
		if k.Caption != nil && *k.Caption != "" {
			w.b.WriteString(` caption="` + w.escAttr(*k.Caption) + `"`)
		}
		if k.Crop != nil {
			parts := make([]string, 4)
			for i, x := range k.Crop {
				parts[i] = fmtF32(float32(math.Round(float64(x*10000))) / 10000)
			}
			w.b.WriteString(` crop="` + strings.Join(parts, ",") + `"`)
		}
		// 左对齐也写：缺 align 属性读回会当成居中。
		switch b.Align {
		case AlignLeft:
			w.b.WriteString(` align="left"`)
		case AlignCenter:
			w.b.WriteString(` align="center"`)
		case AlignRight:
			w.b.WriteString(` align="right"`)
		}
		w.b.WriteString("/>\n")
	case Table:
		w.writeTable(&k, depth)
	case Grid:
		w.b.WriteString("<grid>\n")
		for i := range b.Children {
			col := &b.Children[i]
			ratio := float32(0.5)
			if c, ok := col.Kind.(Column); ok {
				ratio = c.Ratio
			}
			w.indent(depth + 1)
			w.b.WriteString(`<column width-ratio="` + fmtNum(ratio) + `">` + "\n")
			w.writeBlocks(col.Children, depth+2)
			w.indent(depth + 1)
			w.b.WriteString("</column>\n")
		}
		w.indent(depth)
		w.b.WriteString("</grid>\n")
	case Column:
		w.b.WriteString(`<column width-ratio="` + fmtNum(k.Ratio) + `">` + "\n")
		w.writeBlocks(b.Children, depth+1)
		w.indent(depth)
		w.b.WriteString("</column>\n")
	case Raw:
		if w.inCell {
			w.b.WriteString(strings.ReplaceAll(k.XML, "\n", "&#10;"))
		} else {
			w.b.WriteString(k.XML)
		}
		w.b.WriteByte('\n')
	}
}

func (w *xmlWriter) writeTable(t *Table, depth int) {
	w.b.WriteString("<table>\n")
	w.indent(depth + 1)
	w.b.WriteString("<colgroup>")
	for _, cw := range t.ColWidths {
		w.b.WriteString(`<col width="` + fmtNum(cw) + `"/>`)
	}
	w.b.WriteString("</colgroup>\n")
	writeRows := func(rows [][]TableCell, head bool) {
		for _, row := range rows {
			w.indent(depth + 2)
			w.b.WriteString("<tr>")
			for c := range row {
				cell := &row[c]
				if cell.IsCovered() {
					continue
				}
				tag := "td"
				if head || (c == 0 && t.HeaderCol) {
					tag = "th"
				}
				w.b.WriteString("<" + tag)
				if cell.Bg != nil {
					w.b.WriteString(` background-color="` + w.escAttr(string(*cell.Bg)) + `"`)
				}
				if cell.Valign != nil {
					w.b.WriteString(` vertical-align="` + w.escAttr(*cell.Valign) + `"`)
				}
				if cell.Colspan > 1 {
					w.b.WriteString(` colspan="` + strconv.FormatUint(uint64(cell.Colspan), 10) + `"`)
				}
				if cell.Rowspan > 1 {
					w.b.WriteString(` rowspan="` + strconv.FormatUint(uint64(cell.Rowspan), 10) + `"`)
				}
				w.b.WriteByte('>')
				inner := &xmlWriter{inCell: true}
				inner.writeBlocks(cell.Blocks, 0)
				w.b.WriteString(strings.ReplaceAll(inner.b.String(), "\n", ""))
				w.b.WriteString("</" + tag + ">")
			}
			w.b.WriteString("</tr>\n")
		}
	}
	h := min(t.HeaderRows, len(t.Rows))
	if h > 0 {
		w.indent(depth + 1)
		w.b.WriteString("<thead>\n")
		writeRows(t.Rows[:h], true)
		w.indent(depth + 1)
		w.b.WriteString("</thead>\n")
	}
	w.indent(depth + 1)
	w.b.WriteString("<tbody>\n")
	writeRows(t.Rows[h:], false)
	w.indent(depth + 1)
	w.b.WriteString("</tbody>\n")
	w.indent(depth)
	w.b.WriteString("</table>\n")
}

// sourceXML 是附件的 `<source>` 元素（写出路径按当前单元格状态转义）。
func (w *xmlWriter) sourceXML(f FileBlock) string {
	kind := "path"
	if f.SrcKind == "token" {
		kind = "token"
	}
	return "<source " + kind + `="` + w.escAttr(f.Src) + `" name="` + w.escAttr(f.Name) + `"/>`
}

func sourceXML(f FileBlock) string {
	kind := "path"
	if f.SrcKind == "token" {
		kind = "token"
	}
	return "<source " + kind + `="` + escapeAttr(f.Src, false) + `" name="` + escapeAttr(f.Name, false) + `"/>`
}

// wrappers 返回一个 run 的行内包裹元素，从外到内。相邻 run 的公共前缀
// 共享元素，彩色段落因此是一个 <span>。
type wrapper struct{ open, close string }

func wrappers(m *Marks, w *xmlWriter) []wrapper {
	var out []wrapper
	if m.TextColor != nil || m.BgColor != nil {
		open := "<span"
		if c := m.TextColor; c != nil {
			open += ` text-color="` + w.escAttr(string(*c)) + `"`
		}
		if c := m.BgColor; c != nil {
			open += ` background-color="` + w.escAttr(string(*c)) + `"`
		}
		open += ">"
		out = append(out, wrapper{open, "</span>"})
	}
	if m.Link != nil {
		out = append(out, wrapper{`<a href="` + w.escAttr(*m.Link) + `">`, "</a>"})
	}
	for _, on := range []struct {
		set   bool
		open  string
		close string
	}{
		{m.Bold, "<b>", "</b>"},
		{m.Italic, "<em>", "</em>"},
		{m.Underline, "<u>", "</u>"},
		{m.Strike, "<del>", "</del>"},
		{m.Code, "<code>", "</code>"},
	} {
		if on.set {
			out = append(out, wrapper{on.open, on.close})
		}
	}
	return out
}

func (w *xmlWriter) writeInline(t RichText) {
	var stack []wrapper
	closeTo := func(n int) {
		for len(stack) > n {
			w.b.WriteString(stack[len(stack)-1].close)
			stack = stack[:len(stack)-1]
		}
	}
	for _, sp := range t.Spans() {
		seg := t.Text[sp.Start:sp.End]
		if obj := sp.Marks.Inline; obj != nil {
			closeTo(0)
			// 相邻的相同对象共享一个 run，按字符数重复输出。
			for range seg {
				w.b.WriteString(obj.XML)
			}
			continue
		}
		want := wrappers(&sp.Marks, w)
		keep := 0
		for keep < len(stack) && keep < len(want) && stack[keep] == want[keep] {
			keep++
		}
		closeTo(keep)
		for _, wr := range want[keep:] {
			w.b.WriteString(wr.open)
			stack = append(stack, wr)
		}
		first := true
		for _, part := range strings.Split(seg, "\n") {
			if !first {
				w.b.WriteString("<br/>")
			}
			first = false
			w.b.WriteString(w.esc(part))
		}
	}
	closeTo(0)
}

func serializeEl(e *element) string {
	var b strings.Builder
	b.WriteString("<" + e.name)
	for _, a := range e.attrs {
		b.WriteString(" " + a.key + `="` + escapeAttr(a.val, false) + `"`)
	}
	if len(e.children) == 0 {
		b.WriteString("/>")
		return b.String()
	}
	b.WriteByte('>')
	for _, c := range e.children {
		if c.el != nil {
			b.WriteString(serializeEl(c.el))
		} else {
			b.WriteString(escape(c.text, false))
		}
	}
	b.WriteString("</" + e.name + ">")
	return b.String()
}

// ---------------------------------------------------------------- 数值

// satI64 与 Rust 的 float as i64 一致：NaN→0，越界饱和。
func satI64(v float32) int64 {
	f := float64(v)
	switch {
	case math.IsNaN(f):
		return 0
	case f >= 9223372036854775808.0:
		return math.MaxInt64
	case f <= -9223372036854775808.0:
		return math.MinInt64
	default:
		return int64(f)
	}
}

// fmtNum 是宽度、比例等属性的数字写法：整数值不带小数点，否则保留
// 最多三位小数。
func fmtNum(v float32) string {
	if v == float32(math.Trunc(float64(v))) {
		return strconv.FormatInt(satI64(v), 10)
	}
	return fmtF32(float32(math.Round(float64(v*1000))) / 1000)
}

// fmtF32 是 Rust f32 Display 的等价格式：最短往返小数。
func fmtF32(v float32) string {
	return strconv.FormatFloat(float64(v), 'f', -1, 32)
}

// parseFloat32/64 与 Rust 的 float::from_str 一致：上溢得 ±Inf、下溢
// 得 0，都不算错误。
func parseFloat32(s string) (float32, error) {
	f, err := strconv.ParseFloat(s, 32)
	if errors.Is(err, strconv.ErrRange) {
		return float32(f), nil
	}
	return float32(f), err
}

func parseFloat64(s string) (float64, error) {
	f, err := strconv.ParseFloat(s, 64)
	if errors.Is(err, strconv.ErrRange) {
		return f, nil
	}
	return f, err
}

func attrF32(e *element, k string) *float32 {
	v, ok := e.attr(k)
	if !ok {
		return nil
	}
	f, err := parseFloat32(v)
	if err != nil {
		return nil
	}
	return &f
}

func attrNonEmpty(e *element, k string) *string {
	if v, ok := e.attr(k); ok && v != "" {
		return &v
	}
	return nil
}

func attrU32Or(e *element, k string) uint32 {
	if v, ok := e.attr(k); ok {
		if n, err := strconv.ParseUint(v, 10, 32); err == nil {
			return uint32(n)
		}
	}
	return 0
}

// trimSuffixAll 去掉所有重复出现的后缀（同 Rust trim_end_matches）。
func trimSuffixAll(s, suf string) string {
	for strings.HasSuffix(s, suf) {
		s = s[:len(s)-len(suf)]
	}
	return s
}

// ParseInline 解析一段行内对象的原文 XML（to_inline 的逆），不是行内
// 对象时返回 nil。
func ParseInline(xmlText string) *Inline {
	nodes, err := parseDOM(xmlText)
	if err != nil || len(nodes) == 0 || nodes[0].el == nil {
		return nil
	}
	in := inlineOf(nodes[0].el)
	if _, isOther := in.Kind.(OtherInline); isOther {
		return nil
	}
	return &in
}
