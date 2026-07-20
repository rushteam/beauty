# contrib/casbin —— pkg/authz 的 Casbin 引擎(独立模块)

用 [Casbin](https://casbin.org) 实现 `pkg/authz.Enforcer`:RBAC(角色继承/域)、ABAC、策略文件 / DB
adapter 等 Casbin 全部模型能力。应用面向 `authz.Enforcer` 编程,可在内置 RBAC 与 Casbin 间无缝替换。

```bash
go get github.com/rushteam/beauty/contrib/casbin@latest
```

## 用法

```go
import (
    casbinlib "github.com/casbin/casbin/v2"
    casbinx "github.com/rushteam/beauty/contrib/casbin"
    "github.com/rushteam/beauty/pkg/authz"
)

e, _ := casbinlib.NewEnforcer("model.conf", "policy.csv") // 或自建 model/adapter
var enforcer authz.Enforcer = casbinx.New(e)

// 直接用,或挂中间件:
mux.Handle("/article/", authz.HTTP(enforcer, mapper)(handler))
```

## 映射

- **默认**:把 `Subject` 的**每个角色**分别作主体去 `Enforce(role, resource, action)`,任一放行即放行
  ——契合"角色来自 token、Casbin 存权限(p 规则)"。
- **`WithSubjectID()`**:改用 `Subject.ID` 作主体,角色由 Casbin 的 `g` 分组策略解析。
- **`WithMapper(fn)`**:完全自定义(ABAC——把 `Subject.Attrs` 一并传给 Casbin)。

`Casbin()` 返回底层 `*casbin.Enforcer`,用于加载/热更策略、管理角色。单测用进程内 model+policy 字符串。
