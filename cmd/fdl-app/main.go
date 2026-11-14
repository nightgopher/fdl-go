// fdl-app 是 fdl-go 的原生桌面客户端（mygo）：左侧笔记列表，右侧富文
// 本编辑，读写 Quiet.md 文档库，与 Quiet.md / fdl-mcp / fdl-sync 完全
// 互通。
//
//	go run ./cmd/fdl-app
//	QUIET_MD_HOME=/path/to/lib go run ./cmd/fdl-app
package main

import (
	"log"
	"os"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/richtext"

	fdl "github.com/nightgopher/fdl-go"
)

// entry 是列表里的一行。
type entry struct {
	id       string
	title    string
	body     string // 搜索用纯文本
	pinned   bool
	opened   uint64
	modified uint64
}

// note 是打开的笔记：库里的文档 + 编辑器里的文档。
type note struct {
	id  string
	doc *richtext.Document
	// save 是库里的原文（保存时对比，避免没动也写盘）。
	opened time.Time
}

type app struct {
	store      *fdl.Store
	entries    []entry
	cur        *note
	selected   string
	next       int // 新笔记的临时序号
	dirty      bool
	query      string // 搜索词
	lastReload time.Time
}

func (a *app) reload() {
	list, err := a.store.List()
	if err != nil {
		log.Println("fdl-app: 列表失败：", err)
		return
	}
	a.entries = a.entries[:0]
	for _, e := range list {
		if e.Meta.Folder != nil {
			continue
		}
		a.entries = append(a.entries, entry{
			id: e.ID, title: fdl.DisplayTitle(e.Title), body: e.Body,
			pinned: e.Meta.Pinned, opened: e.Meta.Opened, modified: e.Meta.Modified,
		})
	}
}

// sorted 是过滤后的列表，按 quiet.md 的侧栏顺序：置顶在前，其余最近
// 打开的在前（未打开过的按修改时间）。
func (a *app) sorted() (pinned, others []entry) {
	for _, e := range a.filtered() {
		if e.pinned {
			pinned = append(pinned, e)
		} else {
			others = append(others, e)
		}
	}
	key := func(e entry) uint64 {
		if e.opened > 0 {
			return e.opened
		}
		return e.modified
	}
	sort.SliceStable(pinned, func(i, j int) bool { return key(pinned[i]) > key(pinned[j]) })
	sort.SliceStable(others, func(i, j int) bool { return key(others[i]) > key(others[j]) })
	return
}

// filtered 是按搜索词过滤后的列表。
func (a *app) filtered() []entry {
	q := strings.ToLower(strings.TrimSpace(a.query))
	if q == "" {
		return a.entries
	}
	var out []entry
	for _, e := range a.entries {
		if strings.Contains(strings.ToLower(e.title), q) || strings.Contains(strings.ToLower(e.body), q) {
			out = append(out, e)
		}
	}
	return out
}

// open 打开一篇笔记。
func (a *app) open(id string) {
	doc, err := a.store.Load(id)
	if err != nil {
		log.Println("fdl-app: 打开失败：", err)
		return
	}
	a.store.Touch(id)
	a.reload()
	a.lastReload = time.Now()
	a.cur = &note{id: id, opened: time.Now()}
	// 文档标题作为首段 H1 进编辑器（保存时剥离回去），标题因此可编辑。
	text := fdlToRichText(&doc)
	if title := strings.TrimSpace(doc.Title.Text); title != "" {
		paras := []richtext.Paragraph{{Style: richtext.ParagraphStyle{Heading: 1}, Runs: []richtext.Run{{Text: title}}}}
		for _, p := range text.Paragraphs() {
			paras = append(paras, p)
		}
		text = richtext.TextOf(paras...)
	}
	a.cur.doc = richtext.NewDocument(text)
	a.dirty = false
}

// add 新建一篇空笔记。
func (a *app) add() *note {
	a.next++
	d := fdl.NewDocument()
	id, err := a.store.CreateFrom(&d)
	if err != nil {
		log.Println("fdl-app: 新建失败：", err)
		return a.cur
	}
	a.reload()
	a.cur = &note{id: id, opened: time.Now()}
	a.cur.doc = richtext.NewDocument(richtext.TextOf(richtext.Paragraph{Style: richtext.ParagraphStyle{Heading: 1}}))
	a.selected = id
	a.dirty = true
	return a.cur
}

