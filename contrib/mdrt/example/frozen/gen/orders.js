// mdrt generated. spec=orders dialect=mdrt/v0
// source=orders.md#L7 sha256=62685d1c behavior=impl
const __orders_u0_fn = function (input, ctx) {
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
};
const __orders_u0_schema = {"amount":{"in":"body","type":"number","gt":0}};
const __orders_u0_allow = [];
routerAdd("POST", "/orders", function (e) {
  var uid = __mdrt.bearer(e);
  if (!uid) throw new UnauthorizedError();
  var ctx = __mdrt.ctx("orders", 0, uid, __orders_u0_allow);
  var input = __mdrt.input(e, __orders_u0_schema);
  __mdrt.respond(e, __orders_u0_fn(input, ctx));
});
hookAdd("mdrt:unit:orders:0", function (ev) { __mdrt.invoke("route", ev, __orders_u0_fn, __orders_u0_schema, "orders", 0, __orders_u0_allow); });
// source=orders.md#L67 sha256=72d45499 behavior=impl
const __orders_u1_fn = function (input, ctx) {
  const order = ctx.store.get("order:" + input.id);
  if (!order) {
    return { status: 404, body: { message: "not found" } };
  }
  return { status: 200, body: order };
};
const __orders_u1_schema = {"id":{"in":"path","type":"string"}};
const __orders_u1_allow = [];
routerAdd("GET", "/orders/{id}", function (e) {
  var uid = __mdrt.bearer(e);
  if (!uid) throw new UnauthorizedError();
  var ctx = __mdrt.ctx("orders", 1, uid, __orders_u1_allow);
  var input = __mdrt.input(e, __orders_u1_schema);
  __mdrt.respond(e, __orders_u1_fn(input, ctx));
});
hookAdd("mdrt:unit:orders:1", function (ev) { __mdrt.invoke("route", ev, __orders_u1_fn, __orders_u1_schema, "orders", 1, __orders_u1_allow); });
