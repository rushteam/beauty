# Kubernetes RBAC Configuration Guide

Beauty's `pkg/infra/k8s` uses the following Kubernetes resources. Before deployment, ensure the Pod's **ServiceAccount** has the required permissions—otherwise you will receive **403 Forbidden** at runtime, one of the most common pitfalls in Kubernetes deployments.

---

## Feature → Permission Mapping

| Feature | API Group | Resource | Required Verbs | Notes |
|---|---|---|---|---|
| **Leader election (Elector)** | `coordination.k8s.io` | `leases` | `get` `create` `update` | First election creates a Lease; the leader periodically updates to renew; candidates get to detect state |
| **ConfigMap config center** | `""` (core) | `configmaps` | `get` `list` `watch` | Get reads config; Watch listens for changes in real time |
| **Secret config center** | `""` (core) | `secrets` | `get` `list` `watch` | Same as above, for sensitive configuration |

> **Principle of least privilege**: grant only the permissions required for features you actually use. No leader election → no Lease permissions; no Secret config center → no secrets permissions.

---

## Complete YAML (copy and use)

The examples below assume the application is deployed in the `default` namespace with ServiceAccount name `my-app`. Replace `namespace` and `name` as appropriate.

### 1. ServiceAccount

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: my-app
  namespace: default
```

### 2. Role (trim as needed)

#### Leader election only

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

#### ConfigMap config center only

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

#### Secret config center only

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

#### Full feature set (leader election + ConfigMap + Secret)

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

Bind the Role to the ServiceAccount (replace `roleRef.name` with the Role name chosen above):

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: beauty-full-binding
  namespace: default
subjects:
- kind: ServiceAccount
  name: my-app                 # ← your ServiceAccount name
  namespace: default
roleRef:
  kind: Role
  name: beauty-full             # ← Role name created above
  apiGroup: rbac.authorization.k8s.io
```

### 4. Pod / Deployment referencing the ServiceAccount

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
      serviceAccountName: my-app   # ← critical: defaults to "default" SA, which typically has no permissions
      containers:
      - name: app
        image: my-app:latest
```

---

## Troubleshooting

### 1. Leader election failure: 403 Forbidden

```
k8s dlock: new leader elector: leases.coordination.k8s.io "my-lease" is forbidden:
  User "system:serviceaccount:default:default" cannot get resource "leases"
```

**Cause**: The Pod is using the `default` ServiceAccount, which has no Lease permissions.

**Fix**:
1. Create a dedicated ServiceAccount + Role + RoleBinding (YAML above)
2. Set `spec.template.spec.serviceAccountName` in the Deployment

### 2. Watch ConfigMap failure: 403 Forbidden

```
k8s config: establish watch failed: configmaps is forbidden:
  User "..." cannot watch resource "configmaps" in namespace "prod"
```

**Cause**: The Role is missing the `watch` verb, or the Role namespace does not match the one in use.

**Fix**: Ensure the Role `namespace` matches `WithNamespace("prod")` in code, and that verbs include `get`, `list`, and `watch`.

### 3. Cross-namespace

If leader election and configuration live in different namespaces, create a Role + RoleBinding in each namespace. Alternatively, use ClusterRole + ClusterRoleBinding (grants cluster-wide permissions—evaluate security impact carefully).

### 4. Verify current ServiceAccount permissions

```bash
# Check Lease get permission
kubectl auth can-i get leases.coordination.k8s.io \
  --namespace default \
  --as system:serviceaccount:default:my-app

# Check ConfigMap watch permission
kubectl auth can-i watch configmaps \
  --namespace default \
  --as system:serviceaccount:default:my-app
```

---

## Go Code Examples

### Leader Election

```go
import (
    "context"
    k8sdlock "github.com/rushteam/beauty/pkg/infra/k8s"
    "github.com/rushteam/beauty/pkg/dlock"
)

// Option 1: direct construction
elector, err := k8sdlock.NewElectorFromConfig("",
    k8sdlock.WithNamespace("default"),
    k8sdlock.WithIdentity("pod-a"),
)

// Option 2: DSN factory
elector, err := dlock.NewElector("k8s://?namespace=default&identity=pod-a")

// Run leader election
elector.Run(ctx, "my-cron-leader", func(leaderCtx context.Context) {
    // only the leader executes here
    runCronJobs(leaderCtx)
})
```

### ConfigMap Config Center

```go
import (
    "github.com/rushteam/beauty/pkg/conf"
    _ "github.com/rushteam/beauty/pkg/infra/k8s" // register configmap:// factory
)

// conf.New resolves the configmap:// scheme via the k8s factory
c, err := conf.New("configmap://default/app-config/app.yaml")
```
