package fdl

// Markdown → 块（对拍 paste.rs 的 Markdown 部分）：与实时快捷键相同的
// 规则，外加围栏、按缩进嵌套、管道表、图片和链接。HTML 粘贴在后续版本。

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Off 是粘贴目标关掉的规则（列表项不再识别自己的类型，引用里没有引
// 用，高亮块里没有代码和分割线）。
type Off struct {
	Bullet      bool
	Ordered     bool
	Todo        bool
	Quote       bool
	CodeDivider bool
	// File：导入整个文件——空行只分块，整行 `![alt](src)` 是图片。
	File bool
}

// MarkdownToBlocks 把 Markdown 解析成块。
func MarkdownToBlocks(text string, off Off) []Block {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return mdBlocks(strings.Split(strings.TrimRight(text, "\n"), "\n"), off)
}

// MarkdownInlineLines 只上行内规则（粘进非空块）：每行一块。
func MarkdownInlineLines(text string) []Block {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	var out []Block
	for _, l := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		out = append(out, NewBlock(Text{}, mdInlineParse(mdTabs(l))))
	}
	return out
}

// mdTabs：连续的制表合成一个空格。
func mdTabs(s string) string {
	var b strings.Builder
	lastTab := false
	for _, c := range s {
		if c != '\t' {
			b.WriteRune(c)
			lastTab = false
		} else if !lastTab {
			b.WriteByte('\t')
			lastTab = true
		}
	}
	return strings.ReplaceAll(b.String(), "\t", " ")
}

func indentOf(line string) int {
	n := 0
	for _, c := range line {
		switch c {
		case ' ':
			n++
		case '\t':
			n += 4 - n%4
		default:
			return n
		}
	}
	return n
}

// dedent 去掉行首 n 列缩进。
func dedent(line string, n int) string {
	col := 0
	for i, c := range line {
		if col >= n {
			return line[i:]
		}
		switch c {
		case ' ':
			col++
		case '\t':
			col += 4 - col%4
		default:
			return line[i:]
		}
	}
	return ""
}

type mdMarker struct {
	ordered bool
	num     uint32
}

// mdListItem 识别列表项行：(记号, 内容列, 内容)。
func mdListItem(line string) (mdMarker, int, string, bool) {
	ind := indentOf(line)
	rest := strings.TrimLeft(line, " \t")
	if rest == "" {
		return mdMarker{}, 0, "", false
	}
	var marker mdMarker
	mlen := 0
	first, fs := utf8.DecodeRuneInString(rest)
	if first == '-' || first == '*' || first == '+' {
		mlen = fs
	} else {
		digits := 0
		for digits < len(rest) && rest[digits] >= '0' && rest[digits] <= '9' {
			digits++
		}
		if digits == 0 || digits > 9 {
			return mdMarker{}, 0, "", false
		}
		tail := rest[digits:]
		if tail == "" {
			return mdMarker{}, 0, "", false
		}
		d, ds := utf8.DecodeRuneInString(tail)
		if d != '.' && d != ')' && d != '。' {
			return mdMarker{}, 0, "", false
		}
		n, ok := parseU32(rest[:digits])
		if !ok {
			return mdMarker{}, 0, "", false
		}
		marker = mdMarker{ordered: true, num: n}
		mlen = digits + ds
	}
	after := rest[mlen:]
	if !strings.HasPrefix(after, " ") && !strings.HasPrefix(after, "\t") {
		return mdMarker{}, 0, "", false
	}
	content := after[1:]
	// `***` / `- - -` 一类的横线是分割线，不是列表项。
	if mdDivider(rest) {
		return mdMarker{}, 0, "", false
	}
	return marker, ind + mlen + 1, content, true
}

func parseU32(s string) (uint32, bool) {
	var n uint64
	for i := 0; i < len(s); i++ {
		d := uint64(s[i] - '0')
		if n > (0xFFFFFFFF-d)/10 {
			return 0, false
		}
		n = n*10 + d
	}
	if n > 0xFFFFFFFF {
		return 0, false
	}
	return uint32(n), true
}

func mdDivider(line string) bool {
	var b strings.Builder
	for _, c := range strings.TrimSpace(line) {
		if c != ' ' {
			b.WriteRune(c)
		}
	}
	t := b.String()
	if t == "---" || t == "***" || t == "___" {
		return true
	}
	if len(t) > 3 && len(t) < 6 {
		for i := 0; i < len(t); i++ {
			if t[i] != '-' {
				return false
			}
		}
		return true
	}
	return false
}

