# fdl-go

[quiet.md](https://github.com/s1dashu/quiet-md) 文档引擎（`fdl_core`，Rust）的 Go 移植，外加一套围绕它的周边工具：**同一个 Quiet.md 文档库，人、AI 和 Git 都能读写**。

```
                     ┌─ Quiet.md（Rust GUI，人用，高性能编辑）
                     │
~/Documents/Quiet.md ┼─ fdl-mcp   MCP server —— AI（easyagent/Claude 等）挂载读写
（共享磁盘文档库）     ├─ fdl-web   本地 Web 客户端 —— 浏览/搜索/Markdown 编辑
                     ├─ fdl-sync  GitHub 同步 —— .md 镜像仓库双向同步
                     └─ fdl       CLI —— round-trip 校验/格式转换/检查
```

所有入口共用同一个磁盘格式（`docs/<id>.xml` + `index.json`，原子写、flock 锁、`.bak` 备份恢复），与 Quiet.md 应用完全互通——fdl 写的笔记 Quiet.md 打开即所见（已实测验收）。上游参考（只读）在 `~/Projects/third-party/quiet-md/`，移植以行为对拍为准。

## 动机

1. easyagent 集成：知识库（Markdown 世界）与 Quiet.md（DocxXML 世界）之间高保真互通，让 agent 以原生结构读写笔记。
2. 学习载体：以字节级 round-trip 为硬验收，精读并移植一个高质量的文档引擎。

## 能力

| 能力 | 说明 |
|---|---|
| DocxXML 引擎 | 数据模型、双向解析/序列化；`testdata/all-blocks.xml` **字节级 round-trip**；未知元素经 Raw 块保全 |
| 编辑内核 | 光标/选区、增删改、Enter/Backspace 全分支、缩进、块类型变换、折叠、拖拽、表格全套操作、撤销/重做 |
| Markdown ⇄ DocxXML | 导出（列表紧凑/引用缩进/管道表/图片复制清单）与导入（缩进嵌套、围栏、待办、表格）；`.md` 文件**原位编辑**——未改的块按原文逐字节保留 |
| HTML 导入 | 剪贴板 HTML → 块（样式映射、飞书待办/高亮块、代码语言嗅探、表格占位） |
| MCP server | `list` / `read`（markdown 或 docxxml）/ `search` / `write_note` 四工具，stdio 传输 |
| Web 客户端 | 列表/搜索/双形态视图/Markdown 编辑，零外部前端依赖 |
| GitHub 同步 | 笔记 ⇄ 「标题.md」镜像仓库双向同步，网页上改了也能拉回来 |
| CLI | `roundtrip` / `xml2md` / `md2xml` / `html2xml` / `info` |

## 快速开始

```sh
go build ./...   # Go 1.27
go test ./...    # 100+ 项测试（含确定性 fuzz）
```

文档库位置：环境变量 `QUIET_MD_HOME`，默认 `~/Documents/Quiet.md`（与 Quiet.md 应用一致）。**下面的示例都用临时库，不碰真实库。**

### fdl-mcp：给 AI 挂载

```sh
go build -o ~/bin/fdl-mcp ./cmd/fdl-mcp
QUIET_MD_HOME=/path/to/lib ~/bin/fdl-mcp     # stdio MCP server
```

客户端配置（easyagent / Claude 等 `mcpServers` 格式）：

```json
{
  "mcpServers": {
    "fdl": {
      "command": "/Users/you/bin/fdl-mcp",
      "env": { "QUIET_MD_HOME": "/path/to/lib" },
      "trust": true
    }
  }
}
```

AI 即可调用 `list` / `read` / `search` / `write_note`（正文按 Markdown 解析，写成 Quiet.md 的 DocxXML）。已用 easyagent 的 `sdk/mcp` 真实挂载验收。

### fdl-web：浏览器里用

```sh
go build -o ~/bin/fdl-web ./cmd/fdl-web
QUIET_MD_HOME=/path/to/lib ~/bin/fdl-web -addr 127.0.0.1:8765
# 打开 http://127.0.0.1:8765
```

### fdl-sync：GitHub 同步

```sh
go build -o ~/bin/fdl-sync ./cmd/fdl-sync
fdl-sync init ~/notes-repo -git   # 建镜像仓库（git init + 全量导出），加 remote 即可
fdl-sync push ~/notes-repo -git   # 库 → 仓库（git add/commit/push）
fdl-sync pull ~/notes-repo -git   # 仓库 → 库（git pull 后导入）
```

镜像里每篇笔记是一个「标题.md」（图片在 `<名>.assets/` 旁目录），`.fdl-sync.json` 维护映射与内容哈希。GitHub 网页上：改 `.md` → 更新笔记；新增 → 新笔记；删除 → 移入回收站。库侧改名 → 镜像文件 git rename；导出走原位写回，未改的块保持镜像格式。

### fdl：CLI

```sh
fdl roundtrip file.xml...   # 解析→序列化字节级对比（M1 验收口径）
fdl xml2md   file.xml       # DocxXML → Markdown
fdl md2xml   file.md        # Markdown → DocxXML
fdl html2xml file.html      # HTML → DocxXML
fdl info     file.xml       # 结构摘要
```

## 与 Quiet.md 的互通

磁盘层逐项对齐上游 `store.rs`：

- `index.json` / `trash/index.json` 字段与 serde 缺省、省略规则一致（新字段带默认值，旧版可读）
- `format.json` 版本标记（BASE 2 / FORMAT 3），更新版本的库拒绝打开
- 原子写：临时文件 + fsync + rename + 目录 sync；索引每次保存留 `.bak`，损坏自动改名 `corrupt-<秒>` 并从备份恢复
- `.lock` flock 写锁，写前重读索引——与 Quiet.md（或其他 fdl 进程）同时用同一库不出事

上游 GUI 侧能力（settings、file_history、本机文件与库内 .md 写入、回收站恢复/清理、文件夹 CRUD）不在引擎子集内；相关 Meta 字段会原样往返不丢失。

## 对拍与测试

- 上游 `docxml.rs` / `model.rs` / `editor/core.rs` / `table_ops.rs` / `markdown.rs` / `paste.rs` / `store.rs` 的测试全量对拍移植
- 确定性 fuzz（`FDL_FUZZ_SEEDS`/`FDL_FUZZ_STEPS`/`FDL_FUZZ_SEED`）：随机编辑每步验模型不变量 + 存盘往返 + 完整 undo/redo 复原；200 种子 × 400 步通过
- `FDL_ROUNDTRIP_DIR=<库>/docs go test -run Library` 可对拍真实文档库

## 里程碑（全部完成）

| 里程碑 | 内容 | 验收标准 | 状态 |
| --- | --- | --- | --- |
| M1 | 模型 + DocxXML 解析/序列化 | `testdata/all-blocks.xml` 解析→序列化后**字节级相等**；未知元素经 Raw 块保全 | ✅ 91c324e |
| M2 | 编辑内核（光标/增删改/撤销）+ Markdown ⇄ DocxXML 转换 | 对拍 quiet-md `markdown.rs` / `paste.rs` 的行为；模糊测试守不变量 | ✅ 779eb0b..10235db（fuzz 200 种子×400 步通过） |
| M3 | MCP server（list/read/search/write_note） | easyagent 挂载后可写入结构化笔记并在 Quiet.md 正常渲染 | ✅ b179511（easyagent sdk/mcp 真实挂载调用通过；Quiet.md 0.1.7 打开写入库，全部块型渲染正常，截图核验） |

后续：基于 MyGo `ui/richtext` 的终端客户端（待做）；上游同步基线 `db8d172`（2026-11-09），`git log crates/core` 有新变化时按 `docs/FORMAT.md` 与 fixture 重新对拍。

## 致谢与许可

上游 [quiet.md](https://github.com/s1dashu/quiet-md)（MIT，© s1dashu）；`emoji.tsv` 拷自上游。本项目为其引擎的独立 Go 移植，MIT 许可（见 [LICENSE](./LICENSE)）。
