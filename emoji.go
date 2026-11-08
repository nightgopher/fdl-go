package fdl

// emoji 表（emoji-mart v5 的 id、中文名与关键词）：粘贴读 data-emoji-id
// 用。emoji.tsv 拷自上游 quiet-md（MIT）。

import (
	"embed"
	"strings"
)

//go:embed emoji.tsv
var emojiTSV embed.FS

// EmojiByID 返回 emoji-mart id 对应的字符（chestnut → 🌰）。
func EmojiByID(id string) (string, bool) {
	data, err := emojiTSV.ReadFile("emoji.tsv")
	if err != nil {
		return "", false
	}
	for _, l := range strings.Split(string(data), "\n") {
		f := strings.Split(l, "\t")
		if len(f) < 3 {
			continue
		}
		if f[1] == id {
			return f[2], true
		}
	}
	return "", false
}