// save 把当前笔记写回库。
func (a *app) save() {
	if a.cur == nil || !a.dirty {
		return
	}
	doc, err := a.store.Load(a.cur.id)
	if err != nil {
		log.Println("fdl-app: 保存前读取失败：", err)
		return
	}
	title := ""
	body := a.cur.doc.Text()
	// 第一段若是标题样式，作为文档标题。
	for i, para := range body.Paragraphs() {
		if i == 0 && para.Style.Heading == 1 {
			for _, r := range para.Runs {
				title += r.Text
			}
		}
	}
	if strings.TrimSpace(title) == "" {
		title = "未命名文档"
	}
	doc.Title = fdl.Plain(title)
	doc.Blocks = richToFdl(body)
	if _, err := a.store.Save(a.cur.id, &doc); err != nil {
		log.Println("fdl-app: 保存失败：", err)
		return
	}
	a.dirty = false
	a.reload()
	for i := range a.entries {
		if a.entries[i].id == a.cur.id {
			a.entries[i].title = fdl.DisplayTitle(title)
		}
	}
}

// togglePin 置顶/取消置顶当前笔记。
func (a *app) togglePin() {
	if a.cur == nil {
		return
	}
	pinned := false
	for _, e := range a.entries {
		if e.id == a.cur.id {
			pinned = e.pinned
		}
	}
	if err := a.store.SetPinned(a.cur.id, !pinned); err != nil {
		log.Println("fdl-app: 置顶失败：", err)
		return
	}
	a.reload()
	a.lastReload = time.Now()
}

// syncSelection 切换笔记：选中变了先保存当前，再打开新选的。
func (a *app) syncSelection() {
	if a.selected != "" && (a.cur == nil || a.selected != a.cur.id) {
		a.save()
		a.open(a.selected)
	}
}

// del 把笔记移入回收站并清空编辑区。
func (a *app) del(id string) {
	if a.cur != nil && a.cur.id == id {
		a.cur = nil
		a.dirty = false
	}
	if err := a.store.Delete(id); err != nil {
		log.Println("fdl-app: 删除失败：", err)
	}
	a.reload()
	if len(a.entries) > 0 {
		a.open(a.entries[0].id)
		a.selected = a.entries[0].id
	}
}

func (a *app) view(c *ui.Context) {
	t := c.Theme()
	// 外部改动（Quiet.md / fdl-sync / MCP 写库）节流刷新列表；编辑中不动。
	if !a.dirty && time.Since(a.lastReload) > 5*time.Second {
		a.reload()
		a.lastReload = time.Now()
	}
	a.syncSelection()
	if c.Shortcut(ui.Cmd, ui.KeyN) {
		a.add()
	}
	if c.Shortcut(ui.Cmd, ui.KeyS) {
		a.save()
	}
	if a.cur != nil && c.Shortcut(ui.Cmd, ui.KeyBackspace) {
		a.del(a.cur.id)
	}
	if a.cur != nil && c.Shortcut(ui.Cmd, ui.KeyP) {
		a.togglePin()
	}
	if a.cur == nil && len(a.entries) > 0 {
		a.open(a.entries[0].id)
		a.selected = a.entries[0].id
	}
	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		// 侧栏：标题 + 新建 + 列表。
		ui.Column(c).Width(240).Shrink(0).PaddingY(12).Gap(8).Background(t.Surface).Children(func() {
			ui.Row(c).Padding(0, 16).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "Quiet.md").Bold().Grow(1)
				if ui.Button(c, "＋").Label("新建笔记（⌘N）").Clicked() {
					a.add()
				}
			})
			ui.Row(c).Padding(0, 12).Children(func() {
				ui.TextInput(c, &a.query).Placeholder("搜索…").Grow(1).Label("搜索笔记")
			})
			ui.Row(c).Padding(0, 4).Gap(6).Children(func() {
				pinLabel := "置顶 ⌘P"
				for _, e := range a.entries {
					if e.id == a.cur.id && e.pinned {
						pinLabel = "取消置顶 ⌘P"
					}
				}
				if ui.Button(c, pinLabel).Clicked() && a.cur != nil {
					a.togglePin()
				}
				if ui.Button(c, "删除 ⌘⌫").Clicked() && a.cur != nil {
					a.del(a.cur.id)
				}
			})
			ui.Sidebar(c, &a.selected, func() {
				pinned, others := a.sorted()
				items := func(list []entry) {
					for _, e := range list {
						ui.SidebarItem(c.Key(e.id), e.id, nil, e.title)
					}
				}
				if len(pinned) > 0 {
					ui.SidebarSection(c, "置顶", nil, func() { items(pinned) })
				}
				ui.SidebarSection(c, "全部笔记", nil, func() { items(others) })
			}).Grow(1).Label("笔记")
		})
		// 正文：工具栏 + 编辑器 + 状态行。
		ui.Column(c).Grow(1).MinWidth(0).Padding(12, 20).Gap(10).Children(func() {
			if a.cur == nil {
				ui.Text(c, "没有笔记").TextColor(t.TextMuted).Grow(1)
				return
			}
			a.toolbar(c, a.cur.doc)
			changed := richtext.Editor(c.Key(a.cur.id), a.cur.doc, richtext.EditorOptions{
				Label:       "笔记",
				Placeholder: "标题…",
				// 行内对象（公式/附件/提及）的样式：灰底等宽小胶囊。
				TagStyle: func(tag string, s *ui.Span) {
					s.Background = t.Surface
					s.Color = t.TextMuted
					s.Radius = 4
					s.PaddingX, s.PaddingY = 4, 1
					if strings.HasPrefix(tag, "<latex") {
						s.Color = t.Accent
					}
				},
			}).Grow(1).Changed()
			if changed {
				a.dirty = true
			}
			status := "编辑中"
			if !a.dirty {
				status = "已保存"
			}
			words := len(strings.FieldsFunc(a.cur.doc.Text().PlainText(), func(r rune) bool { return unicode.IsSpace(r) }))
			ui.Textf(c, "%s · %d 字 · fdl-go %s", status, words, fdl.Version).FontSize(12).TextColor(t.TextMuted)
		})
	})
}

