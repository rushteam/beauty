# config —— 配置热更新

演示 `beauty.WithConfig` 接入本地/远程配置源，文件变更时自动重载并回调业务逻辑。

## 运行

在 `examples/config` 目录下创建 `config.yaml` 后启动：

```bash
cd examples/config
cat > config.yaml <<'EOF'
name: demo
port: ${APP_PORT:-8080}
password: ${env:DB_PASSWORD:-dev-pass}
EOF
go run .
```

访问 `http://localhost:8080/config`；修改 `config.yaml` 保存后，日志会打印 `(re)loaded`。

## 说明

- `conf.New("config.yaml")` 加载本地 YAML；远程可换 `etcd://127.0.0.1:2379/app/config.yaml`（需匿名导入 `pkg/infra/etcd`）。
- `WithConfig(loader, callback)` 在首次启动和每次变更时调用 callback，示例用 `atomic.Pointer` 保存最新配置。
- 生产环境把 callback 里的字段映射到运行时选项（端口、开关、限流阈值等），避免重启进程。

## 严格字段校验

`conf.New(path, conf.WithStrictFields())`（或 `conf.Load(path, &cfg, conf.WithStrictFields())`）开启后，
配置里出现结构体中不存在的字段（常见于 key 拼写错误）会直接返回错误，而不是被静默忽略：

```yaml
name: demo
prot: 8080   # 拼错的 port → 严格模式下报错；默认模式下被忽略
```

不传该选项即保持向后兼容的宽松行为。

## 占位符展开（WithSecrets）

用 `conf.WithSecrets(loader)` 包装后，字符串字段里的 `${...}` 占位符会在每次 `Unmarshal` 后展开：

| 写法 | 含义 |
| --- | --- |
| `${env:NAME}` | 环境变量 `NAME`，未设置且无默认值时报错 |
| `${env:NAME:-8080}` | 环境变量 `NAME`，缺省时用 `8080` |
| `${PORT}` | 裸变量，等价 `${env:PORT}` |
| `${PORT:-8080}` | 裸变量 + 默认值（shell 风格） |
| `${file:/run/secrets/x}` | 读取文件内容（去掉尾部换行） |

密钥与配置分离：YAML 里只写占位符，真实值通过环境变量或挂载文件注入，避免明文入库。
