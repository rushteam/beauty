package console

import "context"

// Flag 是命令的行为标记,可按位组合。
type Flag int

const (
	// FlagPublic 表示无需登录即可执行。未标记的命令在开启鉴权时需登录后执行。
	FlagPublic Flag = 1 << iota
	// FlagInvisible 表示不在补全提示与 help 列表中显示(仍可执行)。
	FlagInvisible
)

// Ctx 是命令执行上下文,携带参数与连接信息。
type Ctx struct {
	// Args 为按空白切分后的完整参数,Args[0] 是命令名。
	Args []string
	// Authorized 表示当前连接是否已登录(开发模式下恒为 true)。
	Authorized bool
	// Remote 是客户端地址。
	Remote string

	srv *Server
	cl  *client
}

// Handler 执行命令并返回展示给前端的文本结果。返回 error 时前端展示错误信息。
type Handler func(ctx context.Context, c *Ctx) (string, error)

// Command 描述一个控制台命令。
type Command struct {
	// Name 是命令名(用户输入的第一个 token)。
	Name string
	// Note 是命令说明,用于 help 与补全提示。
	Note string
	// Example 是用法示例,用于补全提示。
	Example string
	// Flag 是行为标记(FlagPublic / FlagInvisible)。
	Flag Flag
	// Handler 是执行逻辑,不可为空。
	Handler Handler
}

func (c *Command) isPublic() bool    { return c.Flag&FlagPublic != 0 }
func (c *Command) isInvisible() bool { return c.Flag&FlagInvisible != 0 }