// mdFence 识别围栏：(记号字符, 长度, info)。
func mdFence(line string) (byte, int, string, bool) {
	t := strings.TrimLeft(line, " \t")
	if t == "" {
		return 0, 0, "", false
	}
	c := t[0]
	if c != '`' && c != '~' {
		return 0, 0, "", false
	}
	n := 0
	for n < len(t) && t[n] == c {
		n++
	}
	if n < 3 {
		return 0, 0, "", false
	}
	return c, n, strings.TrimSpace(t[n:]), true
}

// mdTodoPrefix 识别待办前缀：(是否勾选, 其余)。
func mdTodoPrefix(s string) (bool, string, bool) {
	for _, p := range []struct {
		prefix string
		done   bool
	}{
		{"[ ] ", false}, {"[] ", false}, {"【】 ", false},
		{"[x] ", true}, {"[X] ", true}, {"【x】 ", true},
	} {
		if r, ok := strings.CutPrefix(s, p.prefix); ok {
			return p.done, r, true
		}
	}
	return false, "", false
}

func mdBlocks(lines []string, off Off) []Block {
	var out []Block
	i := 0
	// 有序项承接上一项的编号。
	lastList := false
	for i < len(lines) {
		line := lines[i]
		// 空行是空段（第一个块之前没有）。
		if strings.TrimSpace(line) == "" {
			if len(out) > 0 && !off.File {
				out = append(out, BlockText(""))
			}
			lastList = false
			i++
			continue
		}
		ind := indentOf(line)
		body := strings.TrimSpace(line)
		// 围栏代码（闭合或到结尾）。
		if fc, n, info, isFence := mdFence(line); isFence && !off.CodeDivider {
			var code []string
			j := i + 1
			for j < len(lines) {
				if c2, n2, rest, ok := mdFence(lines[j]); ok && c2 == fc && n2 >= n && rest == "" {
					break
				}
				code = append(code, dedent(lines[j], ind))
				j++
			}
			lang := LangPlain
			if fields := strings.Fields(info); len(fields) > 0 {
				lang = LangResolve(fields[0])
			}
			out = append(out, NewBlock(Code{Lang: lang}, Plain(strings.Join(code, "\n"))))
			i = j + 1
			lastList = false
			continue
		}
		if m, col, content, ok := mdListItem(line); ok {
			_, _, todo := mdTodoPrefix(content)
			blocked := (todo && off.Todo) ||
				(!todo && !m.ordered && off.Bullet) ||
				(!todo && m.ordered && off.Ordered)
			if !blocked {
				// 本项的行：缩进超过记号的都算。
				j := i + 1
				var sub []string
				for j < len(lines) {
					l := lines[j]
					if strings.TrimSpace(l) == "" {
						// 空行结束本项，除非后面还有缩进内容。
						if j+1 < len(lines) && strings.TrimSpace(lines[j+1]) != "" && indentOf(lines[j+1]) > ind {
							j++
							continue
						}
						break
					}
					if indentOf(l) <= ind {
						break
					}
					sub = append(sub, dedent(l, min(col, indentOf(l))))
					j++
				}
				var kind BlockKind
				var text string
				if done, r, isTodo := mdTodoPrefix(content); isTodo {
					kind, text = Todo{Done: done}, r
				} else if m.ordered {
					cont := false
					if lastList && len(out) > 0 {
						_, cont = out[len(out)-1].Kind.(Ordered)
					}
					var seq *uint32
					if !cont && m.num != 1 {
						nv := m.num
						seq = &nv
					}
					kind = Ordered{Seq: seq}
					text = content
				} else {
					kind, text = Bullet{}, content
				}
				b := NewBlock(kind, mdInlineParse(strings.TrimSpace(text)))
				b.Children = mdBlocks(sub, off)
				out = append(out, b)
				lastList = true
				i = j
				continue
			}
		}
		lastList = false
		if done, rest, ok := mdTodoPrefix(body); ok && !off.Todo {
			out = append(out, NewBlock(Todo{Done: done}, mdInlineParse(strings.TrimSpace(rest))))
			i++
			continue
		}
		hashes := 0
		for hashes < len(body) && body[hashes] == '#' {
			hashes++
		}
		if hashes >= 1 && hashes <= 9 && len(body) > hashes && (body[hashes] == ' ' || body[hashes] == '\t') {
			t := strings.TrimSpace(body[hashes:])
			t = strings.TrimRight(t, "#")
			t = strings.TrimRightFunc(t, unicode.IsSpace)
			out = append(out, NewBlock(Heading{Level: uint8(hashes)}, mdInlineParse(t)))
			i++
			continue
		}
		if mdDivider(body) && !off.CodeDivider {
			out = append(out, NewBlock(Divider{}, RichText{}))
			i++
			continue
		}
		if _, isQuote := mdQuoteLine(body); isQuote && !off.Quote {
			var sub []string
			for i < len(lines) {
				r, ok := mdQuoteLine(strings.TrimSpace(lines[i]))
				if !ok {
					break
				}
				sub = append(sub, r)
				i++
			}
			quoteOff := off
			quoteOff.Quote = true
			kids := mdBlocks(sub, quoteOff)
			if len(kids) > 0 {
				b := NewBlock(Quote{}, RichText{})
				b.Children = kids
				out = append(out, b)
			}
			continue
		}
		if tb, n, ok := pipeTable(lines[i:]); ok {
			out = append(out, tb)
			i += n
			continue
		}
		if off.File {
			if src, ok := mdImageLine(body); ok {
				b := NewBlock(Image{SrcKind: "src", Src: src}, RichText{})
				b.Align = AlignCenter
				out = append(out, b)
				i++
				continue
			}
		}
		// 前导空格按字面保留。
		out = append(out, NewBlock(Text{}, mdInlineParse(mdTabs(strings.TrimRightFunc(line, unicode.IsSpace)))))
		i++
	}
	return out
}

