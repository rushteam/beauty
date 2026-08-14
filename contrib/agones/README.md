# contrib/agones — GameServer 生命周期 × gameroom

把 [Agones](https://agones.dev/) SDK 的 **Ready / Health / Shutdown** 与 `pkg/gameroom` 房间 FSM 对齐。

```bash
go get github.com/rushteam/beauty/contrib/agones@latest
```

## 用法

```go
mgr := gameroom.New(gameroom.WithHooks(gameroom.Hooks{
    OnRunning: func(ctx context.Context, roomID string) error {
        // 启动 gameloop / 开放 WS
        return nil
    },
}))
handle, _ := agones.AllocateRoom(mgr, gameroom.Spec{ID: roomID, MaxPlayers: 16})

sdk, _ := agones.NewSDK() // Pod 内连接 sidecar
ctrl, _ := handle.Attach(sdk)
_ = ctrl.Run(ctx) // SDK 实现 Watcher 时自动监听 Shutdown → Drain
```

## Watcher

真实 `agones.SDK` 实现 `Watcher.WatchContext`:sidecar 将 GameServer 标为 **Shutdown** 时 cancel context,`Controller` 进入 Drain 宽限期后调用 `SDK.Shutdown()`。

测试与本地开发可注入 mock `Lifecycle`/`Watcher`,见 `controller_test.go` 与 [`examples/agones-room`](../../examples/agones-room)。

## Demo

```bash
go run ./examples/agones-room
```

依赖 `agones.dev/agones` v1.60+;仅在 `BEAUTY_USE_AGONES_SDK=1` 时连接 sidecar。
