// fdl-sync 让 Quiet.md 文档库与一个 Git 仓库（如 GitHub）双向同步：
//
//	镜像仓库里每篇笔记是一个「标题.md」，图片在「<名>.assets/」旁目录。
//	.fdl-sync.json 记录 路径 ↔ 文档 id 与内容哈希，只在变化时写。
//
//	fdl-sync init  <镜像目录> [-git]   建镜像（可选 git init）
//	fdl-sync push  <镜像目录> [-git]   库 → 镜像（可选 git commit+push）
//	fdl-sync pull  <镜像目录> [-git]   镜像 → 库（可选 git pull --ff-only）
//
// 文档库取 QUIET_MD_HOME（默认 ~/Documents/Quiet.md）。拉回的内容按
// Markdown 解析成 DocxXML 入库；镜像里删掉的文件移入回收站。
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	fdl "github.com/nightgopher/fdl-go"
)

const sidecar = ".fdl-sync.json"

type mapping struct {
	Path string `json:"path"`
	ID   string `json:"id"`
	SHA  string `json:"sha"` // 镜像文件内容哈希（push 后记录）
}

type syncState struct {
	Notes map[string]*mapping `json:"notes"` // 镜像相对路径
}

func loadState(mirror string) *syncState {
	st := &syncState{Notes: map[string]*mapping{}}
	data, err := os.ReadFile(filepath.Join(mirror, sidecar))
	if err == nil {
		json.Unmarshal(data, st)
		if st.Notes == nil {
			st.Notes = map[string]*mapping{}
		}
	}
	return st
}

func saveState(mirror string, st *syncState) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(mirror, sidecar), append(data, '\n'), 0o644)
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	git := fs.Bool("git", false, "顺带执行 git 操作（init: git init；push: add+commit+push；pull: pull --ff-only）")
	fs.Parse(os.Args[2:])
	if fs.NArg() != 1 {
		usage()
	}
	mirror := fs.Arg(0)
	libRoot := os.Getenv("QUIET_MD_HOME")
	if libRoot == "" {
		libRoot = fdl.RootDir()
	}
	store, err := fdl.OpenStoreAt(libRoot)
	if err != nil {
		fatal(err)
	}

	switch cmd {
	case "init":
		if err := os.MkdirAll(mirror, 0o755); err != nil {
			fatal(err)
		}
		if err := push(store, libRoot, mirror, loadState(mirror)); err != nil {
			fatal(err)
		}
		if *git {
			gitRun(mirror, "init", "-q")
		}
		fmt.Printf("已建镜像 %s\n", mirror)
	case "push":
		st := loadState(mirror)
		if err := push(store, libRoot, mirror, st); err != nil {
			fatal(err)
		}
		if *git {
			if gitRun(mirror, "add", "-A") == nil {
				if gitRun(mirror, "commit", "-q", "-m", "sync: from Quiet.md") == nil {
					gitRun(mirror, "push")
				}
			}
		}
	case "pull":
		if *git {
			gitRun(mirror, "pull", "--ff-only")
		}
		st := loadState(mirror)
		if err := pull(store, libRoot, mirror, st); err != nil {
			fatal(err)
		}
	default:
		usage()
	}
}

func usage() {
	fmt.Fprint(os.Stderr, "用法：fdl-sync init|push|pull <镜像目录> [-git]\n")
	os.Exit(2)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "fdl-sync:", err)
	os.Exit(1)
}

func gitRun(dir string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	return cmd.Run()
}

// ------------------------------------------------------------------ push

