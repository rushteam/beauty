package mdrt

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
)

var (
	slugRe    = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	fieldRe   = regexp.MustCompile(`^-\s+([A-Za-z_][A-Za-z0-9_]*)\s*(?:\((path|query|body|header)\))?\s*:\s*(string|number|integer|boolean|object|array)\s*(?:,\s*(.*))?$`)
	routeRe   = regexp.MustCompile(`^(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)\s+(/\S*)$`)
	cronRe    = regexp.MustCompile(`^cron\s+(\S+)\s+(\S.*)$`)
	hookRe    = regexp.MustCompile(`^hook\s+(\S+)$`)
	eventRe   = regexp.MustCompile(`^event\s+(\S+)$`)
	pathParam = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)(?:\.\.\.)?\}`)
	tokenRe   = regexp.MustCompile(`\$token\(([A-Za-z0-9_-]+)\)`)
)

var h2Names = map[string]string{
	"鉴权": "auth", "auth": "auth",
	"输入": "input", "input": "input",
	"行为": "behavior", "behavior": "behavior",
	"实现": "impl", "impl": "impl",
	"例子": "examples", "examples": "examples",
}

// Parse 把一个 Markdown 模块解析成 Spec。文件名(不含目录)必须与 frontmatter 的 name 一致。
// 有诊断时 spec 可能不完整,调用方应把诊断视为失败。
func Parse(filename, src string) (*Spec, Diagnostics) {
	base := path.Base(filename)
	text := strings.ReplaceAll(src, "\r\n", "\n")
	text = strings.TrimPrefix(text, "\uFEFF")
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	fm, bodyStart, err := splitFrontmatter(lines)
	if err != nil {
		return nil, Diagnostics{errorDiag(base, 1, "%s", err.Error())}
	}
	spec := &Spec{Path: base, Auth: AuthNone}
	diags := applyFrontmatter(spec, base, fm)

	p := &mdParser{file: base, lines: lines, spec: spec}
	p.run(bodyStart)
	diags = append(diags, p.diags...)
	diags = append(diags, validateSpec(spec)...)
	if len(diags) > 0 {
		return spec, diags
	}
	return spec, nil
}

func splitFrontmatter(lines []string) (string, int, error) {
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", 0, fmt.Errorf("缺少 frontmatter(文件需以 --- 开头)")
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return strings.Join(lines[1:i], "\n"), i + 1, nil
		}
	}
	return "", 0, fmt.Errorf("frontmatter 未闭合(缺少结尾 ---)")
}

func applyFrontmatter(spec *Spec, file, front string) Diagnostics {
	var diags Diagnostics
	vals, listErrs := parseFront(front)
	for _, e := range listErrs {
		diags = append(diags, errorDiag(file, e.line, "%s", e.msg))
	}
	spec.Dialect = vals.scalar["dialect"]
	spec.Slug = vals.scalar["name"]
	if spec.Dialect == "" {
		diags = append(diags, errorDiag(file, 1, "frontmatter 缺少 dialect"))
	} else if spec.Dialect != Dialect {
		diags = append(diags, errorDiag(file, 1, "不支持的方言 %q", spec.Dialect))
	}
	if spec.Slug == "" {
		diags = append(diags, errorDiag(file, 1, "frontmatter 缺少 name"))
	} else if !slugRe.MatchString(spec.Slug) {
		diags = append(diags, errorDiag(file, 1, "name %q 必须是小写字母、数字和连字符", spec.Slug))
	} else if spec.Slug != strings.TrimSuffix(file, ".md") {
		diags = append(diags, errorDiag(file, 1, "name %q 与文件名不一致", spec.Slug))
	}
	if auth, ok := vals.scalar["auth"]; ok && auth != "" {
		if auth != AuthNone && auth != AuthBearer {
			diags = append(diags, errorDiag(file, 1, "auth 只能是 none 或 bearer"))
		} else {
			spec.Auth = auth
		}
	}
	spec.HTTPAllow = append([]string(nil), vals.list["http"]...)
	for i, h := range spec.HTTPAllow {
		spec.HTTPAllow[i] = strings.ToLower(strings.TrimSpace(h))
	}
	if raw, ok := vals.scalar["now"]; ok && raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 {
			diags = append(diags, errorDiag(file, 1, "now 必须是非负整数毫秒"))
		} else {
			spec.NowMs = n
		}
	}
	if ev, ok := vals.scalar["eval"]; ok {
		switch ev {
		case "", "ok", "hostile":
			spec.Eval = ev
		default:
			diags = append(diags, errorDiag(file, 1, "eval 只能是 ok 或 hostile"))
		}
	}
	seen := map[string]bool{}
	for k := range vals.scalar {
		seen[k] = true
	}
	for k := range vals.list {
		seen[k] = true
	}
	for k := range seen {
		if !knownFrontKey(k) {
			diags = append(diags, errorDiag(file, 1, "未知的 frontmatter 字段 %q", k))
		}
	}
	return diags
}

func knownFrontKey(k string) bool {
	switch k {
	case "dialect", "name", "auth", "http", "now", "eval":
		return true
	default:
		return false
	}
}

type frontVals struct {
	scalar map[string]string
	list   map[string][]string
}

type frontErr struct {
	line int
	msg  string
}

func parseFront(front string) (frontVals, []frontErr) {
	vals := frontVals{
		scalar: map[string]string{},
		list:   map[string][]string{},
	}
	var errs []frontErr
	lines := strings.Split(front, "\n")
	for i := 0; i < len(lines); i++ {
		trim := strings.TrimSpace(lines[i])
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		if strings.HasPrefix(trim, "- ") {
			errs = append(errs, frontErr{line: i + 2, msg: "列表项缺少所属字段"})
			continue
		}
		key, rest, ok := strings.Cut(trim, ":")
		if !ok || strings.TrimSpace(key) == "" || strings.ContainsAny(strings.TrimSpace(key), " \t") {
			errs = append(errs, frontErr{line: i + 2, msg: "无法解析的 frontmatter 行"})
			continue
		}
		key = strings.TrimSpace(key)
		rest = strings.TrimSpace(rest)
		if rest == "" {
			var items []string
			for i+1 < len(lines) {
				next := strings.TrimSpace(lines[i+1])
				if !strings.HasPrefix(next, "- ") {
					break
				}
				items = append(items, unquote(strings.TrimSpace(strings.TrimPrefix(next, "- "))))
				i++
			}
			vals.list[key] = items
			continue
		}
		if strings.HasPrefix(rest, "[") && strings.HasSuffix(rest, "]") {
			inner := strings.TrimSpace(rest[1 : len(rest)-1])
			var items []string
			if inner != "" {
				for _, part := range strings.Split(inner, ",") {
					items = append(items, unquote(strings.TrimSpace(part)))
				}
			}
			vals.list[key] = items
			continue
		}
		vals.scalar[key] = unquote(rest)
	}
	return vals, errs
}

func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

type mdParser struct {
	file  string
	lines []string
	spec  *Spec
	diags Diagnostics

	unit    *Unit
	section string
	example *Example
	fence   *fence
	raw     []numberedLine
	seen    map[string]bool
}

type numberedLine struct {
	n    int
	text string
}

type fence struct {
	lang string
	line int
	body strings.Builder
	sec  string
}

func (p *mdParser) run(start int) {
	for i := start; i < len(p.lines); i++ {
		lineNo := i + 1
		line := p.lines[i]
		if p.fence != nil {
			if strings.TrimSpace(line) == "```" {
				p.closeFence()
				continue
			}
			if p.fence.body.Len() > 0 {
				p.fence.body.WriteByte('\n')
			}
			p.fence.body.WriteString(line)
			continue
		}
		switch {
		case strings.HasPrefix(line, "####"):
			p.diags = append(p.diags, errorDiag(p.file, lineNo, "不支持四级及更深的标题"))
		case strings.HasPrefix(line, "### ") || strings.HasPrefix(line, "###\t"):
			p.startExample(strings.TrimSpace(line[3:]), lineNo)
		case strings.HasPrefix(line, "## ") || strings.HasPrefix(line, "##\t"):
			p.startSection(strings.TrimSpace(line[2:]), lineNo)
		case strings.HasPrefix(line, "# ") || strings.HasPrefix(line, "#\t"):
			p.startUnit(strings.TrimSpace(line[1:]), lineNo)
		case strings.HasPrefix(strings.TrimSpace(line), "```"):
			p.openFence(strings.TrimSpace(line), lineNo)
		default:
			p.capture(line, lineNo)
		}
	}
	if p.fence != nil {
		p.diags = append(p.diags, errorDiag(p.file, p.fence.line, "代码块未闭合"))
	}
	p.finishUnit()
}

