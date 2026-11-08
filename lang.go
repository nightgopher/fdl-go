package fdl

import (
	"strings"
	"unicode"
)

// 代码块语言（显示名、XML token、别名），按菜单顺序。模型存显示名，
// DocxXML 的 lang 属性用小写 token。
var langs = []struct {
	name    string
	token   string
	aliases []string
}{
	{"Plain Text", "plaintext", []string{"plain", "text", "txt"}},
	{"ABAP", "abap", nil},
	{"Ada", "ada", nil},
	{"Apache", "apache", nil},
	{"Apex", "apex", nil},
	{"Assembly language", "assembly", []string{"asm"}},
	{"Bash", "bash", []string{"ash", "csh", "sh", "tcsh", "zsh"}},
	{"C#", "csharp", []string{"cs"}},
	{"C++", "cpp", []string{"cc", "hh"}},
	{"C", "c", []string{"h"}},
	{"CMake", "cmake", nil},
	{"COBOL", "cobol", nil},
	{"CSS", "css", nil},
	{"CoffeeScript", "coffeescript", []string{"coffee"}},
	{"D", "d", nil},
	{"Dart", "dart", nil},
	{"Delphi", "delphi", []string{"dfm", "dpr", "pas", "pascal"}},
	{"Diff", "diff", []string{"patch"}},
	{"Django", "django", []string{"jinja"}},
	{"Dockerfile", "dockerfile", []string{"docker"}},
	{"Erlang", "erlang", []string{"erl"}},
	{"Fortran", "fortran", []string{"f90", "f95"}},
	{"Gherkin", "gherkin", nil},
	{"Go", "go", []string{"golang"}},
	{"GraphQL", "graphql", nil},
	{"Groovy", "groovy", nil},
	{"HTML", "html", nil},
	{"HTMLBars", "htmlbars", nil},
	{"HTTP", "http", []string{"https"}},
	{"Haskell", "haskell", []string{"hs"}},
	{"JSON", "json", nil},
	{"Java", "java", []string{"jsp"}},
	{"JavaScript", "javascript", []string{"js", "jsx"}},
	{"Julia", "julia", nil},
	{"Kotlin", "kotlin", []string{"kt"}},
	{"LaTeX", "latex", []string{"math", "tex"}},
	{"Lisp", "lisp", nil},
	{"Lua", "lua", nil},
	{"MATLAB", "matlab", nil},
	{"Makefile", "makefile", []string{"mak", "mk", "make"}},
	{"Markdown", "markdown", []string{"md"}},
	{"Nginx", "nginx", nil},
	{"Objective-C", "objectivec", []string{"oc", "objc", "mm"}},
	{"OpenGL Shading Language", "glsl", nil},
	{"PHP", "php", nil},
	{"Perl", "perl", []string{"pl", "pm"}},
	{"PowerShell", "powershell", []string{"ps", "ps1"}},
	{"Prolog", "prolog", nil},
	{"Properties", "properties", nil},
	{"ProtoBuf", "protobuf", []string{"proto"}},
	{"Python", "python", []string{"py"}},
	{"R", "r", nil},
	{"Ruby", "ruby", []string{"rb"}},
	{"Rust", "rust", []string{"rs"}},
	{"SAS", "sas", nil},
	{"SCSS", "scss", nil},
	{"SQL", "sql", nil},
	{"Scala", "scala", nil},
	{"Scheme", "scheme", nil},
	{"Shell", "shell", nil},
	{"Solidity", "solidity", []string{"sol"}},
	{"Swift", "swift", nil},
	{"TOML", "toml", []string{"ini"}},
	{"Thrift", "thrift", nil},
	{"TypeScript", "typescript", []string{"ts", "tsx"}},
	{"VBScript", "vbscript", []string{"vbs"}},
	{"VHDL", "vhdl", nil},
	{"Verilog", "verilog", []string{"v"}},
	{"Visual Basic", "vb", []string{"visualbasic"}},
	{"XML", "xml", []string{"svg"}},
	{"YAML", "yaml", []string{"yml"}},
}

// LangPlain 是无语言代码块的显示名。
const LangPlain = "Plain Text"

// squash 去掉空白、- 和 _，转小写，让 "objective-c" 和 "objectivec"
// 命中同一项。
func squash(s string) string {
	var b strings.Builder
	for _, c := range s {
		if !unicode.IsSpace(c) && c != '-' && c != '_' {
			b.WriteRune(c)
		}
	}
	return strings.ToLower(b.String())
}

func exactLang(s string) (string, bool) {
	q := squash(s)
	for _, l := range langs {
		if squash(l.name) == q || l.token == q {
			return l.name, true
		}
		for _, a := range l.aliases {
			if a == q {
				return l.name, true
			}
		}
	}
	return "", false
}

// LangFromToken 返回 DocxXML lang token 的显示名；未知 token 原样保留，
// 这样往返不丢。
func LangFromToken(token string) string {
	if strings.TrimSpace(token) == "" {
		return LangPlain
	}
	if n, ok := exactLang(token); ok {
		return n
	}
	return token
}

// LangToToken 返回显示名对应的 DocxXML lang token。
func LangToToken(name string) string {
	for _, l := range langs {
		if l.name == name {
			return l.token
		}
	}
	return name
}

// LangResolve 解析输入的 info string（围栏、粘贴）：精确别名 → 显示名
// 前缀 → 模糊子序列；否则 Plain Text。
func LangResolve(s string) string {
	if squash(s) == "shell" {
		return "Bash"
	}
	if n, ok := exactLang(s); ok {
		return n
	}
	q := squash(s)
	if q == "" {
		return LangPlain
	}
	for _, l := range langs {
		if strings.HasPrefix(squash(l.name), q) {
			return l.name
		}
	}
	qr := []rune(q)
	subseq := func(hay string) bool {
		i := 0
		for _, h := range hay {
			if i < len(qr) && h == qr[i] {
				i++
			}
		}
		return i == len(qr)
	}
	for _, l := range langs {
		if subseq(squash(l.name)) {
			return l.name
		}
	}
	return LangPlain
}
