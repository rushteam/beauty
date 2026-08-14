# matchmaker-room — 匹配 → 分配 GameServer 地址

模拟 **matchmaker → Agones Allocator → 客户端直连** 的流程。

```bash
# 终端 1: 游戏服
go run ./examples/agones-room

# 终端 2: 匹配服(启动时自带 alice+bob 自测)
go run ./examples/matchmaker-room
```

## Allocator 模式

| 模式 | 环境变量 |
|------|----------|
| 地址池 mock(默认) | `BEAUTY_GAME_ADDRS=127.0.0.1:8130` |
| gRPC Allocator | `BEAUTY_AGONES_ALLOCATOR=host:443` + mTLS 或 `BEAUTY_AGONES_ALLOCATOR_INSECURE=1` |

手动:

```bash
curl "http://127.0.0.1:8288/queue?user=alice&region=eu&skill=1000"
curl "http://127.0.0.1:8288/queue?user=bob&region=eu&skill=1010"
curl http://127.0.0.1:8288/assign?user=alice
```
