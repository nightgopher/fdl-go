package fdl

// 磁盘文档库（对拍 store.rs@db8d172 的引擎相关子集）：每篇文档一个
// DocxXML 文件加一个小 JSON 索引，目录布局与 Quiet.md 互通。
//
// 兼容点：index.json/trash/index.json 的字段与缺省值、format.json 版本
// （BASE 2 / FORMAT 3）、原子写（tmp+rename+fsync）、.bak 备份与损坏恢
// 复、.lock flock 写锁。上游的 GUI 侧能力（settings.json、file_history、
// 本机文件与库内 .md 文档的写入、回收站恢复/清理、文件夹 CRUD）不在
// 此子集；文件夹条目与 markdown/path 等 Meta 字段会原样往返。

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

// Meta 是 index.json 里一篇文档的元信息。字段与上游 serde 形状一致：
// 总是写出 created/modified/pinned/opened/catalogue_closed，其余按默认
// 值省略；读取时缺省值与上游相同（catalogue_closed 缺省 true 等）。
type Meta struct {
	Folder    *string `json:"folder,omitempty"`
	RootOrder *uint64 `json:"root_order,omitempty"`
	Created   uint64  `json:"created"`
	Modified  uint64  `json:"modified"`
	Pinned    bool    `json:"pinned"`
	Opened    uint64  `json:"opened"`
	// Width 是页宽："wide"（1020）、"full"，空串是默认 820。
	Width           string   `json:"width,omitempty"`
	CatalogueClosed *bool    `json:"catalogue_closed,omitempty"` // 缺省 true
	Parent          *string  `json:"parent,omitempty"`
	Order           uint64   `json:"order,omitempty"`
	Starred         uint64   `json:"starred,omitempty"`
	Reading         bool     `json:"reading,omitempty"`
	Info            *InfoRow `json:"info,omitempty"` // 缺省 {false,false,true}
	RefsHidden      bool     `json:"refs_hidden,omitempty"`
	Markdown        bool     `json:"markdown,omitempty"`
	Path            *string  `json:"path,omitempty"`
	Name            *string  `json:"name,omitempty"`
}

// CatalogueClosedOf 是 catalogue_closed 的有效值（缺省 true）。
func (m *Meta) CatalogueClosedOf() bool { return m.CatalogueClosed == nil || *m.CatalogueClosed }

// InfoRow 是标题下的文档信息行显示哪些项。
type InfoRow struct {
	Hidden   bool `json:"hidden"`
	Created  bool `json:"created"`
	Modified bool `json:"modified"`
}

// Trashed 是回收站里一篇文档的记录（trash/index.json）。
type Trashed struct {
	Meta    Meta    `json:"meta"`
	Deleted uint64  `json:"deleted"`
	Root    *string `json:"root,omitempty"` // 随哪个页面一起删的
}

// Entry 是 list 给出的一行。
type Entry struct {
	ID        string
	Meta      Meta
	Title     string
	Icon      *string
	Body      string
	Reminders []Reminder
	Links     []DocLink
}

// LibraryFormat 是当前目录布局版本；BaseLibraryFormat 是没有 .md 文档
// 和本机文件时的标记（旧版仍能打开）。
const (
	LibraryFormat     = 3
	BaseLibraryFormat = 2
)

type formatMarker struct {
	Format uint32 `json:"format"`
}

// Store 是一个打开的文档库。
type Store struct {
	root     string
	index    map[string]Meta
	trash    map[string]Trashed
	Warnings []string // 打开库时发现的问题，给上层提示一次
}

// RootDir 是文档库根目录：QUIET_MD_HOME，否则 ~/Documents/Quiet.md。
func RootDir() string {
	if home := os.Getenv("QUIET_MD_HOME"); home != "" {
		return home
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, "Documents", "Quiet.md")
}

// OpenStore 打开默认位置的文档库。
func OpenStore() (*Store, error) { return OpenStoreAt(RootDir()) }

