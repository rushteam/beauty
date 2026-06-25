# {{.Name}}

基于 Beauty + 整洁架构（Clean Architecture）的服务骨架。

## 四环分层（依赖一律指向圆心）

```
internal/
├── domain/                  # Ring 1 实体 + 端口接口(repository/)，纯业务规则
├── application/             # Ring 2 用例编排（只依赖 domain 端口）
├── adapter/http/            # Ring 3 入站适配器（HTTP -> 用例）
├── infra/                   # Ring 3~4 出站适配器 + 驱动
│   ├── config/              #   Ring 4 配置
│   ├── log/                 #   Ring 4 日志（带 trace 关联）
│   └── persistence/memory/  #   Ring 3 仓储实现（实现 domain 端口）
└── bootstrap/               # 组合根：唯一允许 import 所有层的地方
```

**核心规则**：`application` 禁止 import `internal/infra`；端口接口定义在消费方（domain）。

## 运行

```bash
make run        # go run
make build      # 注入版本信息编译
go run . --config config/dev/app.yaml
```

## 试一下

```bash
curl -XPOST localhost:8080/api/v1/users -d '{"name":"alice","email":"a@b.com"}'
curl localhost:8080/api/v1/users
```

## 替换仓储实现

把 `internal/infra/persistence/memory` 换成数据库实现（实现同一个
`domain/repository.UserRepository` 端口），只需改组合根 `internal/bootstrap`，
内层 domain/application 完全不动。
