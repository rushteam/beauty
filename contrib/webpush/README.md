# contrib/webpush

Web Push（VAPID）订阅生命周期与发送，供 PWA / Service Worker 使用。

```go
keys, _ := webpush.GenerateVAPIDKeys()
store := webpush.NewMemStore()
client := webpush.NewClient(keys, "mailto:ops@example.com", webpush.WithStore(store))

mux.Handle("/push/vapid", webpush.VAPIDPublicHandler(client))
mux.Handle("/push/subscribe", webpush.SubscribeHandler(store, nil))
_ = client.SendToUser(ctx, "user-1", []byte(`{"title":"hi","body":"hello"}`))
```

完整示例见 `examples/pwa`。
