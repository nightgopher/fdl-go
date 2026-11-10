// fdl-mcp 是 fdl-go 引擎的 MCP server：让 agent 读写 Quiet.md 文档库。
//
// 传输 stdio；文档库位置取 QUIET_MD_HOME，默认 ~/Documents/Quiet.md。
// 磁盘格式与 Quiet.md 互通（docs/<id>.xml + index.json）。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	fdl "github.com/nightgopher/fdl-go"
)

func main() {
	version := flag.Bool("version", false, "打印版本号")
	flag.Parse()
	if *version {
		fmt.Println(fdl.Version)
		return
	}
	root := os.Getenv("QUIET_MD_HOME")
	if root == "" {
		root = fdl.RootDir()
	}
	st, err := fdl.OpenStoreAt(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fdl-mcp: %v\n", err)
		os.Exit(1)
	}
	for _, w := range st.Warnings {
		fmt.Fprintf(os.Stderr, "fdl-mcp: %s\n", w)
	}
	server := newServer(st)
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		fmt.Fprintf(os.Stderr, "fdl-mcp: %v\n", err)
		os.Exit(1)
	}
}

// ------------------------------------------------------------------ 工具定义

type noteSummary struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Icon     string `json:"icon,omitempty"`
	Folder   bool   `json:"folder,omitempty"`
	Parent   string `json:"parent,omitempty"`
	Created  uint64 `json:"created"`
	Modified uint64 `json:"modified"`
	Pinned   bool   `json:"pinned,omitempty"`
	Starred  uint64 `json:"starred,omitempty"`
	Snippet  string `json:"snippet,omitempty"`
}

type listArgs struct{}

type listOut struct {
	Notes []noteSummary `json:"notes"`
}

type readArgs struct {
	ID     string `json:"id" jsonschema:"文档 id"`
	Format string `json:"format,omitempty" jsonschema:"markdown（默认）或 docxxml"`
}

type readOut struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Format   string `json:"format"`
	Content  string `json:"content"`
	Modified uint64 `json:"modified"`
}

type searchArgs struct {
	Query string `json:"query" jsonschema:"关键词（大小写不敏感）"`
	Limit int    `json:"limit,omitempty" jsonschema:"最多返回条数，默认 20"`
}

type searchOut struct {
	Matches []noteSummary `json:"matches"`
}

type writeNoteArgs struct {
	// ID 给定时更新该文档，否则新建。标题留空时：新建用「未命名文档」，
	// 更新保持原标题。
	ID       string `json:"id,omitempty" jsonschema:"要更新的文档 id；空则新建"`
	Title    string `json:"title,omitempty" jsonschema:"标题"`
	Markdown string `json:"markdown" jsonschema:"正文（Markdown）"`
}

type writeNoteOut struct {
	ID       string `json:"id"`
	Created  bool   `json:"created"`
	Modified uint64 `json:"modified"`
}

// ------------------------------------------------------------------ server

