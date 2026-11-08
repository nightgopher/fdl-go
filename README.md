# fdl-go

quiet-md 文档引擎（`fdl_core`，Rust）的 Go 移植：DocxXML 数据模型、双向解析/序列化、编辑内核与磁盘存储。**无 UI**，只做引擎层。

- 上游参考（只读）：`~/Projects/third-party/quiet-md/`
  - 引擎源码：`crates/core/src/`（`model.rs` / `docxml.rs` / `editor/` / `store.rs`）
  - 格式说明：`docs/FORMAT.md`
  - 权威测试夹具：`testdata/all-blocks.xml`（已拷入本仓库）

## 动机

1. easyagent 集成：知识库（Markdown 世界）与 Quiet.md（DocxXML 世界）之间需要高保真转换；M3 的 MCP server 让 agent 以原生结构读写笔记。
2. 学习载体：以字节级 round-trip 为硬验收，精读并移植一个高质量的文档引擎。

## 里程碑

| 里程碑 | 内容 | 验收标准 |
| --- | --- | --- |
| M1 | 模型 + DocxXML 解析/序列化 | `testdata/all-blocks.xml` 解析→序列化后**字节级相等**；未知元素经 Raw 块保全 |
| M2 | 编辑内核（光标/增删改/撤销）+ Markdown ⇄ DocxXML 转换 | 对拍 quiet-md `markdown.rs` / `paste.rs` 的行为；模糊测试守不变量 |
| M3 | MCP server（list/read/search/write_note） | easyagent 挂载后可写入结构化笔记并在 Quiet.md 正常渲染 |

下游消费者（规划）：easyagent MCP server、基于 MyGo `ui/richtext` 的笔记客户端。

## 边界

- 上游仓库保持只读，不改动；移植以行为对拍为准，不照抄内部命名。
- 表格合并单元格、KaTeX 公式布局等重型特性推迟到 M2 之后，按需评估。
- 语言偏好遵循个人约定：新核心用 Go。