func push(store *fdl.Store, libRoot, mirror string, st *syncState) error {
	entries, err := store.List()
	if err != nil {
		return err
	}
	alive := map[string]bool{} // 镜像路径
	for _, e := range entries {
		if e.Meta.Folder != nil {
			continue
		}
		path, err := pushOne(store, libRoot, mirror, e, st)
		if err != nil {
			fmt.Fprintf(os.Stderr, "fdl-sync: 跳过 %s：%v\n", e.ID, err)
			continue
		}
		alive[path] = true
	}
	// 库里删了的：镜像里也删。
	var removed []string
	for path := range st.Notes {
		if !alive[path] {
			removed = append(removed, path)
		}
	}
	sort.Strings(removed)
	for _, path := range removed {
		os.Remove(filepath.Join(mirror, path))
		os.RemoveAll(filepath.Join(mirror, strings.TrimSuffix(path, ".md")+".assets"))
		delete(st.Notes, path)
	}
	return saveState(mirror, st)
}

func pushOne(store *fdl.Store, libRoot, mirror string, e fdl.Entry, st *syncState) (string, error) {
	doc, err := store.Load(e.ID)
	if err != nil {
		return "", err
	}
	stem := safeName(fdl.DisplayTitle(e.Title))
	// 标题冲突：-2、-3…（已占路径且不是自己的）。
	path := stem + ".md"
	for n := 2; ; n++ {
		if m, ok := st.Notes[path]; !ok || m.ID == e.ID {
			break
		}
		path = fmt.Sprintf("%s-%d.md", stem, n)
	}
	full := filepath.Join(mirror, path)

	// 库内 .md 文档保留 front matter。
	front := ""
	if raw, err := os.ReadFile(store.Path(e.ID)); err == nil && e.Meta.Markdown {
		front, _ = fdl.FrontMatter(strings.TrimPrefix(string(raw), "\ufeff"))
	}

	// 原样保留没改的块：镜像里已有的内容当 original。
	original := ""
	if old, ok := st.Notes[path]; ok && old.ID == e.ID {
		if data, err := os.ReadFile(full); err == nil {
			original = string(data)
		}
	}
	opts := &fdl.ExportOptions{
		InPlace:     true,
		Base:        mirror,
		LibraryRoot: libRoot,
		Name:        stem,
		Original:    original,
	}
	out := fdl.WriteFileWith(&doc, front, opts)
	// 库内图片/附件复制到镜像旁目录。
	for _, f := range out.Files {
		to := filepath.Join(mirror, filepath.FromSlash(f.Rel))
		if _, err := os.Stat(to); err == nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return "", err
		}
		if err := copyFile(f.Src, to); err != nil {
			return "", err
		}
	}
	data := []byte(out.Text)
	sum := sha256.Sum256(data)
	if m, ok := st.Notes[path]; ok && m.ID == e.ID && m.SHA == hex.EncodeToString(sum[:]) {
		return path, nil // 没变
	}
	if err := os.WriteFile(full, data, 0o644); err != nil {
		return "", err
	}
	// 标题改了：旧路径挪过来。
	for old, m := range st.Notes {
		if m.ID == e.ID && old != path {
			os.Remove(filepath.Join(mirror, old))
			os.RemoveAll(filepath.Join(mirror, strings.TrimSuffix(old, ".md")+".assets"))
			delete(st.Notes, old)
		}
	}
	st.Notes[path] = &mapping{Path: path, ID: e.ID, SHA: hex.EncodeToString(sum[:])}
	return path, nil
}

// ------------------------------------------------------------------ pull

