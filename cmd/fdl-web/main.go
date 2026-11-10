// fdl-web 是 fdl-go 引擎的本地 Web 客户端：浏览、搜索、以 Markdown 编
// 辑 Quiet.md 文档库，写盘与 Quiet.md / fdl-mcp 完全同路径。
//
// 用法：fdl-web [-addr 127.0.0.1:8765]（库取 QUIET_MD_HOME，默认
// ~/Documents/Quiet.md）。浏览器打开 http://127.0.0.1:8765。
package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	fdl "github.com/nightgopher/fdl-go"
	"github.com/nightgopher/fdl-go/notesync"
)

//go:embed static
var staticFS embed.FS

func main() {
	addr := flag.String("addr", "127.0.0.1:8765", "监听地址")
	root := flag.String("root", "", "文档库目录（默认 QUIET_MD_HOME 或 ~/Documents/Quiet.md）")
	syncMirror := flag.String("sync", "", "启用同步：Git 镜像仓库目录（工具栏出同步按钮，保存后自动同步）")
	syncGit := flag.Bool("sync-git", true, "同步顺带 git 操作（pull/add/commit/push）")
	flag.Parse()
	if *root == "" {
		*root = os.Getenv("QUIET_MD_HOME")
	}
	if *root == "" {
		*root = fdl.RootDir()
	}
	st, err := fdl.OpenStoreAt(*root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fdl-web: %v\n", err)
		os.Exit(1)
	}
	for _, w := range st.Warnings {
		fmt.Fprintf(os.Stderr, "fdl-web: %s\n", w)
	}
	var syncer *notesync.Syncer
	if *syncMirror != "" {
		syncer, err = notesync.New(*root, *syncMirror, *syncGit)
		if err != nil {
			fmt.Fprintf(os.Stderr, "fdl-web: 同步未启用：%v\n", err)
			syncer = nil
		} else {
			fmt.Printf("fdl-web: 同步已启用 ⇄ %s\n", *syncMirror)
		}
	}
	fmt.Printf("fdl-web: 文档库 %s → http://%s\n", *root, *addr)
	if err := http.ListenAndServe(*addr, newMux(st, *root, syncer)); err != nil {
		fmt.Fprintf(os.Stderr, "fdl-web: %v\n", err)
		os.Exit(1)
	}
}

// autoSync 在改动后自动同步（防抖合并短时间内的连续保存）。
type autoSync struct {
	syncer *notesync.Syncer
	mu     sync.Mutex
	timer  *time.Timer
	last   string
}

func (a *autoSync) schedule() {
	if a == nil || a.syncer == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.timer != nil {
		a.timer.Stop()
	}
	a.timer = time.AfterFunc(2*time.Second, func() {
		stats, err := a.syncer.Sync()
		a.mu.Lock()
		if err != nil {
			a.last = "自动同步失败：" + err.Error()
		} else {
			a.last = fmt.Sprintf("自动同步：更新 %d、新建 %d、删除 %d", stats.Changed, stats.Created, stats.Removed)
		}
		a.mu.Unlock()
	})
}