// mdImageLine：整行的 `![alt](src "title")` 的 src。
func mdImageLine(s string) (string, bool) {
	rest, ok := strings.CutPrefix(s, "![")
	if !ok {
		return "", false
	}
	_, tail, ok := strings.Cut(rest, "](")
	if !ok {
		return "", false
	}
	inner, ok := strings.CutSuffix(tail, ")")
	if !ok {
		return "", false
	}
	src := inner
	if k := strings.IndexAny(inner, " \t"); k >= 0 {
		src = inner[:k]
	}
	src = strings.TrimLeft(strings.TrimRight(src, ">"), "<")
	if src == "" || strings.Contains(inner, ")") {
		return "", false
	}
	return src, true
}

// mdQuoteLine：引用行的内容。
func mdQuoteLine(s string) (string, bool) {
	if s == ">" {
		return "", true
	}
	if r, ok := strings.CutPrefix(s, ">"); ok {
		return strings.TrimPrefix(r, " "), true
	}
	if r, ok := strings.CutPrefix(s, "》 "); ok {
		return r, true
	}
	return "", false
}

func tableCells(line string) []string {
	t := strings.TrimSpace(line)
	t = strings.TrimPrefix(t, "|")
	if strings.HasSuffix(t, "|") && !strings.HasSuffix(t, `\|`) {
		t = t[:len(t)-1]
	}
	var cells []string
	var cur strings.Builder
	rs := []rune(t)
	for k := 0; k < len(rs); k++ {
		switch {
		case rs[k] == '\\' && k+1 < len(rs) && rs[k+1] == '|':
			cur.WriteByte('|')
			k++
		case rs[k] == '|':
			cells = append(cells, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteRune(rs[k])
		}
	}
	cells = append(cells, strings.TrimSpace(cur.String()))
	return cells
}

// pipeTable 是 lines 开头的 GFM 管道表：(表, 用掉的行数)。
func pipeTable(lines []string) (Block, int, bool) {
	if len(lines) < 2 {
		return Block{}, 0, false
	}
	head, sep := lines[0], lines[1]
	if !strings.Contains(head, "|") {
		return Block{}, 0, false
	}
	seps := tableCells(sep)
	okSep := func(c string) bool {
		c = strings.Trim(c, ":")
		if c == "" {
			return false
		}
		for i := 0; i < len(c); i++ {
			if c[i] != '-' {
				return false
			}
		}
		return true
	}
	for _, c := range seps {
		if !okSep(c) {
			return Block{}, 0, false
		}
	}
	if !strings.Contains(sep, "-") {
		return Block{}, 0, false
	}
	header := tableCells(head)
	ncols := len(header)
	if ncols != len(seps) {
		return Block{}, 0, false
	}
	aligns := make([]Align, ncols)
	for i, c := range seps {
		left := strings.HasPrefix(c, ":")
		right := strings.HasSuffix(c, ":")
		switch {
		case left && right:
			aligns[i] = AlignCenter
		case right:
			aligns[i] = AlignRight
		default:
			aligns[i] = AlignLeft
		}
	}
	n := 2
	rows := [][]string{header}
	for n < len(lines) {
		l := lines[n]
		if strings.TrimSpace(l) == "" || !strings.Contains(l, "|") {
			break
		}
		rows = append(rows, tableCells(l))
		n++
	}
	trows := make([][]TableCell, len(rows))
	for r, row := range rows {
		cells := make([]TableCell, ncols)
		for cidx := 0; cidx < ncols; cidx++ {
			v := ""
			if cidx < len(row) {
				v = row[cidx]
			}
			b := NewBlock(Text{}, mdInlineParse(v))
			b.Align = aligns[cidx]
			cells[cidx] = TableCell{Blocks: []Block{b}, Colspan: 1, Rowspan: 1}
		}
		trows[r] = cells
	}
	t := Table{ColWidths: repeatF32(200, ncols), HeaderRows: 1, Rows: trows}
	t.Repair()
	return NewBlock(t, RichText{}), n, true
}

func isRuneBoundary(r rune) bool {
	return r < 0 || isUnicodeSpace(r)
}

// mdInlineParse 是行内 Markdown：代码 span、公式、链接、裸 URL 和实时
// 快捷键的强调规则（`~x~` 是下划线）。转义、图片和 `<…>` 自动链接粘
// 贴时关闭。
func mdInlineParse(s string) RichText {
	var out RichText
	plain := ""
	flush := func() {
		if plain != "" {
			out.Push(plain, Marks{})
			plain = ""
		}
	}
	i := 0
	for i < len(s) {
		rest := s[i:]
		c, cs := utf8.DecodeRuneInString(rest)
		var prev rune = -1
		if i > 0 {
			prev, _ = utf8.DecodeLastRuneInString(s[:i])
		}
		// 代码 span。
		if c == '`' {
			n := 0
			for n < len(rest) && rest[n] == '`' {
				n++
			}
			delim := rest[:n]
			if end, ok := findRun(rest[n:], delim, '`'); ok {
				inner := rest[n : n+end]
				if len(inner) > 2 && inner[0] == ' ' && inner[len(inner)-1] == ' ' {
					inner = inner[1 : len(inner)-1]
				}
				if inner != "" {
					flush()
					out.Push(inner, Marks{Code: true})
					i += n + end + n
					continue
				}
			}
			plain += delim
			i += n
			continue
		}
		// 公式：`$$x$$` 与 `$x$`，两侧要空白/行界。
		if c == '$' && isRuneBoundary(prev) {
			done := false
			for _, delim := range []string{"$$", "$"} {
				if !strings.HasPrefix(rest, delim) || strings.HasPrefix(rest[len(delim):], "$") {
					continue
				}
				body := rest[len(delim):]
				end := strings.Index(body, delim)
				if end < 0 {
					continue
				}
				inner := body[:end]
				var after rune = -1
				if tail := body[end+len(delim):]; tail != "" {
					after, _ = utf8.DecodeRuneInString(tail)
				}
				// 数字前的 `$` 是钱，不是数学。
				digit := delim == "$" && inner != "" && inner[0] >= '0' && inner[0] <= '9'
				if digit || strings.TrimSpace(inner) == "" || strings.HasPrefix(inner, " ") || !isRuneBoundary(after) || after == '$' {
					continue
				}
				flush()
				obj := ToInline(Equation{Src: inner})
				out.Push(EquationRune, Marks{Inline: &obj})
				i += len(delim)*2 + end
				done = true
				break
			}
			if done {
				continue
			}
		}
		// 链接 `[text](url)`。
		if c == '[' {
			if label, url, used, ok := mdLinkAt(rest); ok {
				flush()
				t := mdInlineParse(label)
				if t.IsEmpty() {
					t = Plain(url)
				}
				length := t.Len()
				u := url
				t.UpdateMarks(0, length, func(m *Marks) { m.Link = &u })
				out.Append(&t)
				i += used
				continue
			}
		}
		// 裸 URL（绘制时变链接）。
		if (c == 'h' || c == 'w') && !(prev >= 0 && isAlnum(prev)) {
			l := rest
			if len(rest) > 8 {
				l = rest[:8]
			}
			l = strings.ToLower(l)
			if strings.HasPrefix(l, "https://") || strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "www.") {
				end := strings.IndexFunc(rest, func(r rune) bool {
					return isUnicodeSpace(r) || strings.ContainsRune("<>\"'`", r) || r >= utf8.RuneSelf
				})
				if end < 0 {
					end = len(rest)
				}
				url := strings.TrimRight(rest[:end], ".,;:!?)]}*_~")
				host := url
				if k := strings.Index(url, "://"); k >= 0 {
					host = url[k+3:]
				}
				if len(host) > 3 && strings.Contains(host, ".") {
					flush()
					href := url
					if strings.HasPrefix(l, "www.") {
						href = "https://" + url
					}
					out.Push(url, Marks{Link: &href})
					i += len(url)
					continue
				}
			}
		}
		// 强调。
		if c == '*' || c == '_' || c == '~' {
			n := 0
			for n < len(rest) && rest[n] == byte(c) {
				n++
			}
			run := rest[:n]
			var set func(*Marks)
			switch {
			case (c == '*' || c == '_') && n == 3:
				set = func(m *Marks) { m.Bold = true; m.Italic = true }
			case (c == '*' || c == '_') && n == 2:
				set = func(m *Marks) { m.Bold = true }
			case (c == '*' || c == '_') && n == 1:
				set = func(m *Marks) { m.Italic = true }
			case c == '~' && n == 2:
				set = func(m *Marks) { m.Strike = true }
			case c == '~' && n == 1:
				set = func(m *Marks) { m.Underline = true }
			}
			bounded := c == '_' || (c == '~' && n == 1)
			if set != nil && (!bounded || isRuneBoundary(prev)) {
				if end, ok := findRun(rest[n:], run, byte(c)); ok {
					inner := rest[n : n+end]
					var after rune = -1
					if tail := rest[n+end+n:]; tail != "" {
						after, _ = utf8.DecodeRuneInString(tail)
					}
					innerFirst, _ := utf8.DecodeRuneInString(inner)
					innerLast, _ := utf8.DecodeLastRuneInString(inner)
					if !isUnicodeSpace(innerFirst) && !isUnicodeSpace(innerLast) && (!bounded || isRuneBoundary(after)) {
						flush()
						t := mdInlineParse(inner)
						length := t.Len()
						t.UpdateMarks(0, length, set)
						out.Append(&t)
						i += n + end + n
						continue
					}
				}
			}
			plain += run
			i += n
			continue
		}
		plain += string(c)
		i += cs
	}
	flush()
	return out
}

