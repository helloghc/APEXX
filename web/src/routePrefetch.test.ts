import { afterEach, describe, expect, it } from "vitest";
import { forget, navigated, noteRead, shapeOf, takeStarted, template } from "./routePrefetch";

describe("routePrefetch", () => {
  afterEach(() => forget());

  it("knows a detail route from a tab", () => {
    expect(shapeOf("#pedidos/ord_1a2b")).toEqual({ shape: "pedidos/:id", id: "ord_1a2b" });
    expect(shapeOf("#catalogo/productos/QA-00099")).toEqual({ shape: "catalogo/productos/:id", id: "QA-00099" });
    expect(shapeOf("#clientes/segmentos")).toBeNull();
    expect(shapeOf("#catalogo/productos/nuevo")).toBeNull();
    expect(shapeOf("#pedidos")).toBeNull();
  });

  it("learns only reads with the id as a whole segment", () => {
    expect(template("/v1/orders/ord_1/warehouse-requests", "ord_1")).toBe("/v1/orders/{id}/warehouse-requests");
    expect(template("/v1/products/QA-1?x=1", "QA-1")).toBe("/v1/products/{id}?x=1");
    expect(template("/v1/preorders/QA-1-1", "QA-1")).toBeNull(); // a variant of it: not the same id
    expect(template("/v1/promotions/targets?skus=QA-1", "QA-1")).toBeNull();
  });

  it("starts the learned reads on the next screen of the same kind, once", async () => {
    const asked: string[] = [];
    const start = (p: string) => {
      asked.push(p);
      return Promise.resolve(new Response("{}"));
    };
    navigated("#pedidos/ord_1", start);
    expect(asked).toEqual([]); // nothing learned yet
    noteRead("/v1/orders/ord_1");
    noteRead("/v1/orders/ord_1/warehouse-requests");
    noteRead("/v1/orders?limit=50"); // not about this order
    navigated("#pedidos/ord_2", start);
    expect(asked).toEqual(["/v1/orders/ord_2", "/v1/orders/ord_2/warehouse-requests"]);
    expect(takeStarted("/v1/orders/ord_2")).not.toBeNull();
    expect(takeStarted("/v1/orders/ord_2")).toBeNull(); // used once
    navigated("#clientes/cus_9", start); // another kind: nothing
    expect(asked.length).toBe(2);
  });
});
