package main

// 应用内检查更新：GitHub Releases 最新 tag 与当前版本比对，确认后下
// 载对应平台的包、原子替换本二进制并重启（简单可靠版，不用 mygo 的
// 签名 manifest 体系）。

import (
	"context"
	"strconv"
	"strings"

	"github.com/egoist/mygo"

	fdl "github.com/nightgopher/fdl-go"
)

const releasesAPI = "https://api.github.com/repos/nightgopher/fdl-go/releases/latest"

type ghRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// verGreater 报告 a > b（v 三段数字比较；不相干后缀按字符串）。
func verGreater(a, b string) bool {
	pa := strings.SplitN(a, ".", 3)
	pb := strings.SplitN(b, ".", 3)
	for i := 0; i < 3; i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x > y
		}
	}
	return false
}

// checkUpdate 用 mygo 的签名更新体系检查并（经确认）安装更新。只在本
// 机 mygo build 的产物里生效（Enabled）；开发构建静默跳过。
func checkUpdate(ctx context.Context, manual bool) {
	if !mygo.Updater.Enabled() {
		if manual {
			mygo.Dialog.Message(mygo.MessageOptions{
				Type: "info", Title: "检查更新",
				Message: "开发构建不走签名更新；从 Release 安装的 fdl.app 支持应用内更新。",
			})
		}
		return
	}
	update, err := mygo.Updater.Check(ctx)
	if err != nil && manual {
		mygo.Dialog.Error("检查更新失败", err.Error())
		return
	}
	if update == nil {
		if manual {
			mygo.Dialog.Message(mygo.MessageOptions{
				Type: "info", Title: "已是最新", Message: "fdl-go " + fdl.Version,
			})
		}
		return
	}
	res, err := mygo.Dialog.Message(mygo.MessageOptions{
		Type:    "info",
		Title:   "发现新版本",
		Message: "fdl " + update.Version + " 可用（当前 " + fdl.Version + "）",
		Detail:  "签名校验通过后安装；文档库不动。",
		Buttons: []string{"更新并重启", "取消"},
	})
	if err != nil || res.Button != 0 {
		return
	}
	if err := update.Install(ctx, nil); err != nil {
		mygo.Dialog.Error("更新失败", err.Error())
		return
	}
	mygo.App.Relaunch()
}
