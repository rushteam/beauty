# Kubernetes RBAC 配置指南

Beauty 的 `pkg/infra/k8s` 用到以下 k8s 资源,部署前必须确保 Pod 的 **ServiceAccount**
拥有对应权限——否则运行时会收到 **403 Forbidden**,这是 k8s 部署最常见的翻车点。

---

## 功能 → 权限对照表

| 功能 | API Group | Resource | 所需 Verbs | 说明 |
|---|---|---|---|---|
| **选主 (Elector)** | `coordination.k8s.io` | `leases` | `get` `create` `update` | 首次选主 create Lease,之后 leader 定期 update 续期,候选者 get 检测 |
| **ConfigMap 配置中心** | `""` (core) | `configmaps` | `get` `list` `watch` | Get 读取配置,Watch 实时监听变更 |
| **Secret 配置中心** | `""` (core) | `secrets` | `get` `list` `watch` | 同上,用于敏感配置 |

> **最小权限原则**:只授予实际使用的功能所需权限。不用选主就不给 Lease 权限;
> 不用 Secret 配置中心就不给 secrets 权限。

---

## 完整 YAML(复制即用)

以下示例假设应用部署在 `default` 命名空间、ServiceAccount 名为 `my-app`。
按实际情况替换 `namespace` 和 `name`。

### 1. ServiceAccount

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: my-app
  namespace: default
```

### 2. Role（推荐按需裁剪）

#### 仅选主

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: beauty-leader-election
  namespace: default
rules:
- apiGroups: ["coordination.k8s.io"]
  resources: ["leases"]
  verbs: ["get", "create", "update"]
```

#### 仅 ConfigMap 配置中心

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: beauty-config-reader
  namespace: default
rules:
- apiGroups: [""]
  resources: ["configmaps"]
  verbs: ["get", "list", "watch"]
```

#### 仅 Secret 配置中心

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: beauty-secret-reader
  namespace: default
rules:
- apiGroups: [""]
  resources: ["secrets"]
  verbs: ["get", "list", "watch"]
```

#### 全功能合并版（选主 + ConfigMap + Secret）

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: beauty-full
  namespace: default
rules:
- apiGroups: ["coordination.k8s.io"]
  resources: ["leases"]
  verbs: ["get", "create", "update"]
- apiGroups: [""]
  resources: ["configmaps"]
  verbs: ["get", "list", "watch"]
- apiGroups: [""]
  resources: ["secrets"]
  verbs: ["get", "list", "watch"]
```

### 3. RoleBinding

将 Role 绑定到 ServiceAccount（按上面选择的 Role 名替换 `roleRef.name`）：

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: beauty-full-binding
  namespace: default
subjects:
- kind: ServiceAccount
  name: my-app                 # ← 你的 ServiceAccount 名
  namespace: default
roleRef:
  kind: Role
  name: beauty-full             # ← 上面创建的 Role 名
  apiGroup: rbac.authorization.k8s.io
```

### 4. Pod / Deployment 引用 ServiceAccount

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-app
  namespace: default
spec:
  replicas: 3
  selector:
    matchLabels:
      app: my-app
  template:
    metadata:
      labels:
        app: my-app
    spec:
      serviceAccountName: my-app   # ← 关键：不设置则用 "default" SA，通常没有权限
      containers:
      - name: app
        image: my-app:latest
```

---

## 常见问题排查

### 1. 选主失败：403 Forbidden

```
k8s dlock: new leader elector: leases.coordination.k8s.io "my-lease" is forbidden:
  User "system:serviceaccount:default:default" cannot get resource "leases"
```

**原因**：Pod 使用了 `default` ServiceAccount,它没有 Lease 权限。

**解法**：
1. 创建专用 ServiceAccount + Role + RoleBinding（上面的 YAML）
2. 在 Deployment 的 `spec.template.spec.serviceAccountName` 指定它

### 2. Watch ConfigMap 失败：403 Forbidden

```
k8s config: establish watch failed: configmaps is forbidden:
  User "..." cannot watch resource "configmaps" in namespace "prod"
```

**原因**：Role 中缺少 `watch` verb,或 Role 的 namespace 与实际使用的不一致。

**解法**：确认 Role 的 `namespace` 与代码中 `WithNamespace("prod")` 一致,且 verbs 包含
`get`、`list`、`watch` 三个。

### 3. 跨命名空间

如果选主和配置分布在不同的命名空间,需要在每个命名空间各创建一个 Role + RoleBinding。
或者使用 ClusterRole + ClusterRoleBinding（但会授予集群范围的权限,注意评估安全影响）。

### 4. 确认当前 ServiceAccount 权限

```bash
# 检查是否有 Lease 的 get 权限
kubectl auth can-i get leases.coordination.k8s.io \
  --namespace default \
  --as system:serviceaccount:default:my-app

# 检查是否有 ConfigMap 的 watch 权限
kubectl auth can-i watch configmaps \
  --namespace default \
  --as system:serviceaccount:default:my-app
```

---

## Go 代码示例

### 选主

```go
import (
    "context"
    k8sdlock "github.com/rushteam/beauty/pkg/infra/k8s"
    "github.com/rushteam/beauty/pkg/store/dlock"
)

// 方式一：直接构造
elector, err := k8sdlock.NewElectorFromConfig("",
    k8sdlock.WithNamespace("default"),
    k8sdlock.WithIdentity("pod-a"),
)

// 方式二：DSN 工厂
elector, err := dlock.NewElector("k8s://?namespace=default&identity=pod-a")

// 运行选主
elector.Run(ctx, "my-cron-leader", func(leaderCtx context.Context) {
    // 只有 leader 才会执行这里
    runCronJobs(leaderCtx)
})
```

### ConfigMap 配置中心

```go
import (
    "github.com/rushteam/beauty/pkg/conf"
    _ "github.com/rushteam/beauty/pkg/infra/k8s" // 注册 configmap:// 工厂
)

// conf.New 会用 configmap:// scheme 找到 k8s 工厂
c, err := conf.New("configmap://default/app-config/app.yaml")
```
