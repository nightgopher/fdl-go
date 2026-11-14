package main

import (
	"strings"
	"testing"

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
