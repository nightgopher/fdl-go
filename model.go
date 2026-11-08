// Package fdl 是 quiet-md 文档引擎（fdl_core）的 Go 移植：DocxXML 数据
// 模型与双向解析/序列化。行为对拍上游 ~/Projects/third-party/quiet-md
// （只读），里程碑与验收见仓库 README。
package fdl

import (
	"fmt"
	"math"
	"strings"
	"sync/atomic"
)

// BlockID 在进程内唯一标识一个块。
type BlockID uint64

var nextBlockID atomic.Uint64

// NewBlockID 分配新 id（从 1 开始递增）。
func NewBlockID() BlockID { return BlockID(nextBlockID.Add(1)) }

// Untitled 是空标题文档的显示名。
const Untitled = "未命名文档"

// DisplayTitle 返回列表和链接里显示的文档标题。
func DisplayTitle(t string) string {
	if t = strings.TrimSpace(t); t == "" {
		return Untitled
	}
	return t
}

// ColorName 是 DocxXML 里的调色板颜色名（red、light-blue、…）。
type ColorName string

// Marks 是一段文本的行内样式。行内对象（公式、文件、按钮、日期提醒、
// @文档等）在文本里各占一个 U+FFFC。
type Marks struct {
	Bold      bool
	Italic    bool
	Underline bool
	Strike    bool
	Code      bool
	Link      *string
	TextColor *ColorName
	BgColor   *ColorName
	Inline    *Inline
}

