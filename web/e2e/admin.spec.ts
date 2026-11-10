import { expect, test, type Page } from "@playwright/test";
import { readFileSync } from "node:fs";

/**
 * Merchant QA flow against a real server (see e2e/start-server.sh):
 * owner logs in → edits a price → invites a catalog-only member → that member
 * cannot see orders → owner cancels and refunds an order → builder publish and undo.
 */
const ORIGIN = "http://127.0.0.1:8094";
const t = JSON.parse(readFileSync("/tmp/apexx-e2e/tenant.json", "utf8")) as {
  tenant: string;
  email: string;
  password: string;
  storefrontKey: string;
};
const member = { email: `catalogo@${t.tenant}.test`, password: "member-password-12" };

const nav = (page: Page) => page.getByRole("navigation", { name: "Secciones" });

async function login(page: Page, email: string, password: string) {
  await page.goto("./");
  await page.getByLabel("Tienda").fill(t.tenant);
  await page.getByLabel("Correo").fill(email);
  await page.getByLabel("Contraseña").fill(password);
  await page.getByRole("button", { name: "Entrar" }).click();
  await expect(page.getByRole("button", { name: "Cerrar sesión" })).toBeVisible();
}

async function logout(page: Page) {
  await page.getByRole("button", { name: "Cerrar sesión" }).click();
  await expect(page.getByRole("heading", { name: "Inicia sesión en Apexx" })).toBeVisible();
}

async function liveSections(page: Page): Promise<string[]> {
  const res = await page.request.get(`${ORIGIN}/s/${t.tenant}/main/index`);
  expect(res.ok()).toBeTruthy();
  return [...(await res.text()).matchAll(/<section id="([^"]+)"/g)].map((m) => m[1] as string);
}

async function listOrder(page: Page): Promise<string[]> {
  return page.locator("[data-testid=section-list] > li").evaluateAll((els) => els.map((e) => e.getAttribute("data-section-id") ?? ""));
}

test("merchant QA: login, price, team, permissions, refund, builder", async ({ page }) => {
  // 1. Wrong password shows a clear error; then the owner logs in.
  await page.goto("./");
  await page.getByLabel("Tienda").fill(t.tenant);
  await page.getByLabel("Correo").fill(t.email);
  await page.getByLabel("Contraseña").fill("wrong-password-123");
  await page.getByRole("button", { name: "Entrar" }).click();
  await expect(page.getByText("Correo, contraseña o tienda incorrectos.")).toBeVisible();
  await login(page, t.email, t.password);
  await expect(page.getByTestId("who")).toBeVisible();

  // 2. Catalog: change the price of the first variant of P-000001.
  await nav(page).getByRole("link", { name: "Catálogo" }).click();
  await expect(page.getByRole("heading", { name: "Catálogo" })).toBeVisible();
  await page.getByRole("link", { name: "Producto 1", exact: true }).click();
  const panel = page.getByRole("region", { name: "Precio de SKU-000001-1" });
  await panel.getByLabel("Precio ($)").fill("1234.50");
  await panel.getByRole("button", { name: "Guardar precios" }).click();
  await expect(panel.getByText("Precios guardados")).toBeVisible();
  const sim = await page.request.post(`${ORIGIN}/v1/carts/simulate`, {
    headers: { Authorization: `Bearer ${t.storefrontKey}` },
    data: { channel: "mx", items: [{ sku: "SKU-000001-1", qty: 1 }] },
  });
  expect((await sim.json()).lines[0].unit.amount).toBe(123450);

  // 3. Team: invite a catalog-only member.
  await nav(page).getByRole("link", { name: "Equipo" }).click();
  await page.getByRole("button", { name: "Invitar miembro" }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByLabel("Correo").fill(member.email);
  await dialog.getByLabel("Nombre").fill("Cata Logo");
  await dialog.getByLabel("Contraseña temporal").fill(member.password);
  await dialog.getByRole("button", { name: "Editor de catálogo" }).click();
  await dialog.getByRole("button", { name: "Crear miembro" }).click();
  await expect(page.getByTestId(`member-${member.email}`)).toContainText("Editor de catálogo");

  // 4. The member sees Catálogo but not Pedidos, and the API refuses orders.
  await logout(page);
  await login(page, member.email, member.password);
  await expect(nav(page).getByRole("link", { name: "Catálogo" })).toBeVisible();
  await expect(nav(page).getByRole("link", { name: "Pedidos" })).toHaveCount(0);
  await page.goto("./#pedidos");
  await expect(page.getByRole("heading", { name: "Sin acceso" })).toBeVisible();
  const forbidden = await page.request.get(`${ORIGIN}/v1/orders`);
  expect(forbidden.status()).toBe(403);
  await logout(page);

  // 5. A shopper buys (storefront key), the owner cancels and refunds.
  const buy = await page.request.post(`${ORIGIN}/v1/checkout`, {
    headers: { Authorization: `Bearer ${t.storefrontKey}`, "Idempotency-Key": `e2e-${Date.now()}` },
    data: {
      channel: "mx",
      postalCode: "53129",
      items: [{ sku: "SKU-000003-1", qty: 2 }],
      customer: { email: "comprador@example.com", name: "Comprador QA" },
      paymentMethod: "pm_card_visa",
    },
  });
  expect(buy.status()).toBe(201);
  const order = (await buy.json()) as { id: string; number: number };
  await login(page, t.email, t.password);
  await nav(page).getByRole("link", { name: "Pedidos" }).click();
  await page.getByRole("link", { name: `#${order.number}` }).click();
  await expect(page.getByText("Comprador QA")).toBeVisible();
  await page.getByRole("button", { name: "Cancelar y reembolsar" }).click();
  const confirm = page.getByRole("alertdialog");
  await confirm.getByLabel("Motivo (opcional)").fill("Prueba QA");
  await confirm.getByRole("button", { name: "Sí, cancelar y reembolsar" }).click();
  await expect(page.getByText("Reembolsado").first()).toBeVisible();
  await expect(page.getByText("Cancelado y reembolsado", { exact: true })).toBeVisible();

  // 6. Builder: first publish (no undo offered), then change, publish and undo.
  await nav(page).getByRole("link", { name: "Diseño de la tienda" }).click();
  const rows = page.locator("[data-testid=section-list] > li");
  await expect(rows).toHaveCount(4);
  await expect(page.getByText("Guardado", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Publicar", exact: true }).click();
  await expect(page.getByText(/Publicado en/)).toBeVisible();
  await expect(page.getByRole("button", { name: "Deshacer publicación" })).toHaveCount(0);
  const first = await liveSections(page);

  const before = await listOrder(page);
  const handle = rows.nth(0).getByRole("button", { name: /^Mover / });
  await handle.focus();
  await page.keyboard.press("Space");
  await page.waitForTimeout(150);
  await page.keyboard.press("ArrowDown");
  await page.waitForTimeout(250);
  await page.keyboard.press("Space");
  const moved = [before[1], before[0], before[2], before[3]];
  await expect.poll(() => listOrder(page)).toEqual(moved);
  await expect(page.getByText("Guardado", { exact: true })).toBeVisible();

  await page.getByRole("button", { name: "Publicar", exact: true }).click();
  await expect.poll(() => liveSections(page)).toEqual(moved);
  await page.getByRole("button", { name: "Deshacer publicación" }).click();
  await expect(page.getByText(/Se restauró la publicación/)).toBeVisible();
  expect(await liveSections(page)).toEqual(first);
});