// toolbar 是格式工具栏（样式选择 + 行内格式 + 列表）。
func (a *app) toolbar(c *ui.Context, doc *richtext.Document) {
	t := c.Theme()
	f := doc.Formatting()
	button := func(label, tip string, on bool, run func()) ui.Element {
		b := ui.Button(c, label).KeepFocus().Padding(4, 9).MinWidth(30).Tooltip(tip)
		if on {
			b.Background(t.Accent.Alpha(0.18))
		}
		if b.Clicked() {
			run()
		}
		return b
	}
	divider := func() { ui.Box(c).Width(1).Height(20).Background(t.Border) }
	ui.Row(c).Gap(4).Wrap().AlignItems(ui.Center).Children(func() {
		style := map[int]string{0: "正文", 1: "标题", 2: "小标题", 3: "小小标题"}[min(f.Paragraph.Heading, 3)]
		if f.ParagraphsMixed {
			style = ""
		}
		if ui.Select(c, &style, []string{"标题", "小标题", "小小标题", "正文"}).KeepFocus().Width(110).Label("段落样式").Changed() {
			doc.SetHeading(map[string]int{"标题": 1, "小标题": 2, "小小标题": 3, "正文": 0}[style])
		}
		divider()
		button("B", "加粗 ⌘B", f.Bold == richtext.FormatOn, doc.ToggleBold).Bold()
		button("I", "斜体 ⌘I", f.Italic == richtext.FormatOn, doc.ToggleItalic).Italic()
		button("U", "下划线 ⌘U", f.Underline == richtext.FormatOn, doc.ToggleUnderline).Underline()
		button("S", "删除线 ⇧⌘X", f.Strikethrough == richtext.FormatOn, doc.ToggleStrikethrough).Strikethrough()
		button("</>", "代码 ⌘E", f.Code == richtext.FormatOn, doc.ToggleCode)
		divider()
		button("•", "无序列表 ⇧⌘8", f.Paragraph.List == richtext.Bullets, func() { doc.ToggleList(richtext.Bullets) })
		button("1.", "有序列表 ⇧⌘7", f.Paragraph.List == richtext.Numbers, func() { doc.ToggleList(richtext.Numbers) })
		button("☐", "待办 ⇧⌘L", f.Paragraph.List == richtext.Tasks, func() { doc.ToggleList(richtext.Tasks) })
		button("❝", "引用", f.Paragraph.Quote, doc.ToggleQuote)
		button("{}", "代码块", f.Paragraph.CodeBlock, doc.ToggleCodeBlock)
		divider()
		button("⌫", "减少缩进", f.Paragraph.Indent > 0, func() { doc.Indent(-1) })
		button("⇥", "增加缩进", true, func() { doc.Indent(1) })
	})
}

func main() {
	root := os.Getenv("QUIET_MD_HOME")
	if root == "" {
		root = fdl.RootDir()
	}
	store, err := fdl.OpenStoreAt(root)
	if err != nil {
		log.Fatal("fdl-app: ", err)
	}
	a := &app{store: store}
	a.reload()

	mygo.App.WhenReady(func() {
		mygo.App.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
			{Role: mygo.RoleAppMenu},
			{Role: mygo.RoleFileMenu},
			{Role: mygo.RoleEditMenu},
			{Role: mygo.RoleFormatMenu},
			{Role: mygo.RoleWindowMenu},
		}))
		mygo.NewWindow(mygo.WindowOptions{
			Title:    "Quiet.md",
			Width:    1000,
			Height:   680,
			MinWidth: 620, MinHeight: 420,
			StateKey: "fdl-app",
			Content:  ui.View(a.view),
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
