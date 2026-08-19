// Package messaging 提供消息投递与事件分发的抽象,解决"把消息从 A 送到 B"的问题。
//
// 选包标准:
//   - 核心语义是发布/订阅、扇出、事件通知
//   - 传输无关——具体 broker(Kafka/NATS/RabbitMQ/Redis Streams)在 contrib/ 实现
//   - 可依赖 foundation/,不可依赖 transport/api/ 等上层
//
// 已有子包:
//
//	mq        — 传输无关的 Publisher/Subscriber 接口(contrib/ 实现具体 broker)
//	eventbus  — 进程内事件总线(同步/异步)
//	stream    — 扇出流原语(被 gameloop、ws、sse、versus 等广泛使用)
//	webhook   — 出站 Webhook 投递
package messaging
