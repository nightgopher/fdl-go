# fdl-go 项目约定

## 定位

quiet.md `fdl_core` 的 Go 移植（引擎层）+ 周边（MCP/Web/GitHub 同步/CLI），M1–M3 已完成并验收。上游参考在 `~/Projects/third-party/quiet-md/`（只读，不改不切 remote）。

## 执行边界

- 对拍是硬标准：`testdata/all-blocks.xml` round-trip 字节级相等；上游用例移植后行为一致，不达标不宣称完成。
- 格式行为有疑问时以 quiet.md 源码（`crates/core/`）和 `docs/FORMAT.md` 为准，不猜测 DocxXML 语义。
- 上游版本升级（quiet-md 发新版）需重新对拍 fixture，再同步移植；当前同步基线 `db8d172`（2026-11-09）。
- 未请求不提交、不推送；推送与远程由用户决定（远程为 nightgopher/fdl-go，public）。
- Go 版本基线：`go 1.27`（本机 go1.27.1）。
- store 是上游引擎子集：GUI 侧能力（settings/file_history/本机文件与库内 .md 写入/回收站恢复/文件夹 CRUD）按需补，补时先读上游对应实现再对拍。
