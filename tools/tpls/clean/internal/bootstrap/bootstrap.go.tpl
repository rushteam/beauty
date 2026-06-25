// Package bootstrap 是组合根（Composition Root）：
// 唯一允许 import 所有层的地方，负责把各环的依赖接线在一起。
package bootstrap

import (
	nethttp "net/http"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/service/webserver"

	httpadapter "{{.ImportPath}}internal/adapter/http"
	useruc "{{.ImportPath}}internal/application/user"
	"{{.ImportPath}}internal/infra/config"
	"{{.ImportPath}}internal/infra/persistence/memory"
)

// New 装配依赖并返回 beauty 应用。
//
//	infra(出站) -> application(用例) -> adapter(入站) -> beauty 服务
func New(cfg *config.Config) *beauty.App {
	// Ring 3 出站适配器：仓储实现（可替换为 DB 实现而不动内层）
	userRepo := memory.NewUserRepo()

	// Ring 2 用例：仅依赖端口接口
	userService := useruc.New(userRepo)

	// Ring 3 入站适配器：HTTP handler 注册路由
	mux := nethttp.NewServeMux()
	httpadapter.NewUserHandler(userService).Register(mux)

	return beauty.New(
		beauty.WithService(webserver.New(cfg.HTTP.Addr, mux,
			webserver.WithServiceName(cfg.App),
		)),
		beauty.WithTrace(),
	)
}
