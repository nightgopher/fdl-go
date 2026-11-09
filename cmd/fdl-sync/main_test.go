package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	fdl "github.com/nightgopher/fdl-go"
)

func newLib(t *testing.T) (*fdl.Store, string) {
	t.Helper()
	root := t.TempDir()
	st, err := fdl.OpenStoreAt(root)
	if err != nil {
		t.Fatal(err)
	}
	return st, root
}

func create(t *testing.T, st *fdl.Store, title, md string) string {
	t.Helper()
	doc := fdl.NewDocument()
	doc.Title = fdl.Plain(title)
	doc.Blocks = fdl.MarkdownToBlocks(md, fdl.Off{})
	id, err := st.CreateFrom(&doc)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func strp(s string) *string { return &s }

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestSyncRoundTrip(t *testing.T) {
	st, lib := newLib(t)
	mirror := t.TempDir()
	id1 := create(t, st, "第一篇", "# 标题\n\n正文 **加粗**\n")
	create(t, st, "第二篇/带斜杠", "内容二\n")

	state := loadState(mirror)
	if err := push(st, lib, mirror, state); err != nil {
		t.Fatal(err)
	}
	// 镜像结构：人可读的 md + sidecar。
	for _, want := range []string{"第一篇.md", "第二篇 带斜杠.md", sidecar} {
		if _, err := os.Stat(filepath.Join(mirror, want)); err != nil {
			t.Errorf("镜像缺 %s：%v", want, err)
		}
	}
	if got := read(t, filepath.Join(mirror, "第一篇.md")); !strings.Contains(got, "# 标题") || !strings.Contains(got, "**加粗**") {
		t.Errorf("第一篇.md = %q", got)
	}

	// 库里改标题：push 后文件改名。
	if _, err := st.Rename(id1, "改了名"); err != nil {
		t.Fatal(err)
	}
	state = loadState(mirror)
	if err := push(st, lib, mirror, state); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(mirror, "改了名.md")); err != nil {
		t.Errorf("改名后文件没跟上：%v", err)
	}
	if _, err := os.Stat(filepath.Join(mirror, "第一篇.md")); !os.IsNotExist(err) {
		t.Error("旧文件没删")
	}

	// GitHub 侧编辑：改 md → pull → 库更新；新 md → 新笔记。
	if err := os.WriteFile(filepath.Join(mirror, "改了名.md"),
		[]byte("# 网页上改的\n\n- [x] 从 GitHub 来\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mirror, "新笔记.md"),
		[]byte("全新一篇\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	state = loadState(mirror)
	if err := pull(st, lib, mirror, state); err != nil {
		t.Fatal(err)
	}
	doc, err := st.Load(id1)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Title.Text != "改了名" || doc.PlainText() != "网页上改的\n从 GitHub 来" {
		t.Errorf("拉回后 = %q / %q", doc.Title.Text, doc.PlainText())
	}
	entries, _ := st.List()
	n := 0
	for _, e := range entries {
		if e.Title == "新笔记" {
			n++
			d, _ := st.Load(e.ID)
			if d.PlainText() != "全新一篇" {
				t.Errorf("新笔记内容 = %q", d.PlainText())
			}
		}
	}
	if n != 1 {
		t.Errorf("新笔记导入 %d 次", n)
	}

	// 镜像删文件 → pull → 库移入回收站。
	os.Remove(filepath.Join(mirror, "新笔记.md"))
	state = loadState(mirror)
	if err := pull(st, lib, mirror, state); err != nil {
		t.Fatal(err)
	}
	entries, _ = st.List()
	for _, e := range entries {
		if e.Title == "新笔记" {
			t.Error("删除没生效")
		}
	}
	// 没变的不再重写（modified 不动）。
	state = loadState(mirror)
	if err := push(st, lib, mirror, state); err != nil {
		t.Fatal(err)
	}
}

func TestSyncAssets(t *testing.T) {
	st, lib := newLib(t)
	mirror := t.TempDir()
	// 库内 assets 图片 → push 复制到镜像旁目录。
	os.MkdirAll(filepath.Join(lib, "assets"), 0o755)
	os.WriteFile(filepath.Join(lib, "assets", "photo.png"), []byte("\x89PNG fake"), 0o644)
	doc := fdl.NewDocument()
	doc.Title = fdl.Plain("带图")
	doc.Blocks = []fdl.Block{fdl.NewBlock(fdl.Image{SrcKind: "path", Src: "assets/photo.png", Caption: strp("照片")}, fdl.RichText{})}
	id, err := st.CreateFrom(&doc)
	if err != nil {
		t.Fatal(err)
	}
	_ = id

	state := loadState(mirror)
	if err := push(st, lib, mirror, state); err != nil {
		t.Fatal(err)
	}
	md := read(t, filepath.Join(mirror, "带图.md"))
	if !strings.Contains(md, ".assets/") {
		t.Errorf("图片链接没转旁目录：%q", md)
	}
	// 旁目录里有拷贝。
	matches, _ := filepath.Glob(filepath.Join(mirror, "带图.assets", "*"))
	if len(matches) != 1 {
		t.Fatalf("assets 拷贝 = %v", matches)
	}

	// GitHub 侧加了新图 → pull → 收进库 assets，src 变库内相对路径。
	// 照片用 push 实际写出的链接（从 md 里取），新图是新加的。
	realPhoto := ""
	for _, line := range strings.Split(read(t, filepath.Join(mirror, "带图.md")), "\n") {
		if i := strings.Index(line, "](带图.assets/"); i >= 0 {
			if j := strings.LastIndexByte(line[i+2:], ')'); j >= 0 {
				realPhoto = line[i+2 : i+2+j]
			}
		}
	}
	if realPhoto == "" {
		t.Fatalf("push 出的 md 里没有图片链接：%q", read(t, filepath.Join(mirror, "带图.md")))
	}
	os.WriteFile(filepath.Join(mirror, "带图.assets", "added-1234abcd.png"), []byte("\x89PNG new"), 0o644)
	os.WriteFile(filepath.Join(mirror, "带图.md"),
		[]byte("![照片]("+realPhoto+")\n![新图](带图.assets/added-1234abcd.png)\n"), 0o644)
	state = loadState(mirror)
	if err := pull(st, lib, mirror, state); err != nil {
		t.Fatal(err)
	}
	entries, _ := st.List()
	for _, e := range entries {
		if e.Title != "带图" {
			continue
		}
		doc, _ := st.Load(e.ID)
		if len(doc.Blocks) != 2 {
			t.Fatalf("应有图片两块，得 %d", len(doc.Blocks))
		}
		for i := range doc.Blocks {
			img, ok := doc.Blocks[i].Kind.(fdl.Image)
			if !ok {
				t.Fatalf("第 %d 块不是图片：%T", i, doc.Blocks[i].Kind)
			}
			if !strings.HasPrefix(img.Src, "assets/") {
				t.Errorf("src[%d] = %q", i, img.Src)
			}
			if _, err := os.Stat(filepath.Join(lib, img.Src)); err != nil {
				t.Errorf("库内没有 %s：%v", img.Src, err)
			}
		}
	}
}
