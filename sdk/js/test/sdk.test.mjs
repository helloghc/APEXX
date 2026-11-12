// Runs against a live Apexx: APEXX_SDK_URL=http://127.0.0.1:8097 APEXX_SDK_STORE=fa8042 node --test test/
import test from "node:test";
import assert from "node:assert/strict";
import { createStorefront, verifyExtensionSession } from "../dist/index.js";

const url = process.env.APEXX_SDK_URL, store = process.env.APEXX_SDK_STORE;

test("storefront catalog and cart", { skip: !url && "set APEXX_SDK_URL and APEXX_SDK_STORE" }, async () => {
  const shop = createStorefront({ baseUrl: url, store });
  const page = await shop.products({ first: 3 });
  assert.equal(page.nodes.length, 3);
  const p = await shop.product(page.nodes[0].handle);
  assert.equal(p.handle, page.nodes[0].handle);
  let n = 0;
  for await (const _ of shop.allProducts()) n++;
  assert.ok(n >= 3);
  const sku = page.nodes.flatMap((x) => x.variants).find((v) => v.available > 0).sku;
  const c = await shop.cart([{ sku, qty: 2 }], "06600");
  assert.equal(c.lines[0].qty, 2);
  assert.ok(c.total.amount >= c.items.amount - c.discounts.amount);
  await assert.rejects(shop.products({ first: 500 }), /first va de 1 a 50/);
});

test("extension sessions are verified", async () => {
  const secret = "s3cr3t";
  const body = Buffer.from(JSON.stringify({ store: "x", app: "a", member: "m", order: "", exp: Math.floor(Date.now() / 1000) + 60 })).toString("base64url");
  const { createHmac } = await import("node:crypto");
  const sig = createHmac("sha256", secret).update(body).digest("hex");
  assert.equal((await verifyExtensionSession(body + "." + sig, secret)).store, "x");
  assert.equal(await verifyExtensionSession(body + "." + sig.replace(/^./, "0"), secret), null);
  assert.equal(await verifyExtensionSession(body + "." + sig, "otro"), null);
});