// OpenStoreAt 打开（或创建）root 处的文档库。
func OpenStoreAt(root string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		return nil, err
	}
	st := &Store{root: root, index: map[string]Meta{}, trash: map[string]Trashed{}}
	markerPath := filepath.Join(root, "format.json")
	if data, err := os.ReadFile(markerPath); err == nil {
		var m formatMarker
		if err := json.Unmarshal(data, &m); err == nil && m.Format > LibraryFormat {
			return nil, fmt.Errorf("这个文档库由更新版本的 Quiet.md 创建，请升级后再打开")
		}
	} else {
		if err := writeJSONAtomic(markerPath, formatMarker{Format: BaseLibraryFormat}); err != nil {
			return nil, err
		}
	}
	var err error
	if st.index, err = loadJSON[map[string]Meta](filepath.Join(root, "index.json"), "文档索引", &st.Warnings); err != nil {
		return nil, err
	}
	if st.trash, err = loadJSON[map[string]Trashed](filepath.Join(root, "trash", "index.json"), "回收站索引", &st.Warnings); err != nil {
		return nil, err
	}
	if st.index == nil {
		st.index = map[string]Meta{}
	}
	if st.trash == nil {
		st.trash = map[string]Trashed{}
	}
	return st, nil
}

// Root 是库根目录。
func (st *Store) Root() string { return st.root }

// Path 是 id 的文档文件。
func (st *Store) Path(id string) string {
	if m, ok := st.index[id]; ok {
		if m.Path != nil {
			return *m.Path
		}
		if m.Markdown {
			return filepath.Join(st.root, "docs", id+".md")
		}
	}
	return filepath.Join(st.root, "docs", id+".xml")
}

func (st *Store) isMarkdown(id string) bool {
	m, ok := st.index[id]
	return ok && m.Markdown
}

// markdownTitle 是 Markdown 文档的标题：库内的名字，库外文件的文件名。
func (st *Store) markdownTitle(id string) string {
	m, ok := st.index[id]
	if !ok {
		return ""
	}
	if m.Path != nil {
		base := filepath.Base(*m.Path)
		return strings.TrimSuffix(base, filepath.Ext(base))
	}
	if m.Name != nil {
		return *m.Name
	}
	return ""
}

func (st *Store) base(id string) string {
	if m, ok := st.index[id]; ok && m.Path != nil {
		return filepath.Dir(*m.Path)
	}
	return ""
}

func (st *Store) parseDoc(id, text string) (Document, error) {
	if !st.isMarkdown(id) {
		return Parse(text)
	}
	return ParseFile(text, st.markdownTitle(id), st.base(id)), nil
}

// List 列出磁盘上的全部文档（手放进 docs/ 的 .xml/.md 也会被收进来），
// 末尾按 id 序附上文件夹条目。
func (st *Store) List() ([]Entry, error) {
	release, err := st.beginWrite()
	if err != nil {
		return nil, fmt.Errorf("无法锁定文档库：%w", err)
	}
	defer release()
	dirty := false
	now64 := nowSeconds()
	var out []Entry
	dirEntries, err := os.ReadDir(filepath.Join(st.root, "docs"))
	if err != nil {
		return nil, err
	}
	for _, e := range dirEntries {
		if e.IsDir() {
			continue
		}
		id := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		ext := strings.TrimPrefix(filepath.Ext(e.Name()), ".")
		markdown := ext == "md"
		if ext != "xml" && !markdown {
			continue
		}
		p := filepath.Join(st.root, "docs", e.Name())
		if markdown && !st.isMarkdown(id) {
			// 手放进 docs/ 的 .md。
			st.index[id] = Meta{Created: now64, Modified: now64, Markdown: true, Name: strPtr("未命名")}
			dirty = true
		}
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		doc, perr := st.parseDoc(id, string(data))
		if perr != nil {
			doc = NewDocument()
		}
		meta, seen := st.index[id]
		if !seen {
			mod := now64
			if info, err := e.Info(); err == nil {
				if m := info.ModTime().Unix(); m >= 0 {
					mod = uint64(m)
				}
			}
			meta = Meta{Created: mod, Modified: mod}
			st.index[id] = meta
			dirty = true
		}
		title := doc.Title.Text
		if meta.Markdown {
			title = st.markdownTitle(id)
		}
		out = append(out, Entry{
			ID: id, Meta: meta, Title: title, Icon: doc.Icon,
			Body: doc.PlainText(), Reminders: doc.Reminders(), Links: doc.DocLinks(),
		})
	}
	// 文件夹按 id 序（上游：HashMap 顺序不稳定，落盘前排序）。
	var folderIDs []string
	for id, m := range st.index {
		if m.Folder != nil {
			folderIDs = append(folderIDs, id)
		}
	}
	sort.Strings(folderIDs)
	for _, id := range folderIDs {
		m := st.index[id]
		out = append(out, Entry{ID: id, Meta: m, Title: *m.Folder})
	}
	if dirty {
		if err := st.saveIndex(); err != nil {
			return out, err
		}
	}
	return out, nil
}

