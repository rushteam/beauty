---
dialect: mdrt/v0
name: pricing
auth: none
---

# hook order.create

## 输入
- amount: number

## 实现
```js
if (event.amount > 1000) {
  event.pay = Math.round(event.amount * 0.9);
} else if (event.amount > 0) {
  event.pay = event.amount;
} else {
  event.rejected = true;
  event.reason = "amount must be positive";
}
```

## 例子

### 九折
```event
{"amount": 1200}
```
```event-after
{"amount": 1200, "pay": 1080}
```

### 非法金额
```event
{"amount": 0}
```
```event-after
{"rejected": true}
```
