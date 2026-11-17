---
dialect: mdrt/v0
name: quote
auth: none
---

# POST /quote

## 输入
- amount: number, > 0

## 行为
计算应付金额。amount 大于 1000 时 pay 为 amount 的九折,四舍五入到整数;否则 pay 等于 amount。
返回 201,正文包含 amount 和 pay。amount 不是大于 0 的数字时返回 400。

## 例子

### 满 1000 打九折
```req
POST /quote

{"amount": 1200}
```
```res
201
{"amount": 1200, "pay": 1080}
```

### 未满不打折
```req
POST /quote

{"amount": 800}
```
```res
201
{"amount": 800, "pay": 800}
```

### 金额非法
```req
POST /quote

{"amount": 0}
```
```res
400
```