// Load 读一篇文档。
func (st *Store) Load(id string) (Document, error) {
	data, err := os.ReadFile(st.Path(id))
	if err != nil {
		return Document{}, err
	}
	return st.parseDoc(id, string(data))
}

// Save 写一篇文档并更新索引（modified）。
func (st *Store) Save(id string, doc *Document) (Meta, error) {
	release, err := st.beginWrite()
	if err != nil {
		return Meta{}, err
	}
	defer release()
	return st.saveNow(id, doc)
}

func (st *Store) saveNow(id string, doc *Document) (Meta, error) {
	if st.isMarkdown(id) {
		return Meta{}, fmt.Errorf("库内 Markdown 文档暂不支持经引擎写入：%s", id)
	}
	if len(doc.Blocks) == 0 {
		d := *doc
		d.Blocks = []Block{BlockText("")}
		doc = &d
	}
	xmlText := Serialize(doc)
	if err := writeAtomic(st.Path(id), []byte(xmlText)); err != nil {
		return Meta{}, err
	}
	t := nowSeconds()
	m, ok := st.index[id]
	if !ok {
		m = Meta{Created: t, Modified: t}
	}
	m.Modified = t
	st.index[id] = m
	if err := st.saveIndex(); err != nil {
		return m, err
	}
	return m, nil
}

// CreateFrom 把 doc 存成新文档（uuid id）。
func (st *Store) CreateFrom(doc *Document) (string, error) {
	id := uuid.NewString()
	release, err := st.beginWrite()
	if err != nil {
		return "", err
	}
	defer release()
	if _, err := st.saveNow(id, doc); err != nil {
		return "", err
	}
	return id, nil
}

// Rename 重命名未打开的文档：改写盘上的标题。
func (st *Store) Rename(id, title string) (Meta, error) {
	release, err := st.beginWrite()
	if err != nil {
		return Meta{}, err
	}
	defer release()
	if m, ok := st.index[id]; ok && m.Folder != nil {
		previous := st.index[id]
		m.Folder = &title
		m.Modified = nowSeconds()
		st.index[id] = m
		if err := st.saveIndex(); err != nil {
			st.index[id] = previous
			return Meta{}, err
		}
		return m, nil
	}
	doc, err := st.Load(id)
	if err != nil {
		return Meta{}, err
	}
	doc.Title = Plain(title)
	return st.saveNow(id, &doc)
}

// Touch 记录刚打开过。
func (st *Store) Touch(id string) (Meta, bool) {
	release, err := st.beginWrite()
	if err != nil {
		return Meta{}, false
	}
	defer release()
	m, ok := st.index[id]
	if !ok {
		return Meta{}, false
	}
	m.Opened = nowSeconds()
	st.index[id] = m
	if err := st.saveIndex(); err != nil {
		return m, true
	}
	return m, true
}

