package main

// 应用内检查更新：GitHub Releases 最新 tag 与当前版本比对，确认后下
// 载对应平台的包、原子替换本二进制并重启（简单可靠版，不用 mygo 的
// 签名 manifest 体系）。

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

// newerVersion 返回 GitHub 上的最新版本号；与当前相同则空。
func newerVersion(ctx context.Context) (string, error) {
	if fdl.Version == "dev" || fdl.Version == "" {
		return "", nil // 开发构建不检查
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, releasesAPI, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", fmt.Errorf("GitHub API %d", res.StatusCode)
	}
	var rel ghRelease
	if err := json.NewDecoder(res.Body).Decode(&rel); err != nil {
		return "", err
	}
	latest := strings.TrimPrefix(rel.TagName, "v")
	if latest != "" && verGreater(latest, strings.TrimPrefix(fdl.Version, "v")) {
		return rel.TagName, nil
	}
	return "", nil
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

// selfUpdate 下载 tag 的平台包，替换正在运行的二进制，成功后重启。
// 返回错误时应用继续运行原版本。
func selfUpdate(ctx context.Context, tag string) error {
	target := runtime.GOOS + "-" + runtime.GOARCH
	// 拿资产下载地址。
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, releasesAPI, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	var rel ghRelease
	json.NewDecoder(res.Body).Decode(&rel)
	url := ""
	for _, a := range rel.Assets {
		if strings.Contains(a.Name, target) && strings.HasSuffix(a.Name, ".tar.gz") {
			url = a.BrowserDownloadURL
			break
		}
	}
	if url == "" {
		return fmt.Errorf("release %s 没有 %s 的包", rel.TagName, target)
	}

	// 下载并解出 fdl-app。
	dl, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	res2, err := http.DefaultClient.Do(dl)
	if err != nil {
		return err
	}
	defer res2.Body.Close()
	if res2.StatusCode != 200 {
		return fmt.Errorf("下载 %d", res2.StatusCode)
	}
	gz, err := gzip.NewReader(res2.Body)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return err
	}
	tmp := exe + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	found := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			out.Close()
			os.Remove(tmp)
			return err
		}
		if hdr.Typeflag == tar.TypeReg && strings.HasSuffix(hdr.Name, "/fdl-app") {
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				os.Remove(tmp)
				return err
			}
			found = true
			break
		}
	}
	out.Close()
	if !found {
		os.Remove(tmp)
		return fmt.Errorf("包里没有 fdl-app")
	}

	// 原子替换：旧文件留 .old（下次更新覆盖）。
	os.Remove(exe + ".old")
	os.Rename(exe, exe+".old")
	if err := os.Rename(tmp, exe); err != nil {
		os.Rename(exe+".old", exe) // 恢复
		return err
	}
	return nil
}

// relaunch 拉起新版本进程后退出自己。
func relaunch() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	if cmd := exec.Command(exe, os.Args[1:]...); cmd.Start() == nil {
		mygo.App.Quit()
		return
	}
}

// checkUpdate 检查并（经确认）安装更新，结果以对话框告知。可在任意
// goroutine 调用；manual 区分手动检查（无更新也提示）。
func checkUpdate(ctx context.Context, manual bool) {
	tag, err := newerVersion(ctx)
	if err != nil {
		if manual {
			mygo.Dialog.Error("检查更新失败", err.Error())
		}
		return
	}
	if tag == "" {
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
		Message: "fdl-go " + tag + " 可用（当前 " + fdl.Version + "）",
		Detail:  "更新只替换 fdl-app 本身，文档库不动。",
		Buttons: []string{"更新并重启", "取消"},
	})
	if err != nil || res.Button != 0 {
		return
	}
	if err := selfUpdate(ctx, tag); err != nil {
		mygo.Dialog.Error("更新失败", err.Error())
		return
	}
	relaunch()
}