func (p *mdParser) capture(line string, lineNo int) {
	if p.unit == nil {
		if strings.TrimSpace(line) != "" {
			p.diags = append(p.diags, errorDiag(p.file, lineNo, "一级标题之前不能有正文"))
		}
		return
	}
	switch p.section {
	case "":
		if strings.TrimSpace(line) != "" {
			p.diags = append(p.diags, errorDiag(p.file, lineNo, "正文必须放在二级标题下"))
		}
	case "auth", "input", "behavior":
		p.raw = append(p.raw, numberedLine{n: lineNo, text: line})
	case "impl":
		if strings.TrimSpace(line) != "" {
			p.diags = append(p.diags, errorDiag(p.file, lineNo, "实现只能包含一个 js 代码块"))
		}
	case "examples":
		if strings.TrimSpace(line) != "" && p.example == nil {
			p.diags = append(p.diags, errorDiag(p.file, lineNo, "例子内容必须放在三级标题下"))
		}
	}
}

func (p *mdParser) startUnit(title string, line int) {
	p.finishUnit()
	u := &Unit{Line: line}
	switch {
	case routeRe.MatchString(title):
		m := routeRe.FindStringSubmatch(title)
		u.Kind, u.Method, u.Pattern = KindRoute, m[1], m[2]
	case cronRe.MatchString(title):
		m := cronRe.FindStringSubmatch(title)
		u.Kind, u.Name, u.CronSpec = KindCron, m[1], strings.TrimSpace(m[2])
	case hookRe.MatchString(title):
		u.Kind, u.Name = KindHook, hookRe.FindStringSubmatch(title)[1]
	case eventRe.MatchString(title):
		u.Kind, u.Name = KindEvent, eventRe.FindStringSubmatch(title)[1]
	default:
		p.diags = append(p.diags, errorDiag(p.file, line, "无法识别的一级标题 %q", title))
	}
	p.unit = u
	p.seen = map[string]bool{}
}

