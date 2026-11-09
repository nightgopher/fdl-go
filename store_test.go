package fdl

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustStore(t *testing.T) *Store {
	t.Helper()
	st, err := OpenStoreAt(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func mustCreate(t *testing.T, st *Store, title, body string) string {
	t.Helper()
	doc := NewDocument()
	doc.Title = Plain(title)
	doc.Blocks = MarkdownToBlocks(body, Off{})
	id, err := st.CreateFrom(&doc)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestStoreRoundTrip(t *testing.T) {
	st := mustStore(t)
	doc := NewDocument()
	doc.Title = Plain("测试笔记")
	doc.Blocks = MarkdownToBlocks("# 标题\n\n- 一\n- 二\n", Off{})
	id, err := st.CreateFrom(&doc)
	if err != nil {
		t.Fatal(err)
	}
	if len(id) != 36 { // uuid v4
		t.Errorf("id = %q", id)
	}
	// 磁盘上能读回。
	back, err := st.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if back.Title.Text != "测试笔记" {
		t.Errorf("title = %q", back.Title.Text)
	}
	if text := back.PlainText(); !strings.Contains(text, "标题") || !strings.Contains(text, "一") {
		t.Errorf("body = %q", text)
	}
	// 再读一次（新开一个 Store，走磁盘路径）。
	st2, err := OpenStoreAt(st.Root())
	if err != nil {
		t.Fatal(err)
	}
	entries, err := st2.List()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if e.ID == id {
			found = true
			if e.Title != "测试笔记" {
				t.Errorf("entry title = %q", e.Title)
			}
			if e.Meta.Created == 0 || e.Meta.Modified == 0 {
				t.Errorf("meta = %+v", e.Meta)
			}
		}
	}
	if !found {
		t.Fatal("list 里没有新文档")
	}
}

func TestStoreIndexShape(t *testing.T) {
	st := mustStore(t)
	id := mustCreate(t, st, "形状", "x")
	// index.json 字段形状与上游一致。
	data, err := os.ReadFile(filepath.Join(st.Root(), "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	m := raw[id]
	for _, key := range []string{"created", "modified", "pinned", "opened", "catalogue_closed"} {
		if _, ok := m[key]; !ok {
			t.Errorf("缺少总是写出的字段 %q：%v", key, string(data))
		}
	}
	for _, key := range []string{"starred", "order", "parent", "folder", "markdown", "reading"} {
		if _, ok := m[key]; ok {
			t.Errorf("默认值字段 %q 不应写出", key)
		}
	}
	// format.json 是 base 2。
	fdata, _ := os.ReadFile(filepath.Join(st.Root(), "format.json"))
	if !strings.Contains(string(fdata), `"format": 2`) {
		t.Errorf("format.json = %s", fdata)
	}
	// 第二次保存留下 .bak。
	if _, err := st.Save(id, mustDoc(t, st, id)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(st.Root(), "index.json.bak")); err != nil {
		t.Error("没有 .bak")
	}
}

func mustDoc(t *testing.T, st *Store, id string) *Document {
	t.Helper()
	doc, err := st.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	return &doc
}

func TestStoreCorruptIndexRecoveredFromBak(t *testing.T) {
	st := mustStore(t)
	id := mustCreate(t, st, "一", "a")
	if _, err := st.Save(id, mustDoc(t, st, id)); err != nil {
		t.Fatal(err) // 产生 .bak
	}
	// 把 index.json 写坏。
	if err := os.WriteFile(filepath.Join(st.Root(), "index.json"), []byte("{oops"), 0o644); err != nil {
		t.Fatal(err)
	}
	st2, err := OpenStoreAt(st.Root())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := st2.index[id]; !ok {
		t.Fatal("损坏后没有从 .bak 恢复")
	}
	if len(st2.Warnings) == 0 || !strings.Contains(strings.Join(st2.Warnings, ";"), "已从备份恢复") {
		t.Errorf("warnings = %v", st2.Warnings)
	}
	// 原文件保留为 .corrupt-<秒>。
	matches, _ := filepath.Glob(filepath.Join(st.Root(), "index.json.corrupt-*"))
	if len(matches) != 1 {
		t.Errorf("corrupt 文件 = %v", matches)
	}
}

func TestStoreDeleteAndRename(t *testing.T) {
	st := mustStore(t)
	id := mustCreate(t, st, "待删", "内容")
	if err := st.Delete(id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(st.Path(id)); !os.IsNotExist(err) {
		t.Error("文件没挪进回收站")
	}
	// trash/index.json 有记录；docs 列表不再有。
	tdata, _ := os.ReadFile(filepath.Join(st.Root(), "trash", "index.json"))
	if !strings.Contains(string(tdata), id) {
		t.Errorf("trash index = %s", tdata)
	}
	entries, _ := st.List()
	for _, e := range entries {
		if e.ID == id {
			t.Error("删除后还在 list")
		}
	}
	// Rename 改写标题。
	id2 := mustCreate(t, st, "旧名", "x")
	if _, err := st.Rename(id2, "新名"); err != nil {
		t.Fatal(err)
	}
	doc := mustDoc(t, st, id2)
	if doc.Title.Text != "新名" {
		t.Errorf("title = %q", doc.Title.Text)
	}
}

func TestStoreAdoptsHandPlacedXML(t *testing.T) {
	st := mustStore(t)
	// 手放进 docs/ 的 xml 会被收进列表。
	if err := os.WriteFile(filepath.Join(st.Root(), "docs", "handmade.xml"), []byte("<title>手工</title>\n<p>放进来的</p>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := st.List()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if e.ID == "handmade" {
			found = true
			if e.Title != "手工" || !strings.Contains(e.Body, "放进来的") {
				t.Errorf("entry = %+v", e)
			}
		}
	}
	if !found {
		t.Fatal("手放的 xml 没被收进列表")
	}
	// index 落了盘。
	data, _ := os.ReadFile(filepath.Join(st.Root(), "index.json"))
	if !strings.Contains(string(data), "handmade") {
		t.Error("index 没写")
	}
}

func TestStoreNewerFormatRejected(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "docs"), 0o755)
	os.WriteFile(filepath.Join(root, "format.json"), []byte(`{"format": 99}`), 0o644)
	if _, err := OpenStoreAt(root); err == nil || !strings.Contains(err.Error(), "更新版本") {
		t.Errorf("err = %v", err)
	}
}