func eqPtr[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// Equal 深比较两份样式；行内对象只比较 XML（Kind 由 XML 决定）。
func (m Marks) Equal(o Marks) bool {
	if m.Bold != o.Bold || m.Italic != o.Italic || m.Underline != o.Underline ||
		m.Strike != o.Strike || m.Code != o.Code {
		return false
	}
	if !eqPtr(m.Link, o.Link) || !eqPtr(m.TextColor, o.TextColor) || !eqPtr(m.BgColor, o.BgColor) {
		return false
	}
	switch {
	case m.Inline == nil && o.Inline == nil:
		return true
	case m.Inline == nil || o.Inline == nil:
		return false
	default:
		return m.Inline.XML == o.Inline.XML
	}
}

// Inline 是一个行内对象。XML 是读入时的原文（也是写出的内容），不认识
// 的属性因此不会丢；Kind 是解析出的含义，视图和导出只看它。
type Inline struct {
	Kind InlineKind
	XML  string
}

func (in *Inline) Equation() (string, bool) {
	if e, ok := in.Kind.(Equation); ok {
		return e.Src, true
	}
	return "", false
}

func (in *Inline) File() (FileBlock, bool) {
	if f, ok := in.Kind.(InlineFile); ok {
		return f.File, true
	}
	return FileBlock{}, false
}

func (in *Inline) Button() (Button, bool) {
	if b, ok := in.Kind.(Button); ok {
		return b, true
	}
	return Button{}, false
}

func (in *Inline) Reminder() (Reminder, bool) {
	if r, ok := in.Kind.(Reminder); ok {
		return r, true
	}
	return Reminder{}, false
}

func (in *Inline) DocLink() (InlineDocLink, bool) {
	if l, ok := in.Kind.(InlineDocLink); ok {
		return l, true
	}
	return InlineDocLink{}, false
}

// InlineKind 是行内对象的类型。
type InlineKind interface{ isInlineKind() }

// Equation 是 `<latex>` 公式，Src 是 LaTeX 源码。
type Equation struct{ Src string }

// InlineFile 是段落里的 `<source>` 附件（文本视图；变成块时是卡片）。
type InlineFile struct{ File FileBlock }

// Button 是 `<button action src background-color>文字</button>`。
type Button struct {
	Text   string
	Action ButtonAction
	Src    string
	Color  string
}

// ButtonAction 是按钮的动作类型。
type ButtonAction int

const (
	ActionOpenLink ButtonAction = iota
	ActionDuplicatePage
	ActionFollowPage
)

func (a ButtonAction) Name() string {
	switch a {
	case ActionDuplicatePage:
		return "DuplicatePage"
	case ActionFollowPage:
		return "FollowPage"
	default:
		return "OpenLink"
	}
}

// Label 是类型菜单的名称，也是新按钮的默认文字。
func (a ButtonAction) Label() string {
	switch a {
	case ActionDuplicatePage:
		return "创建副本"
	case ActionFollowPage:
		return "关注文档更新"
	default:
		return "打开超链接"
	}
}

// ButtonDefaultColor 是新按钮的默认颜色。
const ButtonDefaultColor = "light-blue"

// NewButton 新建一个按钮行内对象。
func NewButton(action ButtonAction) Button {
	return Button{Text: action.Label(), Action: action, Color: ButtonDefaultColor}
}

// Reminder 是日期提醒（`<time>`），时间为 unix 毫秒。DocxXML 里没有全天
// 标记（模型里有）：我们写 is-whole-day；没有该属性、且到期时间是本地
// 23:59 的提醒也按全天处理。
type Reminder struct {
	Expire       int64
	Notify       int64
	ShouldNotify bool
	WholeDay     bool
}

// Label 形如 "2026年10月23日，14:00"（全天则只有日期）。
func (r Reminder) Label(sep string) string {
	y, m, d, h, mi := civil(uint64(max(r.Expire, 0)) / 1000)
	if r.WholeDay {
		return fmt.Sprintf("%d年%d月%d日", y, m, d)
	}
	return fmt.Sprintf("%d年%d月%d日%s%02d:%02d", y, m, d, sep, h, mi)
}

// InlineDocLink 是 @文档 提及（`<cite type="doc">`）。标题保留原文字，
// 供导出和目标文档已删除时显示；活标题来自文档库。
type InlineDocLink struct {
	ID    string
	Title string
}

// OtherInline 是原样保留、按其文字显示的行内元素（如 `<cite type="user">`）。
type OtherInline struct{ Tag string }

func (Equation) isInlineKind()      {}
func (InlineFile) isInlineKind()    {}
func (Button) isInlineKind()        {}
func (Reminder) isInlineKind()      {}
func (InlineDocLink) isInlineKind() {}
func (OtherInline) isInlineKind()   {}

// Align 是块级对齐。
type Align int

const (
	AlignLeft Align = iota
	AlignCenter
	AlignRight
)

// BlockKind 是块的类型。Table、Countdown、FileBlock 这三个数据结构本身
// 就是块类型（对应上游 BlockKind::Table/Countdown/File 变体）。
type BlockKind interface{ isBlockKind() }

// Text 是普通段落。
type Text struct{}

// Heading 是标题；SeqAuto 对应 seq="auto"。
type Heading struct {
	Level   uint8
	SeqAuto bool
}

// Bullet 是无序列表项。
type Bullet struct{}

// Ordered 是有序列表项；Seq 为 nil 表示自动编号。
type Ordered struct{ Seq *uint32 }

// Todo 是待办（`<checkbox done>`）。
type Todo struct{ Done bool }

// Code 是代码块；Lang 存显示名。
type Code struct {
	Lang    string
	Caption *string
	Wrap    bool
}

// Quote 是引用块。
type Quote struct{}

// Callout 是高亮块。
type Callout struct {
	Emoji     string
	Bg        *ColorName
	Border    *ColorName
	TextColor *ColorName
}

// Divider 是分割线。
type Divider struct{}

// Image 是图片。SrcKind 记录源在哪个属性上：path、href 或 src。Crop 是
// 全图的 [left, top, right, bottom] 可见比例，nil 表示未裁剪。
type Image struct {
	SrcKind string
	Src     string
	Width   *float32
	Height  *float32
	Caption *string
	Crop    *[4]float32
}

// Grid 是分栏；每栏是一个 Column 子块。
type Grid struct{}

// Column 是分栏的一栏，子块是内容。
type Column struct{ Ratio float32 }

// SubPageList 是子页面列表（`<sub-page-list/>`）。
type SubPageList struct{}

// Raw 是不认识的元素，原样保留以往返。
type Raw struct {
	Tag string
	XML string
}

func (Text) isBlockKind()        {}
func (Heading) isBlockKind()     {}
func (Bullet) isBlockKind()      {}
func (Ordered) isBlockKind()     {}
func (Todo) isBlockKind()        {}
func (Code) isBlockKind()        {}
func (Quote) isBlockKind()       {}
func (Callout) isBlockKind()     {}
func (Divider) isBlockKind()     {}
func (Image) isBlockKind()       {}
func (Table) isBlockKind()       {}
func (Grid) isBlockKind()        {}
func (Column) isBlockKind()      {}
func (SubPageList) isBlockKind() {}
func (Countdown) isBlockKind()   {}
func (FileBlock) isBlockKind()   {}
func (Raw) isBlockKind()         {}

// HasText：光标可以进入、拥有富文本行的块。
func HasText(k BlockKind) bool {
	switch k.(type) {
	case Text, Heading, Bullet, Ordered, Todo, Code:
		return true
	}
	return false
}

// IsContainer：子块渲染在自己内部的容器块。
func IsContainer(k BlockKind) bool {
	switch k.(type) {
	case Quote, Callout, Grid, Column:
		return true
	}
	return false
}

// Nests：可以带缩进子级的文本块（Tab、拖拽成首子块）。
func Nests(k BlockKind) bool {
	switch k.(type) {
	case Text, Heading, Bullet, Ordered, Todo:
		return true
	}
	return false
}

// IsList：列表块。
func IsList(k BlockKind) bool {
	switch k.(type) {
	case Bullet, Ordered, Todo:
		return true
	}
	return false
}

// TakesBG：块菜单「颜色」可以给整块加背景的块。
func TakesBG(k BlockKind) bool {
	switch k.(type) {
	case Text, Heading, Bullet, Ordered, Todo, Quote:
		return true
	}
	return false
}

// Countdown 是倒计时（本地扩展块 `<countdown …/>`），字段对应插件的
// 记录 {duration, startTime, isNotify, color, timingType, settingData}。
type Countdown struct {
	// Duration 是秒；设定长度加一，按插件存的。
	Duration float64
	// Start 是开始的 unix 毫秒；0 表示从未设置（显示 00:00:00:00）。
	Start uint64
	// Notify 到结束时显示气泡提醒。
	Notify bool
	// Color 是 CountdownColors 之一。
	Color  string
	ByDate bool // 选择具体日期（否则输入时长）
	// Dhms 是设置对话框上次的值：天、时、分、秒。
	Dhms [4]uint32
	// Date/Time 是按日期时的 "YYYY-MM-DD" / "HH:mm"。
	Date string
	Time string
}

// CountdownColors 是红/橙/黄/绿/蓝/紫/灰；橙是默认。
var CountdownColors = []string{"#F76964", "#FF8800", "#FFC60A", "#62D256", "#4E83FD", "#935AF6", "#BBBFC4"}

// NewCountdown 返回默认倒计时。
func NewCountdown() Countdown {
	return Countdown{Notify: true, Color: CountdownColors[1]}
}

// Remaining 是 nowMS 时刻剩下的整秒数（round(duration) - ceil(elapsed)），
// 不小于 0。
func (c Countdown) Remaining(nowMS uint64) uint64 {
	if c.Start == 0 {
		return 0
	}
	elapsed := 0.0
	if nowMS > c.Start {
		elapsed = math.Ceil(float64(nowMS-c.Start) / 1000)
	}
	r := math.Round(c.Duration) - elapsed
	if r < 0 {
		return 0
	}
	return uint64(r)
}

// FileView 是附件视图：卡片 / 预览（文本视图是段落里的行内 `<source>`）。
type FileView int

const (
	ViewCard FileView = iota
	ViewPreview
)

// FileBlock 是附件：本地文件（path，在文档库 assets 下）或无法取回的
// 云端 token。
type FileBlock struct {
	SrcKind string // path 或 token
	Src     string
	Name    string
	View    FileView
}

// Block 是文档树上的一个块。
type Block struct {
	ID       BlockID
	Kind     BlockKind
	Text     RichText
	Align    Align
	Bg       *ColorName // 整块背景（标签上的 background-color）
	Children []Block
	// Folded 对应 folded="true"：收起子级；标题则连整个小节一起收起。
	Folded bool
}

// NewBlock 新建块并分配 id。
func NewBlock(kind BlockKind, text RichText) Block {
	return Block{ID: NewBlockID(), Kind: kind, Text: text}
}

// BlockText 新建一个纯文本段落。
func BlockText(s string) Block { return NewBlock(Text{}, Plain(s)) }

// Document 是一篇文档。
type Document struct {
	Title  RichText
	Icon   *string // 文档图标：标题前的 emoji
	Cover  *Cover
	Blocks []Block
}

// Cover 是封面：本地图片加偏移比例（0 = 居中）。
type Cover struct {
	Src string
	X   float32
	Y   float32
}

// NewDocument 返回新文档：空标题加一个空段落。
func NewDocument() Document {
	return Document{Blocks: []Block{BlockText("")}}
}

// IsBlank：一篇没动过的新文档（无标题、无图标封面、只有一个空段落）。
func (d *Document) IsBlank() bool {
	if !d.Title.IsEmpty() || d.Icon != nil || d.Cover != nil {
		return false
	}
	if len(d.Blocks) != 1 {
		return false
	}
	b := &d.Blocks[0]
	_, isText := b.Kind.(Text)
	return isText && b.Text.IsEmpty() && len(b.Children) == 0
}

// PlainText 是整篇文档的纯文本（搜索和摘要用）。
func (d *Document) PlainText() string {
	var out strings.Builder
	var walk func(blocks []Block)
	walk = func(blocks []Block) {
		for i := range blocks {
			b := &blocks[i]
			if !b.Text.IsEmpty() {
				if out.Len() > 0 {
					out.WriteByte('\n')
				}
				out.WriteString(b.Text.PlainText())
			}
			walk(b.Children)
			if t, ok := b.Kind.(Table); ok {
				for _, row := range t.Rows {
					for _, cell := range row {
						walk(cell.Blocks)
					}
				}
			}
		}
	}
	walk(d.Blocks)
	return out.String()
}

// ForEachBlock 按文档顺序访问每个块，含表格单元格里的。
func (d *Document) ForEachBlock(f func(*Block)) {
	var walk func(blocks []Block)
	walk = func(blocks []Block) {
		for i := range blocks {
			b := &blocks[i]
			f(b)
			walk(b.Children)
			if t, ok := b.Kind.(Table); ok {
				for _, row := range t.Rows {
					for j := range row {
						walk(row[j].Blocks)
					}
				}
			}
		}
	}
	walk(d.Blocks)
}

// Reminders 返回文档里的日期提醒。
func (d *Document) Reminders() []Reminder {
	var out []Reminder
	d.ForEachBlock(func(b *Block) {
		for _, r := range b.Text.Runs {
			if r.Marks.Inline != nil {
				if rem, ok := r.Marks.Inline.Kind.(Reminder); ok {
					out = append(out, rem)
				}
			}
		}
	})
	return out
}

// DocLinkSeg 是提及所在段落的一截文字；ID 非 nil 表示这是一个 @文档。
type DocLinkSeg struct {
	Text string
	ID   *string
}

// DocLink 是一条 @文档提及：目标文档、所在块在 ForEachBlock 顺序中的
// 位置，以及该块的文字（用于反向引用）。
type DocLink struct {
	Target string
	Block  int
	Line   []DocLinkSeg
}

func linkLine(t *RichText) []DocLinkSeg {
	var out []DocLinkSeg
	for _, sp := range t.Spans() {
		seg := t.Text[sp.Start:sp.End]
		if sp.Marks.Inline != nil {
			if l, ok := sp.Marks.Inline.Kind.(InlineDocLink); ok {
				id := l.ID
				out = append(out, DocLinkSeg{Text: l.Title, ID: &id})
				continue
			}
		}
		s := strings.NewReplacer("\ufffc", " ", "\n", " ").Replace(seg)
		if sp.Marks.Inline != nil {
			if eq, ok := sp.Marks.Inline.Kind.(Equation); ok {
				s = eq.Src
			}
		}
		if n := len(out); n > 0 && out[n-1].ID == nil {
			out[n-1].Text += s
		} else {
			out = append(out, DocLinkSeg{Text: s})
		}
	}
	return out
}

// DocLinks 返回文档里的 @文档 提及，每 (目标, 块) 一条。
func (d *Document) DocLinks() []DocLink {
	var out []DocLink
	ord := 0
	d.ForEachBlock(func(b *Block) {
		here := ord
		ord++
		if !strings.ContainsRune(b.Text.Text, '\ufffc') {
			return
		}
		var line []DocLinkSeg
		for _, sp := range b.Text.Spans() {
			if sp.Marks.Inline == nil {
				continue
			}
			l, ok := sp.Marks.Inline.Kind.(InlineDocLink)
			if !ok {
				continue
			}
			dup := false
			for _, dl := range out {
				if dl.Block == here && dl.Target == l.ID {
					dup = true
					break
				}
			}
			if dup {
				continue
			}
			if line == nil {
				line = linkLine(&b.Text)
			}
			out = append(out, DocLink{Target: l.ID, Block: here, Line: line})
		}
	})
	return out
}

// AssetSources 返回图片和附件（块与行内附件）的源。
func (d *Document) AssetSources() []string {
	var out []string
	d.ForEachBlock(func(b *Block) {
		switch k := b.Kind.(type) {
		case Image:
			out = append(out, k.Src)
		case FileBlock:
			if k.SrcKind == "path" {
				out = append(out, k.Src)
			}
		}
		for _, r := range b.Text.Runs {
			if r.Marks.Inline == nil {
				continue
			}
			if f, ok := r.Marks.Inline.Kind.(InlineFile); ok && f.File.SrcKind == "path" {
				out = append(out, f.File.Src)
			}
		}
	})
	return out
}

func clonePtr[T comparable](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func cloneMarks(m Marks) Marks {
	m.Link = clonePtr(m.Link)
	m.TextColor = clonePtr(m.TextColor)
	m.BgColor = clonePtr(m.BgColor)
	if m.Inline != nil {
		in := *m.Inline
		m.Inline = &in
	}
	return m
}

func cloneKindShallow(k BlockKind) BlockKind {
	switch kk := k.(type) {
	case Ordered:
		kk.Seq = clonePtr(kk.Seq)
		return kk
	case Code:
		kk.Caption = clonePtr(kk.Caption)
		return kk
	case Callout:
		kk.Bg = clonePtr(kk.Bg)
		kk.Border = clonePtr(kk.Border)
		kk.TextColor = clonePtr(kk.TextColor)
		return kk
	case Image:
		kk.Width = clonePtr(kk.Width)
		kk.Height = clonePtr(kk.Height)
		kk.Caption = clonePtr(kk.Caption)
		kk.Crop = clonePtr(kk.Crop)
		return kk
	default:
		return k // 其余变体是纯值类型
	}
}

// Clone 深拷贝块（含子树、表格单元格和样式指针），保留原 id。
func (b Block) Clone() Block { return cloneBlock(b, false) }

// FreshIDs 深拷贝并给每个块分配新 id（粘贴 / 复制用）。
func (b Block) FreshIDs() Block { return cloneBlock(b, true) }

func cloneBlock(b Block, fresh bool) Block {
	if fresh {
		b.ID = NewBlockID()
	}
	b.Text = b.Text.Clone()
	b.Bg = clonePtr(b.Bg)
	if len(b.Children) > 0 {
		ch := make([]Block, len(b.Children))
		for i := range b.Children {
			ch[i] = cloneBlock(b.Children[i], fresh)
		}
		b.Children = ch
	}
	if tb, ok := b.Kind.(Table); ok {
		b.Kind = cloneTable(tb, fresh)
	} else {
		b.Kind = cloneKindShallow(b.Kind)
	}
	return b
}

func cloneTable(t Table, fresh bool) Table {
	out := Table{HeaderRows: t.HeaderRows, HeaderCol: t.HeaderCol}
	out.ColWidths = append([]float32(nil), t.ColWidths...)
	out.Rows = make([][]TableCell, len(t.Rows))
	for r := range t.Rows {
		out.Rows[r] = make([]TableCell, len(t.Rows[r]))
		for c := range t.Rows[r] {
			cell := t.Rows[r][c]
			cell.Bg = clonePtr(cell.Bg)
			cell.Valign = clonePtr(cell.Valign)
			if len(cell.Blocks) > 0 {
				bs := make([]Block, len(cell.Blocks))
				for i := range cell.Blocks {
					bs[i] = cloneBlock(cell.Blocks[i], fresh)
				}
				cell.Blocks = bs
			}
			out.Rows[r][c] = cell
		}
	}
	return out
}
