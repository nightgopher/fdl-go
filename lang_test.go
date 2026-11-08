package fdl

import "testing"

func TestTokensRoundTrip(t *testing.T) {
	for _, l := range langs {
		if got := LangFromToken(l.token); got != l.name {
			t.Errorf("LangFromToken(%q) = %q, want %q", l.token, got, l.name)
		}
		if got := LangToToken(l.name); got != l.token {
			t.Errorf("LangToToken(%q) = %q, want %q", l.name, got, l.token)
		}
	}
	// 未知 token 原样保留。
	if LangFromToken("brainfuck") != "brainfuck" || LangToToken("brainfuck") != "brainfuck" {
		t.Error("未知语言应原样保留")
	}
	if LangFromToken("") != LangPlain || LangFromToken("  ") != LangPlain {
		t.Errorf("空 token = %q", LangFromToken(""))
	}
	// squash：objective-c 与 alias 命中。
	if LangFromToken("Objective-C") != "Objective-C" || LangFromToken("oc") != "Objective-C" {
		t.Error("objective-c 别名解析不对")
	}
	if LangFromToken("zsh") != "Bash" {
		t.Error("zsh 应命中 Bash")
	}
}
