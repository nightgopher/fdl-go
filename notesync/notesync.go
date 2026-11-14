// Package notesync 在 Quiet.md 文档库与一个 Git 镜像仓库之间双向同步：
// 每篇笔记一个「标题.md」，.fdl-sync.json 记 路径↔id 与内容哈希。
// 供 fdl-sync 命令与 fdl-web（同步按钮/自动同步）共用。
package notesync

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

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

// ------------------------------------------------------------------ push

// Stats 是一次同步的计数与单篇警告（不影响其余笔记）。
type Stats struct {
	Changed  int // 更新
	Created  int // 新建
	Removed  int // 删除/移入回收站
	Warnings []string
}

func push(store *fdl.Store, libRoot, mirror string, st *syncState) (Stats, error) {
	var stats Stats
	entries, err := store.List()
	if err != nil {
		return stats, err
	}
	alive := map[string]bool{} // 镜像路径
	for _, e := range entries {
		if e.Meta.Folder != nil {
			continue
		}
		path, changed, isNew, err := pushOne2(store, libRoot, mirror, e, st)
		if err != nil {
			stats.Warnings = append(stats.Warnings, fmt.Sprintf("跳过 %s：%v", fdl.DisplayTitle(e.Title), err))
			continue
		}
		if isNew {
			stats.Created++
		} else if changed {
			stats.Changed++
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
		stats.Removed++
	}
	return stats, saveState(mirror, st)
}

func pushOne2(store *fdl.Store, libRoot, mirror string, e fdl.Entry, st *syncState) (path string, changed, isNew bool, err error) {
	doc, err := store.Load(e.ID)
	if err != nil {
		return "", false, false, err
	}
	stem := safeName(fdl.DisplayTitle(e.Title))
	// 标题冲突：-2、-3…（已占路径且不是自己的）。
	path = stem + ".md"
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
		Copied:      copiedFromMirror(libRoot, mirror),
	}
	out := fdl.WriteFileWith(&doc, front, opts)
	// 库内图片/附件复制到镜像旁目录。
	for _, f := range out.Files {
		to := filepath.Join(mirror, filepath.FromSlash(f.Rel))
		if _, err := os.Stat(to); err == nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return "", false, false, err
		}
		if err := copyFile(f.Src, to); err != nil {
			return "", false, false, err
		}
	}
	data := []byte(out.Text)
	sum := sha256.Sum256(data)
	if m, ok := st.Notes[path]; ok && m.ID == e.ID && m.SHA == hex.EncodeToString(sum[:]) {
		return path, false, false, nil // 没变
	}
	if err := os.WriteFile(full, data, 0o644); err != nil {
		return "", false, false, err
	}
	// 标题改了：旧路径挪过来。
	for old, m := range st.Notes {
		if m.ID == e.ID && old != path {
			os.Remove(filepath.Join(mirror, old))
			os.RemoveAll(filepath.Join(mirror, strings.TrimSuffix(old, ".md")+".assets"))
			delete(st.Notes, old)
		}
	}
	_, existed := st.Notes[path]
	st.Notes[path] = &mapping{Path: path, ID: e.ID, SHA: hex.EncodeToString(sum[:])}
	if existed {
		return path, true, false, nil
	}
	return path, false, true, nil // 镜像里的新文件（同步统计里算新建）
}

// ------------------------------------------------------------------ pull

func pull(store *fdl.Store, libRoot, mirror string, st *syncState) (Stats, error) {
	var stats Stats
	dirEntries, err := os.ReadDir(mirror)
	if err != nil {
		return stats, err
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
			stats.Warnings = append(stats.Warnings, fmt.Sprintf("%s 的图片：%v", path, err))
		}
		var id string
		if m != nil {
			if _, err := store.Save(m.ID, &doc); err != nil {
				stats.Warnings = append(stats.Warnings, fmt.Sprintf("更新 %s 失败：%v", path, err))
				seen[m.ID] = true
				continue
			}
			id = m.ID
			stats.Changed++
		} else {
			id, err = store.CreateFrom(&doc)
			if err != nil {
				stats.Warnings = append(stats.Warnings, fmt.Sprintf("导入 %s 失败：%v", path, err))
				continue
			}
			stats.Created++
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
			stats.Warnings = append(stats.Warnings, fmt.Sprintf("删除 %s 失败：%v", path, err))
			continue
		}
		os.RemoveAll(filepath.Join(mirror, strings.TrimSuffix(path, ".md")+".assets"))
		delete(st.Notes, path)
		stats.Removed++
	}
	return stats, saveState(mirror, st)
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

