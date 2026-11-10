package notesync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	fdl "github.com/nightgopher/fdl-go"
)

func newSyncer(t *testing.T) (*Syncer, string, string) {
	t.Helper()
	lib := t.TempDir()
	mirror := t.TempDir()
	s, err := New(lib, mirror, false)
	if err != nil {
		t.Fatal(err)
	}
	s.Git = false
	return s, lib, mirror
}

func create(t *testing.T, s *Syncer, title, md string) string {
	t.Helper()
	doc := fdl.NewDocument()
	doc.Title = fdl.Plain(title)
	doc.Blocks = fdl.MarkdownToBlocks(md, fdl.Off{})
	id, err := s.Store.CreateFrom(&doc)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func strp(s string) *string { return &s }

func TestSyncRoundTrip(t *testing.T) {
	s, lib, mirror := newSyncer(t)
	id1 := create(t, s, "第一篇", "# 标题\n\n正文 **加粗**\n")
	create(t, s, "第二篇/带斜杠", "内容二\n")

	if _, err := s.Push(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"第一篇.md", "第二篇 带斜杠.md", sidecar} {
		if _, err := os.Stat(filepath.Join(mirror, want)); err != nil {
			t.Errorf("镜像缺 %s：%v", want, err)
		}
	}
	if got := read(t, filepath.Join(mirror, "第一篇.md")); !strings.Contains(got, "# 标题") || !strings.Contains(got, "**加粗**") {
		t.Errorf("第一篇.md = %q", got)
	}
	_ = lib

	// 库里改标题：push 后文件改名。
	if _, err := s.Store.Rename(id1, "改了名"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Push(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(mirror, "改了名.md")); err != nil {
		t.Errorf("改名后文件没跟上：%v", err)
	}
	if _, err := os.Stat(filepath.Join(mirror, "第一篇.md")); !os.IsNotExist(err) {
		t.Error("旧文件没删")
	}

	// 仓库侧：改 md、加新 md → pull → 库更新/新建。
	if err := os.WriteFile(filepath.Join(mirror, "改了名.md"),
		[]byte("# 网页上改的\n\n- [x] 从 GitHub 来\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mirror, "新笔记.md"),
		[]byte("全新一篇\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pull(); err != nil {
		t.Fatal(err)
	}
	doc, err := s.Store.Load(id1)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Title.Text != "改了名" || doc.PlainText() != "网页上改的\n从 GitHub 来" {
		t.Errorf("拉回后 = %q / %q", doc.Title.Text, doc.PlainText())
	}
	entries, _ := s.Store.List()
	n := 0
	for _, e := range entries {
		if e.Title == "新笔记" {
			n++
			d, _ := s.Store.Load(e.ID)
			if d.PlainText() != "全新一篇" {
				t.Errorf("新笔记内容 = %q", d.PlainText())
			}
		}
	}
	if n != 1 {
		t.Errorf("新笔记导入 %d 次", n)
	}

	// 仓库删文件 → pull → 库移入回收站。
	os.Remove(filepath.Join(mirror, "新笔记.md"))
	if _, err := s.Pull(); err != nil {
		t.Fatal(err)
	}
	entries, _ = s.Store.List()
	for _, e := range entries {
		if e.Title == "新笔记" {
			t.Error("删除没生效")
		}
	}

	// 没变的不重写：再 push 一遍 sidecar 稳定。
	before := read(t, filepath.Join(mirror, sidecar))
	if _, err := s.Push(); err != nil {
		t.Fatal(err)
	}
	if after := read(t, filepath.Join(mirror, sidecar)); before != after {
		t.Error("重复 push 后 sidecar 变了")
	}
}

// sync = pull + push：一条命令收拢两边（仓库侧与库侧）各自的改动。
func TestSyncOneCommand(t *testing.T) {
	s, _, mirror := newSyncer(t)
	create(t, s, "本地写的", "本地内容\n")
	if _, err := s.Push(); err != nil {
		t.Fatal(err)
	}

	// 两边各改各的（不同篇）：仓库改「本地写的」，库新增一篇。
	os.WriteFile(filepath.Join(mirror, "本地写的.md"), []byte("仓库改的\n"), 0o644)
	os.WriteFile(filepath.Join(mirror, "网页来的.md"), []byte("网页内容\n"), 0o644)
	create(t, s, "库新增", "库内容\n")

	stats, err := s.Sync()
	if err != nil {
		t.Fatal(err)
	}
	// 仓库侧 1 篇更新入库 + 1 篇新建入库；库侧 1 篇新建出仓。
	if stats.Changed != 1 || stats.Created != 2 {
		t.Errorf("stats = %+v", stats)
	}
	// 仓库改动进了库。
	st := loadState(mirror)
	doc, err := s.Store.Load(st.Notes["本地写的.md"].ID)
	if err != nil {
		t.Fatal(err)
	}
	if doc.PlainText() != "仓库改的" {
		t.Errorf("本地写的 = %q", doc.PlainText())
	}
	// 库新增的进了仓库；网页来的也在库里。
	if got := read(t, filepath.Join(mirror, "库新增.md")); !strings.Contains(got, "库内容") {
		t.Errorf("库新增.md = %q", got)
	}
	entries, _ := s.Store.List()
	found := 0
	for _, e := range entries {
		if e.Title == "网页来的" {
			found++
		}
	}
	if found != 1 {
		t.Errorf("网页来的入库 %d 次", found)
	}
	// 再 sync 一遍：稳定无变化。
	stats2, err := s.Sync()
	if err != nil {
		t.Fatal(err)
	}
	if stats2.Changed != 0 || stats2.Created != 0 || stats2.Removed != 0 {
		t.Errorf("重复 sync stats = %+v", stats2)
	}
}

func TestSyncAssets(t *testing.T) {
	s, lib, mirror := newSyncer(t)
	// 库内 assets 图片 → push 复制到镜像旁目录。
	os.MkdirAll(filepath.Join(lib, "assets"), 0o755)
	os.WriteFile(filepath.Join(lib, "assets", "photo.png"), []byte("\x89PNG fake"), 0o644)
	doc := fdl.NewDocument()
	doc.Title = fdl.Plain("带图")
	doc.Blocks = []fdl.Block{fdl.NewBlock(fdl.Image{SrcKind: "path", Src: "assets/photo.png", Caption: strp("照片")}, fdl.RichText{})}
	if _, err := s.Store.CreateFrom(&doc); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Push(); err != nil {
		t.Fatal(err)
	}
	md := read(t, filepath.Join(mirror, "带图.md"))
	if !strings.Contains(md, ".assets/") {
		t.Errorf("图片链接没转旁目录：%q", md)
	}
	matches, _ := filepath.Glob(filepath.Join(mirror, "带图.assets", "*"))
	if len(matches) != 1 {
		t.Fatalf("assets 拷贝 = %v", matches)
	}

	// 仓库侧加了新图 → pull → 收进库 assets，src 变库内相对路径。
	// 照片用 push 实际写出的链接（从 md 里取），新图是新加的。
	realPhoto := ""
	for _, line := range strings.Split(md, "\n") {
		if i := strings.Index(line, "](带图.assets/"); i >= 0 {
			if j := strings.LastIndexByte(line[i+2:], ')'); j >= 0 {
				realPhoto = line[i+2 : i+2+j]
			}
		}
	}
	if realPhoto == "" {
		t.Fatalf("push 出的 md 里没有图片链接：%q", md)
	}
	os.WriteFile(filepath.Join(mirror, "带图.assets", "added-1234abcd.png"), []byte("\x89PNG new"), 0o644)
	os.WriteFile(filepath.Join(mirror, "带图.md"),
		[]byte("![照片]("+realPhoto+")\n![新图](带图.assets/added-1234abcd.png)\n"), 0o644)
	if _, err := s.Pull(); err != nil {
		t.Fatal(err)
	}

	entries, _ := s.Store.List()
	for _, e := range entries {
		if e.Title != "带图" {
			continue
		}
		d, _ := s.Store.Load(e.ID)
		if len(d.Blocks) != 2 {
			t.Fatalf("应有图片两块，得 %d", len(d.Blocks))
		}
		for i := range d.Blocks {
			img, ok := d.Blocks[i].Kind.(fdl.Image)
			if !ok {
				t.Fatalf("第 %d 块不是图片：%T", i, d.Blocks[i].Kind)
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
