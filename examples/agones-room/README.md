# agones-room — gameroom + gameloop + Agones 生命周期

演示 **Dedicated GameServer** 在 beauty 上的最小闭环:

```
Agones Ready → gameroom.Running → WS 接入 → Ctrl+C / Shutdown → Drain → SDK.Shutdown
```

## 本地运行(无需 K8s)

```bash
go run ./examples/agones-room
# ws://127.0.0.1:8130/ws?player=alice
curl http://127.0.0.1:8130/health
```

Ctrl+C 会触发 `gameroom.Drain` 与 `SDK.Shutdown`(dev 模式只打日志)。

## 集群运行(真实 Agones sidecar)

```bash
export BEAUTY_USE_AGONES_SDK=1
# Pod 内 sidecar 默认 localhost:9357
go run ./examples/agones-room
```

`contrib/agones.Controller` 在 SDK 实现 `Watcher` 时自动监听 GameServer **Shutdown** 状态并 Drain。

## K8s Fleet 骨架

```yaml
apiVersion: agones.dev/v1
kind: Fleet
metadata:
  name: beauty-room
spec:
  replicas: 3
  template:
    spec:
      ports:
        - name: game
          portPolicy: Dynamic
          containerPort: 8130
      template:
        spec:
          containers:
            - name: game
              image: your-registry/beauty-agones-room:latest
              env:
                - name: BEAUTY_USE_AGONES_SDK
                  value: "1"
```

匹配服通过 [Agones Allocator](https://agones.dev/site/docs/advanced/allocator/) 拿到 `status.address` 后,客户端连 `ws://<ip>:<port>/ws?player=...`。

## 相关包

| 包 | 作用 |
|---|---|
| `pkg/gameroom` | 房间 FSM(Waiting→Running→Draining) |
| `pkg/gameloop` | 定步 tick + 输入扇出 |
| `contrib/agones` | Ready/Health/Shutdown + WatchContext |

增量同步见 [`statesync-delta`](../statesync-delta)、[`statesync-quic-delta`](../statesync-quic-delta);Agones 模块说明见 [`contrib/agones/README.md`](../../contrib/agones/README.md)。
