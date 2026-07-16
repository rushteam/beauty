# gameloop —— 基于 beauty 原语搭帧同步(lockstep)骨架

这是一个**参考示例**,演示怎么用 beauty 现有原语拼出帧同步/状态同步的服务端骨架,
而**不**把「同步引擎」塞进框架。核心是一条边界:

> 框架给**机制**(定步长循环、输入聚合、扇出、生命周期);
> 游戏给**策略**(同步方式、确定性、序列化、快照/AOI)。

## 结构

```
pkg/gameloop/gameloop.go   机制原语 gameloop.Room —— 只依赖 pkg/stream,零框架耦合
examples/gameloop/main.go  lockstep demo:pkg/ws 接连接 + 3 个进程内 bot 自校验
```

`pkg/gameloop.Room[In, Out]` 只做四件事:

- **定步长 tick**:按固定频率推进逻辑帧(lockstep 命脉是「所有端帧率一致」);
- **输入聚合**:并发 `Push` 收集输入,每帧原子取走「上一帧以来的全部输入」;
- **扇出**:把 `OnTick` 产出的东西经 `stream.Broadcaster` 推给所有订阅连接;
- **生命周期**:结构上满足 `beauty.Service` + `ReadyNotifier`,`beauty.WithService(room)` 直接挂进框架、随 app 优雅停机。

它**不懂**帧同步还是状态同步——策略全在你传入的 `Handler.OnTick` 里:

```go
// 帧同步:每帧原样广播「本帧全体输入」,客户端确定性重放
gameloop.HandlerFunc[Cmd, Frame](func(frame uint64, inputs []gameloop.PlayerInput[Cmd]) []Frame {
    return []Frame{{Frame: frame, Inputs: inputs}}
})

// 状态同步(另一种写法):在这里跑服务器权威模拟,用 pkg/spatial 做 AOI,产出快照/增量
```

## 运行

```bash
go run ./examples/gameloop
```

输出(3 个 bot 连上 20Hz 房间,各自发输入、收帧,最后自校验):

```
──────── lockstep 校验 ────────
客户端数:            3
共同帧数:            29
含输入的帧数:        9
逐帧输入全端一致:    ✅ 是(lockstep 传输不变量成立)
  样例: frame 4 -> [alice#1 bob#1 carol#1]
```

校验的是**框架该保证的东西**:所有客户端在同一帧号上收到**完全相同**的输入集合
(lockstep 传输不变量)。至于「同样的输入能否重放出同样的画面」——那是客户端游戏
逻辑的确定性,是**你的**责任,不在框架内,本示例也刻意不去验证。

> 想看**状态同步**版(同一个 `Room`,换 `OnTick` 下发"状态"而非"输入",并用
> `pkg/spatial` 做 AOI 视野过滤):见 [`examples/statesync`](../statesync)。

## 复用到的 beauty 原语

| 原语 | 作用 |
|---|---|
| `pkg/stream.Broadcaster` | 每帧下发扇出给 N 个连接,慢客户端丢帧不拖垮循环 |
| `pkg/ws` | WebSocket 连接:读循环 `Push` 输入、写循环写回每帧 |
| `beauty.WithService` | 把房间挂进 app 生命周期(和 cron 同构) |

> 生产化提示:高频动作类受 WebSocket(TCP)队头阻塞限制,需要 UDP/KCP;下发建议
> 换紧凑二进制(protobuf/flatbuffers)替 JSON;多房间要按房间做粘连路由(presence +
> 一致性哈希/dlock)。这些示例未涉及。
