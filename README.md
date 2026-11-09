# fdl-go

quiet-md 文档引擎（`fdl_core`，Rust）的 Go 移植：DocxXML 数据模型、双向解析/序列化、编辑内核与磁盘存储。**无 UI**，只做引擎层。

- 上游参考（只读）：`~/Projects/third-party/quiet-md/`
  - 引擎源码：`crates/core/src/`（`model.rs` / `docxml.rs` / `editor/` / `store.rs`）
  - 格式说明：`docs/FORMAT.md`
  - 权威测试夹具：`testdata/all-blocks.xml`（已拷入本仓库）

## 动机

1. easyagent 集成：知识库（Markdown 世界）与 Quiet.md（DocxXML 世界）之间需要高保真转换；M3 的 MCP server 让 agent 以原生结构读写笔记。
2. 学习载体：以字节级 round-trip 为硬验收，精读并移植一个高质量的文档引擎。

## 里程碑（全部完成）

| 里程碑 | 内容 | 验收标准 | 状态 |
| --- | --- | --- | --- |
| M1 | 模型 + DocxXML 解析/序列化 | `testdata/all-blocks.xml` 解析→序列化后**字节级相等**；未知元素经 Raw 块保全 | ✅ 91c324e |
| M2 | 编辑内核（光标/增删改/撤销）+ Markdown ⇄ DocxXML 转换 | 对拍 quiet-md `markdown.rs` / `paste.rs` 的行为；模糊测试守不变量 | ✅ 779eb0b..10235db（fuzz 200 种子×400 步通过） |
| M3 | MCP server（list/read/search/write_note） | easyagent 挂载后可写入结构化笔记并在 Quiet.md 正常渲染 | ✅ b179511（easyagent sdk/mcp 真实挂载调用通过；Quiet.md 0.1.7 打开写入库，全部块型渲染正常，截图核验） |

下游消费者：easyagent MCP server（`cmd/fdl-mcp`，已验收）、本地 Web 客户端（`cmd/fdl-web`，浏览/搜索/Markdown 编辑）、GitHub 同步（`cmd/fdl-sync`，见下）、`fdl` CLI（roundtrip/转换/检查）。基于 MyGo `ui/richtext` 的终端客户端待做（本机暂无 mygo 副本）。

## GitHub 同步

`fdl-sync` 在文档库与一个 Git 仓库（如 GitHub）之间双向同步：镜像仓库里
每篇笔记是一个「标题.md」（图片在 `<名>.assets/` 旁目录），`.fdl-sync.json`
记录路径 ↔ 文档 id 与内容哈希，只在变化时写。

```sh
fdl-sync init ~/notes-repo -git   # 建镜像（git init），全量导出
fdl-sync push ~/notes-repo -git   # 库 → 镜像 + git add/commit/push
fdl-sync pull ~/notes-repo -git   # git pull → 镜像 → 库
```

- GitHub 网页/其他机器上：改 `.md` → 更新笔记；新增 `.md` → 新笔记；
  删除 → 移入回收站；镜像里的图片收进库 `assets/` 并改库内相对路径。
- 库侧：改名 → 镜像文件跟着改名（git 识别 rename）；导出走原位写回，
  未改的块保持自己在镜像里的格式。

## 边界

- 上游仓库保持只读，不改动；移植以行为对拍为准，不照抄内部命名。
- 表格合并单元格、KaTeX 公式布局等重型特性推迟到 M2 之后，按需评估。
- 语言偏好遵循个人约定：新核心用 Go。
