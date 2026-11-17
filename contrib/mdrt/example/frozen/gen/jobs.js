// mdrt generated. spec=jobs dialect=mdrt/v0
// source=jobs.md#L7 sha256=6785174a behavior=impl
const __jobs_u0_fn = function (ctx) {
  const n = ctx.store.get("n") || 0;
  ctx.store.set("n", 0);
  ctx.store.set("cleaned", n);
};
const __jobs_u0_schema = {};
const __jobs_u0_allow = [];
cronAdd("cleanup", "0 */5 * * * *", function () {
  var ctx = __mdrt.ctx("jobs", 0, "", __jobs_u0_allow);
  __jobs_u0_fn(ctx);
});
hookAdd("mdrt:unit:jobs:0", function (ev) { __mdrt.invoke("cron", ev, __jobs_u0_fn, __jobs_u0_schema, "jobs", 0, __jobs_u0_allow); });
// source=jobs.md#L26 sha256=2e5e4cc7 behavior=impl
const __jobs_u1_fn = function (payload, input, ctx) {
  const n = (ctx.store.get("signups") || 0) + 1;
  ctx.store.set("signups", n);
  ctx.store.set("last", input.email);
};
const __jobs_u1_schema = {"email":{"in":"body","type":"string"}};
const __jobs_u1_allow = [];
eventSubscribe("user.signup", function (topic, payload) {
  var ctx = __mdrt.ctx("jobs", 1, "", __jobs_u1_allow);
  var input = __mdrt.pick(payload || {}, __jobs_u1_schema);
  __jobs_u1_fn(payload, input, ctx);
});
hookAdd("mdrt:unit:jobs:1", function (ev) { __mdrt.invoke("event", ev, __jobs_u1_fn, __jobs_u1_schema, "jobs", 1, __jobs_u1_allow); });