func (p *mdParser) startSection(title string, line int) {
	if p.unit == nil {
		p.diags = append(p.diags, errorDiag(p.file, line, "二级标题必须出现在一级标题之后"))
		return
	}
	p.flushSection()
	key := h2Names[strings.ToLower(title)]
	if key == "" {
		key = h2Names[title]
	}
	if key == "" {
		p.diags = append(p.diags, errorDiag(p.file, line, "无法识别的二级标题 %q", title))
		p.section = ""
		return
	}
	if p.seen[key] {
		p.diags = append(p.diags, errorDiag(p.file, line, "小节 %q 重复", title))
	}
	p.seen[key] = true
	p.section = key
	p.example = nil
	p.raw = nil
}

func (p *mdParser) startExample(title string, line int) {
	if p.unit == nil || p.section != "examples" {
		p.diags = append(p.diags, errorDiag(p.file, line, "三级标题只能出现在「例子」小节里"))
		return
	}
	p.unit.Examples = append(p.unit.Examples, Example{Title: title, Line: line})
	p.example = &p.unit.Examples[len(p.unit.Examples)-1]
}

func (p *mdParser) openFence(trim string, line int) {
	lang := strings.TrimSpace(strings.TrimPrefix(trim, "```"))
	sec := ""
	if p.unit != nil {
		sec = p.section
	}
	if sec == "" {
		p.diags = append(p.diags, errorDiag(p.file, line, "代码块必须出现在小节里"))
	}
	p.fence = &fence{lang: lang, line: line, sec: sec}
}

