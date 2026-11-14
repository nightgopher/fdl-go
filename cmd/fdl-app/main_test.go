package main

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui/richtext"

	fdl "github.com/nightgopher/fdl-go"
)

// 切换笔记：选中变化触发保存当前 + 打开新选（用户报告的 bug 的回归测试）。
func TestSwitchNotes(t *testing.T) {
	root := t.TempDir()
	store, err := fdl.OpenStoreAt(root)
	if err != nil {
		t.Fatal(err)
	}
	a := &app{store: store}
	// 两篇笔记。
	d1 := fdl.NewDocument()
	d1.Title = fdl.Plain("甲")
	id1, _ := store.CreateFrom(&d1)
	d2 := fdl.NewDocument()
	d2.Title = fdl.Plain("乙")
	d2.Blocks = fdl.MarkdownToBlocks("乙内容", fdl.Off{})
	id2, _ := store.CreateFrom(&d2)
	a.reload()
	a.selected = id1
	a.syncSelection()
	if a.cur == nil || a.cur.id != id1 {
		t.Fatalf("首次选中 = %v", a.cur)
	}
	// 在甲里改了内容（dirty）→ 切到乙：甲先保存，乙打开。
	a.cur.doc = richtext.NewDocument(richtext.TextOf(
		richtext.Paragraph{Style: richtext.ParagraphStyle{Heading: 1}, Runs: []richtext.Run{{Text: "甲"}}},
		richtext.Paragraph{Runs: []richtext.Run{{Text: "甲改了"}}},
	))
	a.dirty = true
	a.selected = id2
	a.syncSelection()
	if a.cur.id != id2 {
		t.Fatalf("切换后 = %s, want %s", a.cur.id, id2)
	}
	saved, _ := store.Load(id1)
	if !strings.Contains(saved.PlainText(), "甲改了") {
		t.Errorf("切换时没保存甲：%q", saved.PlainText())
	}
	// 乙内容正确载入编辑器（标题进正文 + 正文段）。
	pt := a.cur.doc.Text().PlainText()
	if !strings.Contains(pt, "乙") || !strings.Contains(pt, "乙内容") {
		t.Errorf("乙编辑器内容 = %q", pt)
	}
	// 切回甲：内容是保存过的版本。
	a.selected = id1
	a.syncSelection()
	if a.cur.id != id1 {
		t.Fatalf("切回 = %s", a.cur.id)
	}
	if pt := a.cur.doc.Text().PlainText(); !strings.Contains(pt, "甲改了") {
		t.Errorf("切回甲内容 = %q", pt)
	}
	// 搜索过滤后选中的笔记不在列表里：仍能打开。
	a.query = "乙"
	a.selected = id2
	a.syncSelection()
	if a.cur.id != id2 {
		t.Errorf("过滤态切换失败")
	}
}

// 置顶分组与最近打开排序（对齐 quiet.md 侧栏顺序）。
func TestSortedSidebar(t *testing.T) {
	root := t.TempDir()
	store, _ := fdl.OpenStoreAt(root)
	a := &app{store: store}
	// 三篇：旧打开的、新打开的、置顶的。
	d := fdl.NewDocument()
	d.Title = fdl.Plain("旧")
	old, _ := store.CreateFrom(&d)
	d = fdl.NewDocument()
	d.Title = fdl.Plain("新")
	fresh, _ := store.CreateFrom(&d)
	d = fdl.NewDocument()
	d.Title = fdl.Plain("顶")
	pin, _ := store.CreateFrom(&d)
	store.Touch(old)
	// 让 fresh 更晚打开。
	time.Sleep(1100 * time.Millisecond)
	store.Touch(fresh)
	store.SetPinned(pin, true)
	a.reload()
	pinned, others := a.sorted()
	if len(pinned) != 1 || pinned[0].id != pin {
		t.Errorf("置顶组 = %+v", pinned)
	}
	if len(others) != 2 || others[0].id != fresh || others[1].id != old {
		t.Errorf("其余顺序 = %+v（want 新→旧）", others)
	}
	// 置顶切换。
	a.cur = &note{id: pin}
	a.togglePin()
	if _, others2 := a.sorted(); len(others2) != 3 {
		t.Errorf("取消置顶后其余 = %d", len(others2))
	}
}
