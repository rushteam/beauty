# config —— 配置热更新

演示 `beauty.WithConfig` 接入本地/远程配置源，文件变更时自动重载并回调业务逻辑。

## 运行

在 `examples/config` 目录下创建 `config.yaml` 后启动：

```bash
cd examples/config
cat > config.yaml <<'EOF'
name: demo
port: 8080
EOF
go run .
```

访问 `http://localhost:8080/config`；修改 `config.yaml` 保存后，日志会打印 `(re)loaded`。

## 说明

- `conf.New("config.yaml")` 加载本地 YAML；远程可换 `etcd://127.0.0.1:2379/app/config.yaml`（需匿名导入 `pkg/infra/etcd`）。
- `WithConfig(loader, callback)` 在首次启动和每次变更时调用 callback，示例用 `atomic.Pointer` 保存最新配置。
- 生产环境把 callback 里的字段映射到运行时选项（端口、开关、限流阈值等），避免重启进程。