func (p *mdParser) closeFence() {
	f := p.fence
	p.fence = nil
	if f.sec == "" || p.unit == nil {
		return
	}
	body := f.body.String()
	switch f.sec {
	case "impl":
		if f.lang != "js" && f.lang != "javascript" {
			p.diags = append(p.diags, errorDiag(p.file, f.line, "实现代码块的语言必须是 js"))
			return
		}
		if p.unit.Impl != nil {
			p.diags = append(p.diags, errorDiag(p.file, f.line, "实现只能有一个代码块"))
			return
		}
		p.unit.Impl = &Text{Body: body, Line: f.line}
	case "examples":
		p.takeExampleFence(f.lang, body, f.line)
	default:
		p.diags = append(p.diags, errorDiag(p.file, f.line, "代码块不能出现在这个小节"))
	}
}

func (p *mdParser) takeExampleFence(lang, body string, line int) {
	if p.example == nil {
		p.diags = append(p.diags, errorDiag(p.file, line, "代码块必须放在某个例子的三级标题下"))
		return
	}
	switch lang {
	case "store":
		obj, err := decodeObject(body)
		if err != nil {
			p.diags = append(p.diags, errorDiag(p.file, line, "store 不是 JSON 对象: %s", err.Error()))
			return
		}
		if p.example.Store != nil {
			p.diags = append(p.diags, errorDiag(p.file, line, "store 重复"))
			return
		}
		p.example.Store = obj
	case "store-after":
		obj, err := decodeObject(body)
		if err != nil {
			p.diags = append(p.diags, errorDiag(p.file, line, "store-after 不是 JSON 对象: %s", err.Error()))
			return
		}
		if p.example.StoreAfter != nil {
			p.diags = append(p.diags, errorDiag(p.file, line, "store-after 重复"))
			return
		}
		p.example.StoreAfter = obj
	case "req":
		req, err := parseReq(body, line)
		if err != nil {
			p.diags = append(p.diags, errorDiag(p.file, line, "%s", err.Error()))
			return
		}
		p.example.Req = req
	case "res":
		res, err := parseRes(body, line)
		if err != nil {
			p.diags = append(p.diags, errorDiag(p.file, line, "%s", err.Error()))
			return
		}
		p.example.Res = res
	case "event":
		raw := strings.TrimSpace(body)
		if !json.Valid([]byte(raw)) {
			p.diags = append(p.diags, errorDiag(p.file, line, "event 不是 JSON"))
			return
		}
		p.example.Event = raw
	case "event-after":
		raw := strings.TrimSpace(body)
		if !json.Valid([]byte(raw)) {
			p.diags = append(p.diags, errorDiag(p.file, line, "event-after 不是 JSON"))
			return
		}
		p.example.EventAfter = raw
	case "stub":
		st, err := parseStub(body)
		if err != nil {
			p.diags = append(p.diags, errorDiag(p.file, line, "%s", err.Error()))
			return
		}
		p.example.Stubs = append(p.example.Stubs, st)
	default:
		p.diags = append(p.diags, errorDiag(p.file, line, "例子里不认识的代码块 %q", lang))
	}
}