func pull(store *fdl.Store, libRoot, mirror string, st *syncState) error {
	dirEntries, err := os.ReadDir(mirror)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, de := range dirEntries {
		name := de.Name()
		if de.IsDir() || !strings.HasSuffix(name, ".md") || strings.HasPrefix(name, ".") {
			continue
		}
		path := name
		data, err := os.ReadFile(filepath.Join(mirror, path))
		if err != nil {
			continue
		}
		sum := sha256.Sum256(data)
		sha := hex.EncodeToString(sum[:])
		m := st.Notes[path]
		if m != nil && m.SHA == sha {
			seen[m.ID] = true
			continue // 与上次 push 相同：不动库
		}
		title := strings.TrimSuffix(name, ".md")
		doc := fdl.ParseFile(string(data), title, mirror)
		// 镜像里的图片复制进库的 assets/，源改成库内相对路径。
		if err := adoptAssets(&doc, libRoot, mirror); err != nil {
			fmt.Fprintf(os.Stderr, "fdl-sync: %s 的图片：%v\n", path, err)
		}
		var id string
		if m != nil {
			if _, err := store.Save(m.ID, &doc); err != nil {
				fmt.Fprintf(os.Stderr, "fdl-sync: 更新 %s 失败：%v\n", path, err)
				seen[m.ID] = true
				continue
			}
			id = m.ID
		} else {
			id, err = store.CreateFrom(&doc)
			if err != nil {
				fmt.Fprintf(os.Stderr, "fdl-sync: 导入 %s 失败：%v\n", path, err)
				continue
			}
		}
		st.Notes[path] = &mapping{Path: path, ID: id, SHA: sha}
		seen[id] = true
	}
	// 镜像里删掉的（有过映射且这次没见到）：移入回收站。
	var removed []string
	for path, m := range st.Notes {
		if !seen[m.ID] {
			removed = append(removed, path)
		}
	}
	sort.Strings(removed)
	for _, path := range removed {
		id := st.Notes[path].ID
		if err := store.Delete(id); err != nil {
			fmt.Fprintf(os.Stderr, "fdl-sync: 删除 %s 失败：%v\n", path, err)
			continue
		}
		os.RemoveAll(filepath.Join(mirror, strings.TrimSuffix(path, ".md")+".assets"))
		delete(st.Notes, path)
	}
	return saveState(mirror, st)
}

// adoptAssets 把镜像目录下的本地图片/附件收进库的 assets/，块源改成
// 库内相对路径。
func adoptAssets(doc *fdl.Document, libRoot, mirror string) error {
	var walk func(blocks []fdl.Block) error
	walk = func(blocks []fdl.Block) error {
		for i := range blocks {
			b := &blocks[i]
			if err := walk(b.Children); err != nil {
				return err
			}
			img, ok := b.Kind.(fdl.Image)
			if !ok {
				continue
			}
			src := img.Src
			if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") || strings.HasPrefix(src, "data:") {
				continue
			}
			p := strings.TrimPrefix(src, "file://")
			if !filepath.IsAbs(p) {
				continue // 已是库内相对路径
			}
			rel, err := filepath.Rel(mirror, p)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				continue // 库外的绝对路径（本机文件），保持原样
			}
			data, err := os.ReadFile(p)
			if err != nil {
				continue // 引用的文件不在（仓库里删了？）：保持原样
			}
			name := uniqueName(libRoot, filepath.Base(p), data)
			dst := filepath.Join(libRoot, "assets", name)
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(dst, data, 0o644); err != nil {
				return err
			}
			img.Src = "assets/" + name
			b.Kind = img
		}
		return nil
	}
	return walk(doc.Blocks)
}

// uniqueName 内容相同复用同名，否则加短哈希。
func uniqueName(libRoot, base string, data []byte) string {
	sum := sha256.Sum256(data)
	h := hex.EncodeToString(sum[:8])
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	name := stem + "-" + h + ext
	if _, err := os.Stat(filepath.Join(libRoot, "assets", name)); err == nil {
		return name
	}
	return name
}

// ------------------------------------------------------------------ 杂项

// safeName 把标题变成文件名（去掉路径分隔符与控制字符）。
func safeName(title string) string {
	var b strings.Builder
	for _, r := range title {
		switch {
		case r < 0x20:
		case strings.ContainsRune(`/\:*?"<>|`, r):
			b.WriteRune(' ')
		case r == '.' && b.Len() == 0:
		default:
			b.WriteRune(r)
		}
	}
	s := strings.TrimSpace(b.String())
	s = strings.TrimLeft(s, ".")
	if s == "" {
		s = "未命名"
	}
	if len(s) > 80 {
		s = s[:80]
	}
	return s
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
