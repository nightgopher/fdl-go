// fdl-sync 让 Quiet.md 文档库与一个 Git 仓库（如 GitHub）双向同步：
//
//	镜像仓库里每篇笔记是一个「标题.md」，图片在「<名>.assets/」旁目录。
//	.fdl-sync.json 记录 路径 ↔ 文档 id 与内容哈希，只在变化时写。
//
//	fdl-sync init  [-git] <镜像目录>   建镜像（可选 git init）
//	fdl-sync sync  [-git] <镜像目录>   一条龙：先拉（仓库→库）再推（库→仓库）
//	fdl-sync push  [-git] <镜像目录>   库 → 镜像（可选 git commit+push）
//	fdl-sync pull  [-git] <镜像目录>   镜像 → 库（可选 git pull --ff-only）
//
// 文档库取 QUIET_MD_HOME（默认 ~/Documents/Quiet.md）。拉回的内容按
// Markdown 解析成 DocxXML 入库；镜像里删掉的文件移入回收站。
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/nightgopher/fdl-go/notesync"
)

func main() {
	if len(os.Args) == 2 && (os.Args[1] == "-version" || os.Args[1] == "--version") {
		fmt.Println(notesync.Version())
		return
	}
	if len(os.Args) < 2 {
		usage()
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	git := fs.Bool("git", false, "顺带执行 git 操作（init: git init；push/sync: add+commit+push；pull/sync: pull --ff-only）")
	fs.Parse(os.Args[2:])
	if fs.NArg() != 1 {
		usage()
	}
	mirror := fs.Arg(0)
	libRoot := os.Getenv("QUIET_MD_HOME")
	if libRoot == "" {
		libRoot = notesync.DefaultLibRoot()
	}

	switch cmd {
	case "init":
		if err := os.MkdirAll(mirror, 0o755); err != nil {
			fatal(err)
		}
		s, err := notesync.New(libRoot, mirror, false)
		if err != nil {
			fatal(err)
		}
		if _, err := s.Push(); err != nil {
			fatal(err)
		}
		if *git {
			notesync.GitInit(mirror)
		}
		fmt.Printf("已建镜像 %s\n", mirror)
	case "sync", "push", "pull":
		s, err := notesync.New(libRoot, mirror, *git)
		if err != nil {
			fatal(err)
		}
		var stats notesync.Stats
		var err2 error
		switch cmd {
		case "sync":
			stats, err2 = s.Sync()
		case "push":
			stats, err2 = s.Push()
		case "pull":
			stats, err2 = s.Pull()
		}
		if err2 != nil {
			fatal(err2)
		}
		fmt.Printf("%s 完成：更新 %d、新建 %d、删除 %d\n", cmd, stats.Changed, stats.Created, stats.Removed)
		for _, w := range stats.Warnings {
			fmt.Fprintln(os.Stderr, "fdl-sync:", w)
		}
	default:
		usage()
	}
}

func usage() {
	fmt.Fprint(os.Stderr, "用法：fdl-sync init|sync|push|pull [-git] <镜像目录>\n")
	os.Exit(2)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "fdl-sync:", err)
	os.Exit(1)
}