func (p *mdParser) flushSection() {
	if p.unit == nil {
		return
	}
	lines := p.raw
	p.raw = nil
	switch p.section {
	case "auth":
		raw := strings.TrimSpace(joinNumbered(lines))
		line := p.unit.Line
		if len(lines) > 0 {
			line = lines[0].n
		}
		if raw != AuthNone && raw != AuthBearer {
			p.diags = append(p.diags, errorDiag(p.file, line, "鉴权只能是 none 或 bearer"))
			return
		}
		p.unit.Auth = raw
	case "input":
		p.parseInputs(lines)
	case "behavior":
		raw := strings.TrimSpace(joinNumbered(lines))
		if raw == "" {
			return
		}
		line := p.unit.Line
		for _, l := range lines {
			if strings.TrimSpace(l.text) != "" {
				line = l.n
				break
			}
		}
		p.unit.Behavior = &Text{Body: raw, Line: line}
	}
}

func joinNumbered(lines []numberedLine) string {
	var b strings.Builder
	for i, l := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(l.text)
	}
	return b.String()
}

func (p *mdParser) parseInputs(lines []numberedLine) {
	for _, line := range lines {
		trim := strings.TrimSpace(line.text)
		if trim == "" {
			continue
		}
		m := fieldRe.FindStringSubmatch(trim)
		if m == nil {
			p.diags = append(p.diags, errorDiag(p.file, line.n, "无法解析的输入行 %q", trim))
			continue
		}
		f := Field{Name: m[1], In: m[2], Type: m[3], Line: line.n}
		if m[4] != "" {
			if err := applyConstraints(&f, m[4]); err != nil {
				p.diags = append(p.diags, errorDiag(p.file, line.n, "字段 %s: %s", f.Name, err.Error()))
				continue
			}
		}
		p.unit.Inputs = append(p.unit.Inputs, f)
	}
}

func (p *mdParser) finishUnit() {
	if p.unit == nil {
		return
	}
	p.flushSection()
	if p.unit.Kind != "" {
		fillInputLocations(p.unit)
		p.spec.Units = append(p.spec.Units, *p.unit)
	}
	p.unit = nil
	p.section = ""
	p.example = nil
	p.seen = nil
	p.raw = nil
}

func fillInputLocations(u *Unit) {
	params := map[string]bool{}
	for _, m := range pathParam.FindAllStringSubmatch(u.Pattern, -1) {
		params[m[1]] = true
	}
	for i := range u.Inputs {
		f := &u.Inputs[i]
		if f.In != "" {
			continue
		}
		switch {
		case u.Kind == KindRoute && params[f.Name]:
			f.In = InPath
		case u.Kind == KindRoute && (u.Method == "GET" || u.Method == "DELETE"):
			f.In = InQuery
		default:
			f.In = InBody
		}
	}
}

