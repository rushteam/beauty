routerUse((e) => {
  e.setHeader("X-Powered-By", "beauty-jsvm");
  return e.next();
});

routerAdd("PUT", "/notes/{key...}", (e) => {
  const key = "notes/" + e.request.pathValue("key");
  $filesystem.put(key, e.request.text());
  e.json(201, { key: key, sha256: $security.sha256(e.request.text()) });
});

routerAdd("GET", "/notes/{key...}", (e) => {
  const key = "notes/" + e.request.pathValue("key");
  const st = $filesystem.stat(key);
  if (!st) throw new NotFoundError("note not found");
  e.blob(200, st.contentType, $filesystem.get(key));
});

const pricing = require("./pricing");

hookAdd("order.create", (order) => {
  if (!(order.amount > 0)) throw new BadRequestError("amount must be positive");
  order.discount = pricing.discount(order.amount);
  order.total = order.amount * order.discount;
  order.id = $security.randomString(12);
});

cronAdd("heartbeat", "@every 30s", () => {
  $app.logger().info("jsvm heartbeat", "notes", $filesystem.list("notes/").length);
});

onStart(() => console.log("jsvm example ready on :8080"));
