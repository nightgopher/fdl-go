package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	fdl "github.com/nightgopher/fdl-go"
)

// MCP 端到端：真实 SDK 客户端 ↔ server ↔ 磁盘文档库。
func TestMCPEndToEnd(t *testing.T) {
	st, err := fdl.OpenStoreAt(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	server := newServer(st)
	ct, stt := mcp.NewInMemoryTransports()
	go server.Run(ctx, stt)

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	call := func(name string, args map[string]any, out any) {
		t.Helper()
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if res.IsError {
			t.Fatalf("%s 返回错误：%v", name, res.Content)
		}
		raw, err := json.Marshal(res.StructuredContent)
		if err != nil {
			t.Fatalf("%s 结构化结果：%v", name, err)
		}
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("%s 结构化结果：%v（%s）", name, err, raw)
		}
	}

	// write_note：新建。
	var w writeNoteOut
	call("write_note", map[string]any{
		"title":    "会议纪要",
		"markdown": "# 结论\n\n- 通过\n- [ ] 跟进排期\n\n```go\nfn()\n```\n",
	}, &w)
	if !w.Created || w.ID == "" {
		t.Fatalf("write_note = %+v", w)
	}
	// 磁盘上是合法 DocxXML，Quiet.md 能打开。
	doc, err := fdl.Parse(mustRead(t, st, w.ID))
	if err != nil {
		t.Fatalf("盘上的文件解析失败：%v\n%s", err, mustRead(t, st, w.ID))
	}
	if doc.Title.Text != "会议纪要" {
		t.Errorf("title = %q", doc.Title.Text)
	}
	if text := doc.PlainText(); !containsAll(text, "结论", "通过", "跟进排期", "fn()") {
		t.Errorf("body = %q", text)
	}
	if _, isTodo := kindOf(doc, func(k fdl.BlockKind) bool { _, ok := k.(fdl.Todo); return ok }); !isTodo {
		t.Error("待办没解析出来")
	}

	// list 能看到。
	var l listOut
	call("list", map[string]any{}, &l)
	if len(l.Notes) != 1 || l.Notes[0].Title != "会议纪要" || l.Notes[0].ID != w.ID {
		t.Fatalf("list = %+v", l.Notes)
	}

	// read（markdown）。
	var r readOut
	call("read", map[string]any{"id": w.ID}, &r)
	if r.Title != "会议纪要" || !containsAll(r.Content, "# 结论", "- 通过", "- [ ] 跟进排期", "```go") {
		t.Errorf("read = %+v content=%q", r, r.Content)
	}
	// read（docxxml）。
	call("read", map[string]any{"id": w.ID, "format": "docxxml"}, &r)
	if !strings.HasPrefix(r.Content, "<title>") {
		t.Errorf("docxxml = %q", r.Content[:40])
	}

	// search。
	var s searchOut
	call("search", map[string]any{"query": "排期"}, &s)
	if len(s.Matches) != 1 || s.Matches[0].ID != w.ID || !contains(s.Matches[0].Snippet, "排期") {
		t.Fatalf("search = %+v", s.Matches)
	}
	call("search", map[string]any{"query": "不存在的词"}, &s)
	if len(s.Matches) != 0 {
		t.Errorf("空搜索 = %+v", s.Matches)
	}

	// write_note：更新已有。
	call("write_note", map[string]any{
		"id":       w.ID,
		"markdown": "只有正文了\n",
	}, &w)
	if w.Created {
		t.Error("更新不应标记 created")
	}
	doc = mustParse(t, st, w.ID)
	if text := doc.PlainText(); text != "只有正文了" {
		t.Errorf("更新后 body = %q", text)
	}
	if doc.Title.Text != "会议纪要" {
		t.Errorf("更新不应改标题： %q", doc.Title.Text)
	}
}

func mustRead(t *testing.T, st *fdl.Store, id string) string {
	t.Helper()
	doc, err := st.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	return fdl.Serialize(&doc)
}

func mustParse(t *testing.T, st *fdl.Store, id string) fdl.Document {
	t.Helper()
	doc, err := st.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func kindOf(doc fdl.Document, match func(fdl.BlockKind) bool) (int, bool) {
	for i := range doc.Blocks {
		if match(doc.Blocks[i].Kind) {
			return i, true
		}
	}
	return 0, false
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !contains(s, sub) {
			return false
		}
	}
	return true
}