func applyConstraints(f *Field, raw string) error {
	for _, part := range splitConstraints(raw) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		switch {
		case part == "可选" || part == "optional":
			f.Optional = true
		case strings.HasPrefix(part, ">="):
			n, err := strconv.ParseFloat(strings.TrimSpace(part[2:]), 64)
			if err != nil {
				return fmt.Errorf("无法解析 %q", part)
			}
			f.GTE = &n
		case strings.HasPrefix(part, "<="):
			n, err := strconv.ParseFloat(strings.TrimSpace(part[2:]), 64)
			if err != nil {
				return fmt.Errorf("无法解析 %q", part)
			}
			f.LTE = &n
		case strings.HasPrefix(part, ">"):
			n, err := strconv.ParseFloat(strings.TrimSpace(part[1:]), 64)
			if err != nil {
				return fmt.Errorf("无法解析 %q", part)
			}
			f.GT = &n
		case strings.HasPrefix(part, "<"):
			n, err := strconv.ParseFloat(strings.TrimSpace(part[1:]), 64)
			if err != nil {
				return fmt.Errorf("无法解析 %q", part)
			}
			f.LT = &n
		case strings.HasPrefix(part, "长度 ") || strings.HasPrefix(part, "len "):
			rest := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(part, "长度"), "len"))
			a, b, ok := strings.Cut(rest, "..")
			if !ok {
				return fmt.Errorf("长度要写成 a..b")
			}
			minL, err1 := strconv.Atoi(strings.TrimSpace(a))
			maxL, err2 := strconv.Atoi(strings.TrimSpace(b))
			if err1 != nil || err2 != nil || minL < 0 || maxL < minL {
				return fmt.Errorf("无法解析长度 %q", part)
			}
			f.MinLen, f.MaxLen = &minL, &maxL
		case strings.HasPrefix(part, "枚举 ") || strings.HasPrefix(part, "enum "):
			rest := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(part, "枚举"), "enum"))
			if rest == "" {
				return fmt.Errorf("枚举不能为空")
			}
			f.Enum = strings.Split(rest, "|")
			for i := range f.Enum {
				f.Enum[i] = strings.TrimSpace(f.Enum[i])
			}
		case strings.HasPrefix(part, "正则 ") || strings.HasPrefix(part, "pattern "):
			rest := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(part, "正则"), "pattern"))
			if len(rest) < 2 || !strings.HasPrefix(rest, "/") || !strings.HasSuffix(rest, "/") {
				return fmt.Errorf("正则要写成 /.../")
			}
			f.Pattern = rest[1 : len(rest)-1]
			if _, err := regexp.Compile(f.Pattern); err != nil {
				return fmt.Errorf("正则无法编译: %s", err.Error())
			}
		default:
			return fmt.Errorf("无法识别的约束 %q", part)
		}
	}
	return nil
}

func splitConstraints(s string) []string {
	var parts []string
	var b strings.Builder
	inSlash := false
	for _, r := range s {
		switch {
		case r == '/':
			inSlash = !inSlash
			b.WriteRune(r)
		case r == ',' && !inSlash:
			parts = append(parts, b.String())
			b.Reset()
		default:
			b.WriteRune(r)
		}
	}
	if b.Len() > 0 {
		parts = append(parts, b.String())
	}
	return parts
}

func decodeObject(body string) (map[string]any, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return map[string]any{}, nil
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(body), &obj); err != nil {
		return nil, err
	}
	if obj == nil {
		return nil, fmt.Errorf("必须是 JSON 对象")
	}
	return obj, nil
}

func parseReq(body string, line int) (*HTTPExample, error) {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return nil, fmt.Errorf("req 缺少请求行")
	}
	method, url, ok := strings.Cut(strings.TrimSpace(lines[0]), " ")
	if !ok || method == "" || url == "" {
		return nil, fmt.Errorf("req 首行必须是 METHOD path")
	}
	req := &HTTPExample{
		Method: strings.ToUpper(method),
		URL:    url,
		Header: map[string]string{},
		Line:   line,
	}
	i := 1
	for ; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "" {
			i++
			break
		}
		k, v, ok := strings.Cut(lines[i], ":")
		if !ok {
			return nil, fmt.Errorf("req 头 %q 缺少冒号", strings.TrimSpace(lines[i]))
		}
		req.Header[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	if i < len(lines) {
		req.Body = strings.TrimSpace(strings.Join(lines[i:], "\n"))
	}
	return req, nil
}

func parseRes(body string, line int) (*HTTPExample, error) {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return nil, fmt.Errorf("res 缺少状态码")
	}
	status, err := strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil || status < 100 || status > 599 {
		return nil, fmt.Errorf("res 首行必须是状态码")
	}
	res := &HTTPExample{Status: status, Line: line}
	if len(lines) > 1 {
		res.Body = strings.TrimSpace(strings.Join(lines[1:], "\n"))
	}
	return res, nil
}