// Delete 把文档移进回收站（trash/index.json 先落盘，再挪文件）。
func (st *Store) Delete(id string) error {
	release, err := st.beginWrite()
	if err != nil {
		return err
	}
	defer release()
	if m, ok := st.index[id]; ok && m.Path != nil {
		// 本机文件：只从列表拿掉，文件是用户的。
		delete(st.index, id)
		return st.saveIndex()
	}
	trashPath := st.trashPathFor(id, st.index[id])
	if _, err := os.Stat(trashPath); err == nil {
		return fmt.Errorf("回收站已存在同名文档，未覆盖：%s", id)
	}
	if err := os.MkdirAll(filepath.Join(st.root, "trash"), 0o755); err != nil {
		return err
	}
	meta, ok := st.index[id]
	if !ok {
		meta = Meta{}
	}
	trash := make(map[string]Trashed, len(st.trash)+1)
	for k, v := range st.trash {
		trash[k] = v
	}
	trash[id] = Trashed{Meta: meta, Deleted: nowSeconds()}
	if err := saveJSON(filepath.Join(st.root, "trash", "index.json"), trash); err != nil {
		return err
	}
	st.trash = trash
	if meta.Folder == nil {
		if err := os.Rename(st.Path(id), trashPath); err != nil {
			return err
		}
	}
	delete(st.index, id)
	if err := st.saveIndex(); err != nil {
		st.Warnings = append(st.Warnings, "文档已移入回收站，文档索引清理失败："+err.Error())
	}
	return nil
}

func (st *Store) trashPathFor(id string, m Meta) string {
	ext := "xml"
	if m.Markdown {
		ext = "md"
	}
	return filepath.Join(st.root, "trash", id+"."+ext)
}

// SetParent 把文档挂到 parent 下（空串 = 我的文档库顶层）。
func (st *Store) SetParent(id, parent string) (Meta, error) {
	release, err := st.beginWrite()
	if err != nil {
		return Meta{}, err
	}
	defer release()
	m, ok := st.index[id]
	if !ok {
		return Meta{}, fmt.Errorf("没有这篇文档：%s", id)
	}
	if parent == "" {
		m.Parent = nil
	} else {
		p := parent
		m.Parent = &p
	}
	m.Order = nowMilliseconds()
	m.Modified = nowSeconds()
	st.index[id] = m
	return m, st.saveIndex()
}

// SetStarred 收藏/取消收藏，返回收藏时间（0 = 未收藏）。
func (st *Store) SetStarred(id string, on bool) (uint64, error) {
	release, err := st.beginWrite()
	if err != nil {
		return 0, err
	}
	defer release()
	t := uint64(0)
	if on {
		t = nowSeconds()
	}
	if m, ok := st.index[id]; ok {
		m.Starred = t
		st.index[id] = m
	}
	return t, st.saveIndex()
}

// ------------------------------------------------------------------ 基础设施

