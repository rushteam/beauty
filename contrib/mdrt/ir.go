package mdrt

import "fmt"

const (
	// Dialect 是 v0 方言标识,写在 frontmatter 里。
	Dialect = "mdrt/v0"
	// PreludeVersion 变了就要让行为段缓存失效。
	PreludeVersion = "1"
	// PromptVersion 变了就要让行为段缓存失效。
	PromptVersion = "1"
	// PreludeFile 是随运行时发布的入口脚本,按文件名排在所有生成物之前。
	PreludeFile = "000_mdrt_prelude.js"
	// PlaceholderBody 在行为段还没有缓存、也还没编译完时上线。
	PlaceholderBody = `ctx.fail(503, "mdrt: behavior not compiled");`

	maxBodyBytes = 8 << 10
)

// Mode 决定监听、是否调用模型,以及缓存缺失时怎么处理。
type Mode int

const (
	// ModeDev 监听文件。缓存缺失先上占位实现,再在后台编译。
	ModeDev Mode = iota
	// ModeOffline 监听文件,但不调用模型。
	ModeOffline
	// ModeBuild 不监听。缓存缺失且没有模型客户端时失败。
	ModeBuild
)

func (m Mode) String() string {
	switch m {
	case ModeDev:
		return "dev"
	case ModeOffline:
		return "offline"
	case ModeBuild:
		return "build"
	default:
		return fmt.Sprintf("mode(%d)", int(m))
	}
}

// 单元种类。
const (
	KindRoute = "route"
	KindCron  = "cron"
	KindHook  = "hook"
	KindEvent = "event"
)

// 入参位置。
const (
	InPath   = "path"
	InQuery  = "query"
	InBody   = "body"
	InHeader = "header"
)

// 鉴权。空字符串按 none 处理。
const (
	AuthNone   = "none"
	AuthBearer = "bearer"
)

// Diagnostic 是一条带行号的诊断,形状对齐 LSP。
type Diagnostic struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

// Diagnostics 是一次解析或验收攒下的诊断。
type Diagnostics []Diagnostic

func (d Diagnostics) Error() string {
	if len(d) == 0 {
		return "mdrt: 无诊断"
	}
	b := make([]byte, 0, len(d)*48)
	for i, item := range d {
		if i > 0 {
			b = append(b, '\n')
		}
		b = append(b, []byte(fmt.Sprintf("%s:%d: %s", item.File, item.Line, item.Message))...)
	}
	return string(b)
}

func errorDiag(file string, line int, format string, args ...any) Diagnostic {
	return Diagnostic{
		File:     file,
		Line:     line,
		Severity: "error",
		Message:  fmt.Sprintf(format, args...),
	}
}

// Spec 是一个 Markdown 模块。
type Spec struct {
	Slug      string
	Path      string
	Dialect   string
	Auth      string
	HTTPAllow []string
	NowMs     int64
	Eval      string
	Units     []Unit
}

// Unit 是一个路由、定时任务、钩子或事件订阅。
type Unit struct {
	Kind     string
	Line     int
	Method   string
	Pattern  string
	Name     string
	CronSpec string
	Auth     string // 空表示沿用文件默认
	Inputs   []Field
	Behavior *Text
	Impl     *Text
	Examples []Example
	Pending  bool
}

// Text 是一块带起始行号的原文。
type Text struct {
	Body string
	Line int
}

// Field 是一个入参。数值边界用指针区分「未设置」和 0。
type Field struct {
	Name     string
	Type     string
	In       string
	Optional bool
	GT       *float64
	GTE      *float64
	LT       *float64
	LTE      *float64
	MinLen   *int
	MaxLen   *int
	Enum     []string
	Pattern  string
	Line     int
}

// Example 是一个验收用例。
type Example struct {
	Title      string
	Line       int
	Store      map[string]any
	StoreAfter map[string]any
	Req        *HTTPExample
	Res        *HTTPExample
	Event      string
	EventAfter string
	Stubs      []Stub
}

// HTTPExample 是例子里的请求或响应。Body 为空表示没有正文。
type HTTPExample struct {
	Method string
	URL    string
	Header map[string]string
	Status int
	Body   string
	Line   int
}

// Stub 是例子声明的外部 HTTP 桩。
type Stub struct {
	Method string
	URL    string
	Status int
	Body   string
}

// effectiveAuth 返回单元实际使用的鉴权。
func (u Unit) effectiveAuth(fileAuth string) string {
	if u.Auth != "" {
		return u.Auth
	}
	if fileAuth == "" {
		return AuthNone
	}
	return fileAuth
}
