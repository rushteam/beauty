# matchmaker-room — 匹配 → 分配 GameServer 地址

模拟 **matchmaker → Agones Allocator → 客户端直连** 的流程(本地用地址池轮询,无需 K8s)。

## 运行

```bash
# 终端 1: 游戏服
go run ./examples/agones-room

# 终端 2: 匹配服
go run ./examples/matchmaker-room

# 入队两名玩家
curl "http://127.0.0.1:8288/queue?user=alice&region=eu&skill=1000"
curl "http://127.0.0.1:8288/queue?user=bob&region=eu&skill=1010"

# 匹配成功后查询分配
curl http://127.0.0.1:8288/assign?user=alice
# {"game_addr":"127.0.0.1:8130","ws_url":"ws://127.0.0.1:8130/ws?player=alice"}
```

多游戏服实例:

```bash
export BEAUTY_GAME_ADDRS=127.0.0.1:8130,127.0.0.1:8131
go run ./examples/matchmaker-room
```

生产环境将 `Allocator.Allocate()` 替换为 Agones [Allocator gRPC](https://agones.dev/site/docs/advanced/allocator/) 即可。
