---
dialect: mdrt/v0
name: orders
auth: bearer
---

# POST /orders

## 输入
- amount: number, > 0

## 实现
```js
const ev = { amount: input.amount };
ctx.trigger("order.create", ev);
if (ev.rejected) {
  return { status: 400, body: { message: ev.reason } };
}
const seq = (ctx.store.get("seq") || 0) + 1;
ctx.store.set("seq", seq);
const id = "o" + seq;
ctx.store.set("order:" + id, { id: id, amount: input.amount, pay: ev.pay });
return { status: 201, body: { id: id, amount: input.amount, pay: ev.pay } };
```

## 例子

### 满 1000 打九折
```store
{"orders:seq": 7}
```
```req
POST /orders
Authorization: Bearer $token(u1)

{"amount": 1200}
```
```res
201
{"amount": 1200, "pay": 1080, "id": "o8"}
```
```store-after
{"orders:seq": 8}
```

### 金额非法
```req
POST /orders
Authorization: Bearer $token(u1)

{"amount": 0}
```
```res
400
```

### 未登录
```req
POST /orders

{"amount": 10}
```
```res
401
```

# GET /orders/{id}

## 输入
- id (path): string

## 实现
```js
const order = ctx.store.get("order:" + input.id);
if (!order) {
  return { status: 404, body: { message: "not found" } };
}
return { status: 200, body: order };
```

## 例子

### 命中
```store
{"orders:order:o1": {"id": "o1", "amount": 10, "pay": 10}}
```
```req
GET /orders/o1
Authorization: Bearer $token(u1)
```
```res
200
{"id": "o1", "pay": 10}
```

### 不存在
```req
GET /orders/missing
Authorization: Bearer $token(u1)
```
```res
404
```
