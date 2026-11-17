---
dialect: mdrt/v0
name: jobs
auth: none
---

# cron cleanup 0 */5 * * * *

## 实现
```js
const n = ctx.store.get("n") || 0;
ctx.store.set("n", 0);
ctx.store.set("cleaned", n);
```

## 例子

### 清零
```store
{"jobs:n": 3}
```
```store-after
{"jobs:n": 0, "jobs:cleaned": 3}
```

# event user.signup

## 输入
- email: string

## 实现
```js
const n = (ctx.store.get("signups") || 0) + 1;
ctx.store.set("signups", n);
ctx.store.set("last", input.email);
```

## 例子

### 记录邮箱
```event
{"email": "a@b.c"}
```
```store-after
{"jobs:signups": 1, "jobs:last": "a@b.c"}
```

### 缺少邮箱
```event
{}
```
```res
400
```
