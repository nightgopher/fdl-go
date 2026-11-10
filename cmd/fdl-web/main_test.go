package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	fdl "github.com/nightgopher/fdl-go"
	"github.com/nightgopher/fdl-go/notesync"
)

func TestWebAPI(t *testing.T) {
	st, err := fdl.OpenStoreAt(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(newMux(st, st.Root(), nil))
	defer srv.Close()

	post := func(path, body string) map[string]any {
		t.Helper()
		res, err := http.Post(srv.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("%s: %d", path, res.StatusCode)
		}
		var out map[string]any
		json.NewDecoder(res.Body).Decode(&out)
		return out
	}
	get := func(path string) int {
		t.Helper()
		res, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}

	// 新建。
	r := post("/api/notes", `{"title":"网页笔记","markdown":"# 你好\n\n**加粗** 和 [链接](https://a.b)\n"}`)
	id, _ := r["id"].(string)
	if id == "" {
		t.Fatalf("create = %v", r)
	}

	// 列表。
	if code := get("/api/notes"); code != 200 {
		t.Fatalf("list = %d", code)
	}
	res, err := http.Get(srv.URL + "/api/notes")
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	json.NewDecoder(res.Body).Decode(&rows)
	res.Body.Close()
	if len(rows) != 1 || rows[0]["title"] != "网页笔记" {
		t.Fatalf("rows = %v", rows)
	}

	// 读：html 与 markdown 双形态。
	res, _ = http.Get(srv.URL + "/api/notes/" + id)
	var note map[string]any
	json.NewDecoder(res.Body).Decode(&note)
	res.Body.Close()
	if html, _ := note["html"].(string); !strings.Contains(html, "<h1>你好</h1>") || !strings.Contains(html, "<b>加粗</b>") {
		t.Errorf("html = %v", note["html"])
	}
	if md, _ := note["markdown"].(string); !strings.Contains(md, "# 你好") {
		t.Errorf("markdown = %q", md)
	}

	// 更新。
	res2, err := http.NewRequest("PUT", srv.URL+"/api/notes/"+id, strings.NewReader(`{"title":"改名","markdown":"只剩正文"}`))
	if err != nil {
		t.Fatal(err)
	}
	res2.Header.Set("Content-Type", "application/json")
	http.DefaultClient.Do(res2)
	doc, err := st.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Title.Text != "改名" || doc.PlainText() != "只剩正文" {
		t.Errorf("更新后 = %q / %q", doc.Title.Text, doc.PlainText())
	}

	// 搜索。
	res, _ = http.Get(srv.URL + "/api/search?q=正文")
	var hits []map[string]any
	json.NewDecoder(res.Body).Decode(&hits)
	res.Body.Close()
	if len(hits) != 1 || hits[0]["id"] != id {
		t.Fatalf("hits = %v", hits)
	}

	// 删除 → 回收站。
	post("/api/notes/"+id+"/delete", "")
	if _, err := st.Load(id); err == nil {
		t.Error("删除后还能读")
	}

	// /library 不越库。
	res, _ = http.Get(srv.URL + "/library/../../etc/passwd")
	if res.StatusCode == 200 {
		t.Error("路径穿越")
	}
	res.Body.Close()

	// 首页可访问。
	res, _ = http.Get(srv.URL + "/")
	page := make([]byte, 200)
	n, _ := res.Body.Read(page)
	res.Body.Close()
	if !strings.Contains(string(page[:n]), "fdl") && !strings.Contains(string(page[:n]), "<html") {
		t.Errorf("首页 = %q", string(page[:n]))
	}
}

// 同步按钮链路：-sync 启用后 POST /api/sync 执行双向同步。
func TestWebSync(t *testing.T) {
	lib := t.TempDir()
	st, err := fdl.OpenStoreAt(lib)
	if err != nil {
		t.Fatal(err)
	}
	mirror := t.TempDir()
	syncer, err := notesync.New(lib, mirror, false)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(newMux(st, lib, syncer))
	defer srv.Close()

	// 未启用时 health.sync=false；这里已启用，应为 true。
	res, _ := http.Get(srv.URL + "/api/health")
	var h map[string]any
	json.NewDecoder(res.Body).Decode(&h)
	res.Body.Close()
	if h["sync"] != true {
		t.Fatalf("health = %v", h)
	}

	// 建一篇笔记 → POST /api/sync → 镜像里出现 md。
	http.Post(srv.URL+"/api/notes", "application/json",
		strings.NewReader(`{"title":"同步按钮","markdown":"按钮按一下\n"}`))
	resp, err := http.Post(srv.URL+"/api/sync", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("sync = %d", resp.StatusCode)
	}
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	if out["created"] != float64(1) {
		t.Errorf("sync out = %v", out)
	}
	if _, err := os.Stat(filepath.Join(mirror, "同步按钮.md")); err != nil {
		t.Errorf("镜像里没有：%v", err)
	}
}
