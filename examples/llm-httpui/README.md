# llm-httpui — Agent SSE + checkpoint 回放

暴露 `contrib/llm/agent/httpui.Handler`:

- `POST /run` — RunStream SSE
- `POST /continue` — ContinueStream SSE
- `GET /events?run_id=` — checkpoint 事件回放

```bash
go run ./examples/llm-httpui
```

默认 `127.0.0.1:8299`,启动时自带 stub agent 自测。
