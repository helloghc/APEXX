import { describe, expect, it } from "vitest";
import { can, visibleNav } from "@/perms";
import { query } from "./api";
import { customError } from "./controls";
import { bucketLabel, compact, compactMoney, delta, duration, money, niceTicks, num, rangeLabel, ratio } from "./format";

describe("report formatting", () => {
  it("renders money from cents without floating point drift", () => {
    expect(money(129950)).toBe("$1,299.50");
    expect(money(-5)).toBe("-$0.05");
    expect(compactMoney(999_900)).toBe("$9,999");
    expect(compactMoney(1_234_500)).toBe("$12.3 k");
    expect(compactMoney(45_000_000)).toBe("$450 k");
    expect(compactMoney(123_456_789)).toBe("$1.23 M");
    expect(compactMoney(-2_500_000)).toBe("-$25 k");
    expect(num(1234567)).toBe("1,234,567");
    expect(compact(15300)).toBe("15.3 k");
  });

  it("computes ratios and never divides by zero", () => {
    expect(ratio(1, 6, 2)).toBe("16.67%");
    expect(ratio(0, 10)).toBe("0.0%");
    expect(ratio(5, 0)).toBe("—");
  });

  it("compares with the previous period", () => {
    expect(delta(150, 100)).toMatchObject({ dir: "up", label: "+50.0%" });
    expect(delta(75, 100)).toMatchObject({ dir: "down", label: "−25.0%" });
    expect(delta(100, 100)).toMatchObject({ dir: "flat", label: "Sin cambio" });
    expect(delta(0, 0)).toMatchObject({ dir: "flat" });
    // No base to compare against: say "Nuevo" instead of an infinite percentage.
    expect(delta(10, 0)).toMatchObject({ pct: null, dir: "up", label: "Nuevo" });
    expect(delta(-50, -100)).toMatchObject({ dir: "up" });
    expect(delta(5000, 100).label).toBe("+4,900%");
  });

  it("labels buckets and ranges in Spanish", () => {
    expect(bucketLabel("2026-11-10", "day")).toBe("10 nov");
    expect(bucketLabel("2026-11-10", "day", true)).toBe("10 nov 2026");
    expect(bucketLabel("2026-11-10T09", "hour")).toBe("9:00");
    expect(bucketLabel("2026-11-10T09", "hour", true)).toBe("10 nov, 9:00");
    expect(bucketLabel("2026-11-09", "week", true)).toBe("Semana del 9 nov");
    expect(bucketLabel("2026-03", "month")).toBe("mar 2026");
    expect(rangeLabel({ from: "2026-11-04", to: "2026-11-10" })).toBe("4 – 10 nov 2026");
    expect(rangeLabel({ from: "2026-10-28", to: "2026-11-03" })).toBe("28 oct – 3 nov 2026");
    expect(rangeLabel({ from: "2025-12-30", to: "2026-01-02" })).toBe("30 dic 2025 – 2 ene 2026");
    expect(rangeLabel({ from: "2026-11-10", to: "2026-11-10" })).toBe("10 nov 2026");
  });

  it("picks round axis ticks that cover the maximum", () => {
    expect(niceTicks(0)).toEqual([0, 1]);
    expect(niceTicks(87)).toEqual([0, 25, 50, 75, 100]);
    expect(niceTicks(4)).toEqual([0, 1, 2, 3, 4]);
    const t = niceTicks(1_234_567);
    expect(t[0]).toBe(0);
    expect(t[t.length - 1]).toBeGreaterThanOrEqual(1_234_567);
    expect(t.length).toBeLessThanOrEqual(6);
  });

  it("describes elapsed time", () => {
    expect(duration(2)).toBe("hace un momento");
    expect(duration(45)).toBe("hace 45 s");
    expect(duration(600)).toBe("hace 10 min");
    expect(duration(7200)).toBe("hace 2 h");
    expect(duration(3 * 86400)).toBe("hace 3 días");
  });
});

describe("report filters", () => {
  it("builds the query string and only sends dates for custom ranges", () => {
    expect(query({ range: "7d", from: "2026-01-01", to: "2026-01-31" })).toBe("range=7d");
    expect(query({ range: "custom", from: "2026-01-01", to: "2026-01-31", channel: "mx" }, { grain: "week", format: undefined })).toBe(
      "range=custom&from=2026-01-01&to=2026-01-31&channel=mx&grain=week",
    );
  });

  it("validates custom ranges before asking the server", () => {
    expect(customError({ range: "30d" })).toBeNull();
    expect(customError({ range: "custom" })).toMatch(/fecha inicial/);
    expect(customError({ range: "custom", from: "2026-02-01", to: "2026-01-01" })).toMatch(/anterior/);
    expect(customError({ range: "custom", from: "2020-01-01", to: "2026-01-01" })).toMatch(/dos años/);
    expect(customError({ range: "custom", from: "2026-01-01", to: "2026-01-31" })).toBeNull();
  });
});

describe("reports and operations permissions", () => {
  const base = { tenant: "t", kind: "member" as const, id: "m", name: "x" };
  it("shows Reportes with reports:read and Operación only to settings admins", () => {
    const reader = { ...base, owner: false, permissions: { reports: "read" as const, settings: "read" as const } };
    const routes = visibleNav(reader).map((n) => n.route);
    expect(routes).toContain("reportes");
    expect(routes).not.toContain("operacion");
    const ops = { ...base, owner: false, permissions: { settings: "admin" as const } };
    expect(visibleNav(ops).map((n) => n.route)).toEqual(["inicio", "correos", "configuracion", "conectores", "operacion"]);
    expect(can(ops, "reports", "read")).toBe(false);
    expect(visibleNav({ ...base, owner: true, permissions: {} }).map((n) => n.route)).toEqual(expect.arrayContaining(["reportes", "operacion"]));
  });
});
