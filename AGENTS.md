# fdl-go 项目约定

## 定位

quiet-md `fdl_core` 的 Go 移植，纯引擎层，不做 UI。上游参考在 `~/Projects/third-party/quiet-md/`（只读，不改不切 remote）。

## 执行边界

- M1 验收是硬标准：`testdata/all-blocks.xml` round-trip 字节级相等，不达标不宣称完成。
- 格式行为有疑问时以 quiet-md 源码（`crates/core/`）和 `docs/FORMAT.md` 为准，不猜测 DocxXML 语义。
- 上游版本升级（quiet-md 发新版）需重新对拍 fixture，再同步移植。
- 未请求不提交、不推送、不建远程仓库；远程由用户决定。
- Go 版本基线：`go 1.27`（本机 go1.27.1）。