// copiedFromMirror 建立库内 assets 文件 → 镜像已有拷贝 的映射（按内容
// 匹配），让导出复用已拷过的文件而不是每轮另起 -N 新名。
func copiedFromMirror(libRoot, mirror string) map[string]string {
	hashToRel := map[string]string{}
	des, _ := os.ReadDir(mirror)
	for _, de := range des {
		if !de.IsDir() || !strings.HasSuffix(de.Name(), ".assets") {
			continue
		}
		files, _ := os.ReadDir(filepath.Join(mirror, de.Name()))
		for _, f := range files {
			if f.IsDir() {
				continue
			}
			p := filepath.Join(mirror, de.Name(), f.Name())
			if data, err := os.ReadFile(p); err == nil {
				hashToRel[hashBytes(data)] = pathSlash(de.Name() + "/" + f.Name())
			}
		}
	}
	if len(hashToRel) == 0 {
		return nil
	}
	copied := map[string]string{}
	assetsDir := filepath.Join(libRoot, "assets")
	filepath.WalkDir(assetsDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if data, err := os.ReadFile(p); err == nil {
			if rel, ok := hashToRel[hashBytes(data)]; ok {
				copied[p] = rel
			}
		}
		return nil
	})
	return copied
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func pathSlash(p string) string { return strings.ReplaceAll(p, string(filepath.Separator), "/") }

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

// hasChanges 报告镜像仓库有没有待提交的改动（不是 git 仓库则 false）。
func hasChanges(dir string) bool {
	cmd := exec.Command("git", "-C", dir, "status", "--porcelain")
	out, err := cmd.Output()
	return err == nil && len(out) > 0
}

func gitRun(dir string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	return cmd.Run()
}

// Syncer 绑定一个文档库与一个镜像仓库，串行执行同步。
type Syncer struct {
	Store   *fdl.Store
	LibRoot string
	Mirror  string
	Git     bool
	mu      sync.Mutex
}

// New 打开文档库并返回同步器。
func New(libRoot, mirror string, git bool) (*Syncer, error) {
	store, err := fdl.OpenStoreAt(libRoot)
	if err != nil {
		return nil, err
	}
	return &Syncer{Store: store, LibRoot: libRoot, Mirror: mirror, Git: git}, nil
}

// Pull 仓库 → 库（Git 时先 git pull --ff-only）。
func (s *Syncer) Pull() (Stats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Git {
		gitRun(s.Mirror, "pull", "--ff-only")
	}
	return pull(s.Store, s.LibRoot, s.Mirror, loadState(s.Mirror))
}

// Push 库 → 仓库（Git 时 add/commit/push，没有改动不提交）。
func (s *Syncer) Push() (Stats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stats, err := push(s.Store, s.LibRoot, s.Mirror, loadState(s.Mirror))
	if err != nil {
		return stats, err
	}
	if s.Git && hasChanges(s.Mirror) {
		gitRun(s.Mirror, "add", "-A")
		if gitRun(s.Mirror, "commit", "-q", "-m", "sync: from Quiet.md") == nil {
			gitRun(s.Mirror, "push")
		}
	}
	return stats, nil
}

// Sync 一条龙：先拉（收拢仓库侧改动）再推（上传库状态）。
func (s *Syncer) Sync() (Stats, error) {
	a, err := s.Pull()
	if err != nil {
		return a, err
	}
	b, err := s.Push()
	a.Changed += b.Changed
	a.Created += b.Created
	a.Removed += b.Removed
	a.Warnings = append(a.Warnings, b.Warnings...)
	return a, err
}

// DebugSync 分别返回两侧统计（诊断用）。
func (s *Syncer) DebugSync() (pull, push Stats, err error) {
	pull, err = s.Pull()
	if err != nil {
		return
	}
	push, err = s.Push()
	return
}

// DefaultLibRoot 是文档库默认根目录（fdl.RootDir 的转发，方便调用方只
// import 本包）。
func DefaultLibRoot() string { return fdl.RootDir() }

// Version 返回构建版本号。
func Version() string { return fdl.Version }

// GitInit 在目录里 git init（已存在则不动）。
func GitInit(dir string) { gitRun(dir, "init", "-q") }
