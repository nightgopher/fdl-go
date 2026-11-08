package fdl

import (
	"os"
	"path/filepath"
	"strings"
)

// 本文件先落 store.rs 里导出 Markdown 需要的路径安全部分；完整磁盘
// 存储（索引/锁/原子写）属后续里程碑。

// RootDir 是文档库根目录：QUIET_MD_HOME，否则 ~/Documents/Quiet.md
// （「文稿」里的独立文件夹，用户能直接看到和备份）。
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

// canonical 等价 Rust 的 canonicalize：绝对化并解析符号链接，路径不
// 存在时报错。
func canonical(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// withinDir 按路径分量判断 p 是否在 root 里（"/a/bc" 不算在 "/a/b" 里）。
func withinDir(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
