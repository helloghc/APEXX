import { expect, test, type Page } from "@playwright/test";

const API = "http://127.0.0.1:8098";
const KEY = process.env.APEXX_API_KEY ?? "";

async function liveOrder(page: Page): Promise<string[]> {
  const res = await page.request.get(`${API}/s/demo/index`, { headers: KEY ? { Authorization: `Bearer ${KEY}` } : {} });
  expect(res.ok()).toBeTruthy();
  return [...(await res.text()).matchAll(/<section id="([^"]+)"/g)].map((m) => m[1] as string);
}

async function listOrder(page: Page): Promise<string[]> {
  return page.locator("[data-testid=section-list] > li").evaluateAll((els) => els.map((e) => e.getAttribute("data-section-id") ?? ""));
}

test("drag a section, publish, and roll back", async ({ page }) => {
  const probe = await page.request.get(`${API}/v1/builder/sections`, { headers: KEY ? { Authorization: `Bearer ${KEY}` } : {} });
  test.skip(
    probe.status() === 401 || probe.status() === 403,
    "El backend exige clave de API: exporta APEXX_API_KEY para correr esta prueba.",
  );
  if (KEY) {
    await page.addInitScript((k) => {
      sessionStorage.setItem("apexx.apiKey", k);
      sessionStorage.setItem("apexx.actor", "e2e");
    }, KEY);
  }

  await page.goto("/?site=demo&page=index");
  const rows = page.locator("[data-testid=section-list] > li");
  await expect(rows).toHaveCount(4);
  const before = await listOrder(page);
  const published = await liveOrder(page);

  // Pointer drag: the last section onto the second slot.
  const handle = rows.nth(3).getByRole("button", { name: /^Mover / });
  const target = rows.nth(1);
  const hb = (await handle.boundingBox())!;
  const tb = (await target.boundingBox())!;
  await page.mouse.move(hb.x + hb.width / 2, hb.y + hb.height / 2);
  await page.mouse.down();
  await page.mouse.move(hb.x + hb.width / 2, hb.y - 10, { steps: 5 });
  await page.mouse.move(tb.x + 20, tb.y + 4, { steps: 15 });
  await page.mouse.up();

  const moved = [before[0], before[3], before[1], before[2]];
  await expect.poll(() => listOrder(page)).toEqual(moved);
  await expect(page.getByText("Guardado", { exact: true })).toBeVisible();

  // The preview iframe re-renders with the new order.
  const frame = page.frameLocator("[data-testid=preview]");
  await expect(frame.locator("section").nth(1)).toHaveAttribute("id", moved[1]!);

  // Keyboard reorder is available too (accessibility): move the first section down and back.
  const first = rows.nth(0).getByRole("button", { name: /^Mover / });
  await first.focus();
  // dnd-kit measures positions between key presses; a real user is never faster than this.
  await page.keyboard.press("Space");
  await page.waitForTimeout(150);
  await page.keyboard.press("ArrowDown");
  await page.waitForTimeout(250);
  await page.keyboard.press("Space");
  await expect.poll(() => listOrder(page)).toEqual([moved[1], moved[0], moved[2], moved[3]]);
  await page.keyboard.press("Control+z");
  await expect.poll(() => listOrder(page)).toEqual(moved);

  // Publish: the live page changes.
  await page.getByRole("button", { name: "Publicar" }).click();
  await expect(page.getByTestId("toast")).toContainText("Publicado en");
  expect(await liveOrder(page)).toEqual(moved);

  // Roll back from the toast: the live page returns to what was published before.
  await page.getByRole("button", { name: "Deshacer publicación" }).click();
  await expect(page.getByTestId("toast")).toContainText("Se restauró");
  expect(await liveOrder(page)).toEqual(published);
});