func newMux(st *fdl.Store, root string, syncer *notesync.Syncer) http.Handler {
	var mu sync.Mutex
	auto := &autoSync{syncer: syncer}
	mux := http.NewServeMux()

	// 页面与静态资源。
	static, _ := fs.Sub(staticFS, "static")
	mux.Handle("/", http.FileServer(http.FS(static)))

	// 库内文件（图片等），只读、限定在库目录内。
	libServer := http.StripPrefix("/library/", http.FileServer(http.Dir(root)))
	mux.HandleFunc("/library/", func(w http.ResponseWriter, r *http.Request) {
		p := filepath.Clean(strings.TrimPrefix(r.URL.Path, "/library/"))
		if strings.HasPrefix(p, "..") {
			http.NotFound(w, r)
			return
		}
		libServer.ServeHTTP(w, r)
	})

	writeJSON := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(v)
	}
	fail := func(w http.ResponseWriter, err error) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
	}

	mux.HandleFunc("GET /api/notes", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		entries, err := st.List()
		if err != nil {
			fail(w, err)
			return
		}
		type row struct {
			ID       string `json:"id"`
			Title    string `json:"title"`
			Icon     string `json:"icon,omitempty"`
			Folder   bool   `json:"folder,omitempty"`
			Modified uint64 `json:"modified"`
			Pinned   bool   `json:"pinned,omitempty"`
			Snippet  string `json:"snippet,omitempty"`
		}
		rows := make([]row, 0, len(entries))
		for _, e := range entries {
			snippet := e.Body
			if len(snippet) > 72 {
				snippet = snippet[:72]
			}
			rows = append(rows, row{
				ID: e.ID, Title: fdl.DisplayTitle(e.Title), Modified: e.Meta.Modified,
				Pinned: e.Meta.Pinned, Snippet: strings.ReplaceAll(snippet, "\n", " "),
				Icon:   deref(e.Icon),
				Folder: e.Meta.Folder != nil,
			})
		}
		writeJSON(w, rows)
	})

	mux.HandleFunc("GET /api/notes/{id}", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		id := r.PathValue("id")
		doc, err := st.Load(id)
		if err != nil {
			fail(w, err)
			return
		}
		st.Touch(id)
		writeJSON(w, map[string]any{
			"id":       id,
			"title":    doc.Title.Text,
			"icon":     deref(doc.Icon),
			"html":     fdl.RenderHTML(&doc),
			"markdown": fdl.ToMarkdown(&doc),
		})
	})

	mux.HandleFunc("GET /api/search", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		q := strings.ToLower(r.URL.Query().Get("q"))
		entries, err := st.List()
		if err != nil {
			fail(w, err)
			return
		}
		type hit struct {
			ID      string `json:"id"`
			Title   string `json:"title"`
			Snippet string `json:"snippet,omitempty"`
		}
		var hits []hit
		for _, e := range entries {
			if !strings.Contains(strings.ToLower(e.Title), q) && !strings.Contains(strings.ToLower(e.Body), q) {
				continue
			}
			snippet := around(e.Body, q)
			hits = append(hits, hit{ID: e.ID, Title: fdl.DisplayTitle(e.Title), Snippet: snippet})
			if len(hits) >= 30 {
				break
			}
		}
		writeJSON(w, hits)
	})

	type noteInput struct {
		Title    string `json:"title"`
		Markdown string `json:"markdown"`
	}

	mux.HandleFunc("POST /api/notes", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		var in noteInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			fail(w, err)
			return
		}
		doc := fdl.NewDocument()
		doc.Title = fdl.Plain(fdl.DisplayTitle(in.Title))
		doc.Blocks = fdl.MarkdownToBlocks(in.Markdown, fdl.Off{})
		id, err := st.CreateFrom(&doc)
		if err != nil {
			fail(w, err)
			return
		}
		auto.schedule()
		writeJSON(w, map[string]any{"id": id})
	})

	mux.HandleFunc("PUT /api/notes/{id}", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		id := r.PathValue("id")
		var in noteInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			fail(w, err)
			return
		}
		doc, err := st.Load(id)
		if err != nil {
			fail(w, err)
			return
		}
		if in.Title != "" {
			doc.Title = fdl.Plain(in.Title)
		}
		doc.Blocks = fdl.MarkdownToBlocks(in.Markdown, fdl.Off{})
		if len(doc.Blocks) == 0 {
			doc.Blocks = []fdl.Block{fdl.BlockText("")}
		}
		if _, err := st.Save(id, &doc); err != nil {
			fail(w, err)
			return
		}
		auto.schedule()
		writeJSON(w, map[string]any{"id": id, "ok": true})
	})

	mux.HandleFunc("POST /api/notes/{id}/delete", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		id := r.PathValue("id")
		if err := st.Delete(id); err != nil {
			fail(w, err)
			return
		}
		auto.schedule()
		writeJSON(w, map[string]any{"ok": true})
	})

	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"ok": true, "root": root, "sync": syncer != nil})
	})

	// 手动同步按钮。
	mux.HandleFunc("POST /api/sync", func(w http.ResponseWriter, r *http.Request) {
		if syncer == nil {
			fail(w, fmt.Errorf("同步未启用（fdl-web -sync <镜像目录>）"))
			return
		}
		stats, err := syncer.Sync()
		if err != nil {
			fail(w, err)
			return
		}
		auto.mu.Lock()
		auto.last = ""
		auto.mu.Unlock()
		writeJSON(w, map[string]any{
			"changed": stats.Changed, "created": stats.Created, "removed": stats.Removed,
			"warnings": stats.Warnings,
		})
	})

	// 上次自动同步的结果（页面加载时显示一次）。
	mux.HandleFunc("GET /api/sync/status", func(w http.ResponseWriter, r *http.Request) {
		auto.mu.Lock()
		last := auto.last
		auto.mu.Unlock()
		writeJSON(w, map[string]any{"enabled": syncer != nil, "last": last})
	})

	return logAll(mux)
}

func logAll(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	})
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// around 是命中词附近的一段上下文。
func around(body, lowerQuery string) string {
	i := strings.Index(strings.ToLower(body), lowerQuery)
	if i < 0 {
		return ""
	}
	lo := i - 36
	if lo < 0 {
		lo = 0
	}
	hi := i + len(lowerQuery) + 60
	if hi > len(body) {
		hi = len(body)
	}
	p, s := "", ""
	if lo > 0 {
		p = "…"
	}
	if hi < len(body) {
		s = "…"
	}
	return p + strings.ReplaceAll(strings.TrimSpace(body[lo:hi]), "\n", " ⏎ ") + s
}