func newServer(st *fdl.Store) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "fdl", Version: "0.1.0"}, nil)
	var mu sync.Mutex // 一个 MCP 会话内串行使用 Store

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list",
		Description: "列出 Quiet.md 文档库里的全部笔记和文件夹（标题、时间、置顶、收藏、摘要）。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ listArgs) (*mcp.CallToolResult, listOut, error) {
		mu.Lock()
		defer mu.Unlock()
		entries, err := st.List()
		if err != nil {
			return nil, listOut{}, err
		}
		out := listOut{Notes: make([]noteSummary, 0, len(entries))}
		for _, e := range entries {
			out.Notes = append(out.Notes, summaryOf(e))
		}
		return nil, out, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "read",
		Description: "读一篇笔记。format=markdown（默认）返回正文 Markdown；docxxml 返回原始 DocxXML。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args readArgs) (*mcp.CallToolResult, readOut, error) {
		mu.Lock()
		defer mu.Unlock()
		doc, err := st.Load(args.ID)
		if err != nil {
			return nil, readOut{}, fmt.Errorf("读不了 %s：%w", args.ID, err)
		}
		st.Touch(args.ID)
		format := args.Format
		if format == "" {
			format = "markdown"
		}
		var content string
		switch format {
		case "markdown":
			content = fdl.ToMarkdown(&doc)
		case "docxxml":
			content = fdl.Serialize(&doc)
		default:
			return nil, readOut{}, fmt.Errorf("不认识的 format %q（markdown 或 docxxml）", format)
		}
		meta := fdl.Meta{}
		if entries, err := st.List(); err == nil {
			for _, e := range entries {
				if e.ID == args.ID {
					meta = e.Meta
				}
			}
		}
		return nil, readOut{
			ID: args.ID, Title: fdl.DisplayTitle(doc.Title.Text), Format: format,
			Content: content, Modified: meta.Modified,
		}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "search",
		Description: "按关键词搜索笔记（标题和正文，大小写不敏感），返回匹配的笔记与上下文摘要。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args searchArgs) (*mcp.CallToolResult, searchOut, error) {
		mu.Lock()
		defer mu.Unlock()
		entries, err := st.List()
		if err != nil {
			return nil, searchOut{}, err
		}
		limit := args.Limit
		if limit <= 0 {
			limit = 20
		}
		q := strings.ToLower(args.Query)
		out := searchOut{}
		for _, e := range entries {
			if !strings.Contains(strings.ToLower(e.Title), q) && !strings.Contains(strings.ToLower(e.Body), q) {
				continue
			}
			s := summaryOf(e)
			s.Snippet = around(e.Body, q)
			out.Matches = append(out.Matches, s)
			if len(out.Matches) >= limit {
				break
			}
		}
		return nil, out, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "write_note",
		Description: "新建或更新一篇笔记：正文按 Markdown 解析（标题/列表/待办/引用/代码块/表格/图片行），" +
			"写成 Quiet.md 的 DocxXML 格式，Quiet.md 应用打开即所见。给 id 更新，不给则新建。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args writeNoteArgs) (*mcp.CallToolResult, writeNoteOut, error) {
		mu.Lock()
		defer mu.Unlock()
		blocks := fdl.MarkdownToBlocks(args.Markdown, fdl.Off{})
		if len(blocks) == 0 {
			blocks = append(blocks, fdl.BlockText(""))
		}
		created := args.ID == ""
		var id string
		if created {
			doc := fdl.NewDocument()
			doc.Blocks = blocks
			doc.Title = fdl.Plain(fdl.DisplayTitle(args.Title))
			var err error
			id, err = st.CreateFrom(&doc)
			if err != nil {
				return nil, writeNoteOut{}, err
			}
		} else {
			doc, err := st.Load(args.ID)
			if err != nil {
				return nil, writeNoteOut{}, fmt.Errorf("读不了 %s：%w", args.ID, err)
			}
			if args.Title != "" {
				doc.Title = fdl.Plain(args.Title)
			}
			doc.Blocks = blocks
			if _, err := st.Save(args.ID, &doc); err != nil {
				return nil, writeNoteOut{}, err
			}
			id = args.ID
		}
		meta := fdl.Meta{}
		if entries, err := st.List(); err == nil {
			for _, e := range entries {
				if e.ID == id {
					meta = e.Meta
				}
			}
		}
		return nil, writeNoteOut{ID: id, Created: created, Modified: meta.Modified}, nil
	})

	return s
}

func summaryOf(e fdl.Entry) noteSummary {
	s := noteSummary{
		ID:       e.ID,
		Title:    fdl.DisplayTitle(e.Title),
		Created:  e.Meta.Created,
		Modified: e.Meta.Modified,
		Pinned:   e.Meta.Pinned,
		Starred:  e.Meta.Starred,
		Snippet:  firstRunes(e.Body, 60),
	}
	if e.Icon != nil {
		s.Icon = *e.Icon
	}
	if e.Meta.Folder != nil {
		s.Folder = true
	}
	if e.Meta.Parent != nil {
		s.Parent = *e.Meta.Parent
	}
	return s
}

func firstRunes(s string, n int) string {
	count := 0
	for i := range s {
		if count >= n {
			return trimSpaceLine(s[:i])
		}
		count++
	}
	return trimSpaceLine(s)
}

// trimSpaceLine 摘要压成一行。
func trimSpaceLine(s string) string {
	return strings.TrimSpace(strings.ReplaceAll(s, "\n", " ⏎ "))
}

// around 是命中词附近的一段上下文。
func around(body, lowerQuery string) string {
	i := strings.Index(strings.ToLower(body), lowerQuery)
	if i < 0 {
		return firstRunes(body, 80)
	}
	lo := i - 30
	if lo < 0 {
		lo = 0
	}
	hi := i + len(lowerQuery) + 50
	if hi > len(body) {
		hi = len(body)
	}
	prefix, suffix := "", ""
	if lo > 0 {
		prefix = "…"
	}
	if hi < len(body) {
		suffix = "…"
	}
	return prefix + trimSpaceLine(body[lo:hi]) + suffix
}

var _ = json.Marshal
