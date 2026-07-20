# contrib/openfga —— pkg/authz 的 OpenFGA 关系授权(独立模块)

用 [OpenFGA](https://openfga.dev)(Google Zanzibar 式 **ReBAC**)实现 `pkg/authz.Enforcer`,适合
"X 是文档 Y 的编辑者"这类细粒度、基于关系的权限。薄封装官方 `openfga/go-sdk`,通过 Check API 判定。

```bash
go get github.com/rushteam/beauty/contrib/openfga@latest
```

## 用法

```go
import (
    ofga "github.com/rushteam/beauty/contrib/openfga"
    "github.com/rushteam/beauty/pkg/authz"
)

var enforcer authz.Enforcer, _ = ofga.New("http://127.0.0.1:8080", storeID)
mux.Handle("/doc/", authz.HTTP(enforcer, mapper)(handler))
```

## 映射(authz → OpenFGA 元组)

默认:`user = "user:"+Subject.ID`、`relation = action`、`object = resource`;`WithMapper` 可自定义
(如按类型加前缀 `document:`+id)。授权模型(类型/关系定义)与关系元组写入在 **OpenFGA 侧管理**,
本模块只做 `Check`;`Client()` 暴露底层 SDK 供写元组/管模型。

需要一个可达的 OpenFGA 服务(`ApiUrl` + `StoreId`)。单测用 httptest 打桩 check 端点;真服务互操作请在具备 OpenFGA 的环境验证。