// findRun 找 delim 的精确一次出现（不是更长 run 的一部分），跳过空匹配。
func findRun(s, delim string, ch byte) (int, bool) {
	from := 0
	for {
		k := strings.Index(s[from:], delim)
		if k < 0 {
			return 0, false
		}
		at := from + k
		beforeOK := at == 0 || s[at-1] != ch
		afterOK := at+len(delim) >= len(s) || s[at+len(delim)] != ch
		if at > 0 && beforeOK && afterOK {
			return at, true
		}
		from = at + max(len(delim), 1)
		for from < len(s) && s[from] == ch {
			from++
		}
	}
}

// mdLinkAt 是 s 开头的 `[label](url "title")`：(标签, url, 用掉的字节)。
func mdLinkAt(s string) (string, string, int, bool) {
	if s == "" || s[0] != '[' {
		return "", "", 0, false
	}
	depth := 0
	close := -1
	for k := 0; k < len(s); k++ {
		switch s[k] {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				close = k
			}
		}
		if close >= 0 {
			break
		}
	}
	if close < 0 {
		return "", "", 0, false
	}
	rest := s[close+1:]
	inner, ok := strings.CutPrefix(rest, "(")
	if !ok {
		return "", "", 0, false
	}
	end := strings.IndexByte(inner, ')')
	if end < 0 {
		return "", "", 0, false
	}
	target := strings.TrimSpace(inner[:end])
	url := ""
	if fields := strings.Fields(target); len(fields) > 0 {
		url = fields[0]
	}
	url = strings.Trim(url, "<>")
	if url == "" || !(LinkOK(url) || strings.HasPrefix(url, "www.")) {
		return "", "", 0, false
	}
	return s[1:close], url, close + 1 + 1 + end + 1, true
}

func repeatF32(v float32, n int) []float32 {
	out := make([]float32, n)
	for i := range out {
		out[i] = v
	}
	return out
}
