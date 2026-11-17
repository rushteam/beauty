// mdrt generated. spec=pricing dialect=mdrt/v0
// source=pricing.md#L7 sha256=38cf1d1d behavior=impl
const __pricing_u0_fn = function (event, input, ctx) {
  if (event.amount > 1000) {
    event.pay = Math.round(event.amount * 0.9);
  } else if (event.amount > 0) {
    event.pay = event.amount;
  } else {
    event.rejected = true;
    event.reason = "amount must be positive";
  }
};
const __pricing_u0_schema = {"amount":{"in":"body","type":"number"}};
const __pricing_u0_allow = [];
hookAdd("order.create", function (event) {
  var ctx = __mdrt.ctx("pricing", 0, "", __pricing_u0_allow);
  var input = __mdrt.pick(event, __pricing_u0_schema);
  __pricing_u0_fn(event, input, ctx);
});
hookAdd("mdrt:unit:pricing:0", function (ev) { __mdrt.invoke("hook", ev, __pricing_u0_fn, __pricing_u0_schema, "pricing", 0, __pricing_u0_allow); });