func parseStub(body string) (Stub, error) {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	if len(lines) < 2 {
		return Stub{}, fmt.Errorf("stub 至少要有请求行和状态码")
	}
	method, url, ok := strings.Cut(strings.TrimSpace(lines[0]), " ")
	if !ok {
		return Stub{}, fmt.Errorf("stub 首行必须是 METHOD url")
	}
	status, err := strconv.Atoi(strings.TrimSpace(lines[1]))
	if err != nil {
		return Stub{}, fmt.Errorf("stub 第二行必须是状态码")
	}
	st := Stub{Method: strings.ToUpper(method), URL: url, Status: status}
	if len(lines) > 2 {
		st.Body = strings.TrimSpace(strings.Join(lines[2:], "\n"))
	}
	return st, nil
}

func validateSpec(spec *Spec) Diagnostics {
	var diags Diagnostics
	if spec == nil {
		return diags
	}
	for i := range spec.Units {
		u := &spec.Units[i]
		diags = append(diags, validateUnit(spec.Path, u)...)
	}
	return diags
}

func validateUnit(file string, u *Unit) Diagnostics {
	var diags Diagnostics
	if u.Kind == "" {
		return diags
	}
	if u.Kind != KindRoute && u.Auth != "" {
		diags = append(diags, errorDiag(file, u.Line, "只有路由可以写鉴权"))
	}
	if u.Kind == KindCron && len(u.Inputs) > 0 {
		diags = append(diags, errorDiag(file, u.Line, "定时任务没有输入"))
	}
	hasBehavior := u.Behavior != nil && strings.TrimSpace(u.Behavior.Body) != ""
	hasImpl := u.Impl != nil && strings.TrimSpace(u.Impl.Body) != ""
	switch {
	case hasBehavior && hasImpl:
		diags = append(diags, errorDiag(file, u.Line, "行为和实现只能写一个"))
	case !hasBehavior && !hasImpl:
		diags = append(diags, errorDiag(file, u.Line, "缺少行为或实现"))
	}
	if u.Behavior != nil && strings.TrimSpace(u.Behavior.Body) == "" {
		u.Behavior = nil
		hasBehavior = false
	}
	for _, ex := range u.Examples {
		diags = append(diags, validateExample(file, u, ex)...)
	}
	if !hasBehavior {
		return diags
	}
	if len(u.Examples) < 2 {
		diags = append(diags, errorDiag(file, u.Line, "行为段至少要两个例子"))
	}
	boundary := false
	for _, ex := range u.Examples {
		if isBoundary(ex) {
			boundary = true
		}
	}
	if len(u.Examples) >= 2 && !boundary {
		diags = append(diags, errorDiag(file, u.Line, "行为段需要一个错误或边界例子(状态码 >= 400,或标题含「边界/错误/非法/boundary/error/invalid」)"))
	}
	return diags
}

func validateExample(file string, u *Unit, ex Example) Diagnostics {
	var diags Diagnostics
	switch u.Kind {
	case KindRoute:
		if ex.Req == nil || ex.Res == nil {
			diags = append(diags, errorDiag(file, ex.Line, "路由例子需要 req 和 res"))
		}
	case KindHook, KindEvent:
		if ex.Event == "" && ex.Res == nil {
			diags = append(diags, errorDiag(file, ex.Line, "钩子和事件例子需要 event 或 res"))
		}
	case KindCron:
		if ex.Store == nil && ex.StoreAfter == nil && ex.Res == nil {
			diags = append(diags, errorDiag(file, ex.Line, "定时任务例子需要 store、store-after 或 res"))
		}
	}
	return diags
}

func isBoundary(ex Example) bool {
	if ex.Res != nil && ex.Res.Status >= 400 {
		return true
	}
	title := strings.ToLower(ex.Title)
	for _, w := range []string{"边界", "错误", "非法", "boundary", "error", "invalid"} {
		if strings.Contains(title, w) {
			return true
		}
	}
	return false
}
