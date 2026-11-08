package fdl

import (
	"strings"
	"testing"
)

// 对拍 paste.rs 的 markdown 测试。
func TestPasteMarkdownBlocks(t *testing.T) {
	doc := NewDocument()
	doc.Blocks = MarkdownToBlocks("# H1\n\nSome **bold** and *it* and ~u~ and `c` [l](https://a.b/c)\n- a\n  - b\n- [x] t\n3. three\n4. four\n> q\n\n---\n```rust\nfn main() {}\n```\n| A | B |\n|---|:-:|\n| 1 | 2 |\n", Off{})
	x := Serialize(&doc)
	for _, want := range []string{
		"<h1>H1</h1>", "<b>bold</b>", "<em>it</em>", "<u>u</u>", "<code>c</code>",
		`<a href="https://a.b/c">l</a>`, "<li>a", `<checkbox done="true">t</checkbox>`,
		`<li seq="3">three</li>`, "<li>four</li>", "<blockquote>", "<hr/>",
		"fn main() {}", "<table",
	} {
		if !strings.Contains(x, want) {
			t.Errorf("缺少 %s:\n%s", want, x)
		}
	}
}

func TestPasteMarkdownLiterals(t *testing.T) {
	rt := mdInlineParse("a_b_c snake_case_name ** x** costs $5 and $6")
	for _, r := range rt.Runs {
		if r.Marks.Bold || r.Marks.Italic {
			t.Errorf("不该有样式: %+v in %q", r.Marks, rt.Text)
		}
	}
}

func TestLangResolve(t *testing.T) {
	cases := map[string]string{
		"":            LangPlain,
		"rs":          "Rust",
		"zsh":         "Bash",
		"shell":       "Bash",
		"C++":         "C++",
		"type":        "TypeScript",
		"objective-c": "Objective-C",
		"qqqq":        LangPlain,
	}
	for in, want := range cases {
		if got := LangResolve(in); got != want {
			t.Errorf("resolve(%q) = %q, want %q", in, got, want)
		}
	}
}