// beginWrite 拿库的写锁并吸收其他副本写下的索引；返回释放函数。
// 拿不到锁绝不写。
func (st *Store) beginWrite() (func(), error) {
	f, err := os.OpenFile(filepath.Join(st.root, ".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	// 别的副本可能写过：能读就采纳内存外的版本。
	if idx, ok := rereadJSON[map[string]Meta](filepath.Join(st.root, "index.json")); ok {
		if idx == nil {
			idx = map[string]Meta{}
		}
		st.index = idx
	}
	if tr, ok := rereadJSON[map[string]Trashed](filepath.Join(st.root, "trash", "index.json")); ok {
		if tr == nil {
			tr = map[string]Trashed{}
		}
		st.trash = tr
	}
	return func() {
		unix.Flock(int(f.Fd()), unix.LOCK_UN)
		f.Close()
	}, nil
}

func (st *Store) saveIndex() error {
	// catalogue_closed 上游没有 skip：缺省（nil = true）落盘时补成字面量。
	for id, m := range st.index {
		if m.CatalogueClosed == nil {
			v := true
			m.CatalogueClosed = &v
			st.index[id] = m
		}
	}
	return saveJSON(filepath.Join(st.root, "index.json"), st.index)
}

// writeAtomic 经临时文件写：崩溃时磁盘上要么旧内容要么新内容。
func writeAtomic(path string, data []byte) error {
	tmp := filepath.Join(filepath.Dir(path), "."+uuid.NewString()+".tmp")
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	serr := f.Sync()
	cerr := f.Close()
	if werr != nil || serr != nil || cerr != nil {
		os.Remove(tmp)
		return errors.Join(werr, serr, cerr)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		dir.Sync()
		dir.Close()
	}
	return nil
}

func writeJSONAtomic(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, append(data, '\n'))
}

// loadJSON 读 saveJSON 写的 JSON 索引：存在但解析失败时改名为
// <name>.corrupt-<秒> 保留（不覆盖），改用 .bak。
func loadJSON[T any](path, what string, warnings *[]string) (T, error) {
	var zero T
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return zero, nil
		}
		*warnings = append(*warnings, "无法读取"+what+"："+err.Error())
		return zero, nil
	}
	var v T
	if err := json.Unmarshal(data, &v); err == nil {
		return v, nil
	}
	aside := fmt.Sprintf("%s.corrupt-%d", path, nowSeconds())
	os.Rename(path, aside)
	name := filepath.Base(aside)
	if bak, err := os.ReadFile(path + ".bak"); err == nil {
		var bv T
		if json.Unmarshal(bak, &bv) == nil {
			*warnings = append(*warnings, what+"已损坏，已从备份恢复（原文件保留为 "+name+"）")
			return bv, nil
		}
	}
	*warnings = append(*warnings, what+"已损坏且没有可用备份，部分文档的置顶、层级等信息已重置（原文件保留为 "+name+"）")
	return zero, nil
}

// rereadJSON 读另一个副本可能留下的 JSON（此刻能读就返回）。
func rereadJSON[T any](path string) (T, bool) {
	var zero T
	data, err := os.ReadFile(path)
	if err != nil {
		return zero, false
	}
	var v T
	if json.Unmarshal(data, &v) != nil {
		return zero, false
	}
	return v, true
}

// saveJSON 原子替换 JSON 索引，把被替换的版本留作 .bak。
func saveJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if _, err := os.Stat(path); err == nil {
		if old, err := os.ReadFile(path); err == nil {
			if err := writeAtomic(path+".bak", old); err != nil {
				return err
			}
		}
	}
	return writeAtomic(path, data)
}

func nowSeconds() uint64      { return uint64(time.Now().Unix()) }
func nowMilliseconds() uint64 { return uint64(time.Now().UnixMilli()) }

// ------------------------------------------------------------------ 导出路径

// ExportPathIn 返回 src 指向的库内文件路径（解析符号链接、拒绝库外
// 路径与目录）；不存在或越界时 ok 为 false。
func ExportPathIn(root, src string) (string, bool) {
	r, err := canonical(root)
	if err != nil {
		return "", false
	}
	raw := strings.TrimPrefix(src, "file://")
	p := raw
	if !filepath.IsAbs(p) {
		p = filepath.Join(r, raw)
	}
	p, err = canonical(p)
	if err != nil {
		return "", false
	}
	if !withinDir(r, p) {
		return "", false
	}
	fi, err := os.Stat(p)
	if err != nil || fi.IsDir() {
		return "", false
	}
	return p, true
}

// canonical 等价 Rust 的 canonicalize：绝对化并解析符号链接。
func canonical(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// withinDir 按路径分量判断 p 是否在 root 里。
func withinDir(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// ImagePath 是图片块源指向的本地文件（assets/… 相对库根）。
func ImagePath(src string) (string, bool) {
	p := strings.TrimPrefix(src, "file://")
	if filepath.IsAbs(p) {
		return p, true
	}
	if src != "" && !strings.Contains(src, ":") && strings.Contains(src, "/") {
		return filepath.Join(RootDir(), p), true
	}
	return "", false
}
