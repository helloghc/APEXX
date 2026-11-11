import json, sys, time, urllib.request, urllib.error
B, T = sys.argv[1], sys.argv[2]
def call(m, p, body=None, tok=None, hdr=None):
    h = {"Content-Type": "application/json"}
    if tok: h["Authorization"] = "Bearer " + tok
    if hdr: h.update(hdr)
    r = urllib.request.Request(B + p, method=m, data=json.dumps(body).encode() if body is not None else None, headers=h)
    try:
        with urllib.request.urlopen(r) as resp:
            raw = resp.read(); return resp.status, (json.loads(raw) if raw and raw[:1] in b"{[" else raw.decode()[:200])
    except urllib.error.HTTPError as e:
        raw = e.read()
        try: return e.code, json.loads(raw)
        except Exception: return e.code, raw.decode()[:200]
ok = fail = 0
def check(name, cond, info=""):
    global ok, fail
    if cond: ok += 1; print("  ok  ", name)
    else: fail += 1; print("  FAIL", name, "→", str(info)[:400])
s, d = call("POST", "/v1/auth/login", {"store": T, "email": "dueno@qa.apexx.test", "password": "QA-prueba-2026!"}); tok = d["token"]
def buy(key, sku="QA-00020-1", qty=3, email="cliente.uno@example.com"):
    return call("POST", f"/s/{T}/api/checkout", {"channel": "mx", "postalCode": "53129", "items": [{"sku": sku, "qty": qty}],
        "customer": {"email": email, "name": "Cliente Uno", "phone": "5512345678", "address": "Av. Uno 1"}, "paymentMethod": "pm_card_visa"}, hdr={"Idempotency-Key": key})
k = str(int(time.time()))
s, o = buy("as2-" + k); check("compra", s == 201, o); oid = o.get("id")
s, d = call("GET", "/v1/orders?q=cliente.uno@example.com", tok=tok); check("buscar pedido por correo", s == 200 and any(x["id"] == oid for x in d.get("items", [])), d)
s, d = call("GET", "/v1/customers?q=cliente.uno@example.com", tok=tok); check("cliente creado por la compra", s == 200 and len(d.get("items", [])) >= 1, d)
cid = d["items"][0]["id"] if s == 200 and d.get("items") else ""
s, d = call("GET", f"/v1/customers/{cid}", tok=tok); check("ficha de cliente con estadísticas", s == 200, d); print("     ", {k: d.get(k) for k in ("ordersCount", "totalSpent", "email", "name") if isinstance(d, dict)} if isinstance(d, dict) else d)
s, d = call("POST", f"/v1/orders/{oid}/notes", {"body": "Cliente pidió factura", "visibility": "internal"}, tok); check("nota interna", s in (200, 201), d)
s, d = call("POST", f"/v1/orders/{oid}/fulfillments", {"items": [{"line": 1, "qty": 2}], "carrier": "Estafeta", "trackingNumber": "EST123456", "trackingUrl": "https://rastreo.example/EST123456"}, tok); check("envío parcial con guía", s in (200, 201), d)
fid = d.get("id") if isinstance(d, dict) else None
s, od = call("GET", f"/v1/orders/{oid}", tok=tok); check("estado de envío parcial", s == 200 and od.get("fulfillmentStatus") not in ("unfulfilled", None), od.get("fulfillmentStatus") if isinstance(od, dict) else od)
s, q = call("POST", f"/v1/orders/{oid}/refunds/quote", {"items": [{"sku": "QA-00020-1", "qty": 1}]}, tok); check("cotizar reembolso parcial", s == 200, q); print("     ", q if s != 200 else {k: q.get(k) for k in list(q)[:8]})
s, r = call("POST", f"/v1/orders/{oid}/refunds", {"lines": [{"line": 1, "qty": 1}], "restock": True, "reason": "talla incorrecta"}, tok, {"Idempotency-Key": "rf-" + k}); check("reembolso parcial", s in (200, 201), r)
s, r2 = call("POST", f"/v1/orders/{oid}/refunds", {"lines": [{"line": 1, "qty": 1}], "restock": True, "reason": "talla incorrecta"}, tok, {"Idempotency-Key": "rf-" + k}); check("reembolso idempotente (misma llave)", s in (200, 201) and isinstance(r, dict) and r2.get("id") == r.get("id"), r2)
s, od = call("GET", f"/v1/orders/{oid}", tok=tok); check("total reembolsado en el pedido", s == 200 and od.get("refundedTotal", 0) > 0 and od.get("refundedTotal") < od.get("grandTotal"), {k: od.get(k) for k in ("refundedTotal", "grandTotal", "paymentStatus", "status")})
s, rt = call("POST", f"/v1/orders/{oid}/returns", {"items": [{"line": 1, "qty": 1, "reason": "defective"}], "note": "Llegó dañado"}, tok); check("crear devolución (RMA)", s in (200, 201), rt)
rid = rt.get("id") if isinstance(rt, dict) else ""
s, d = call("POST", f"/v1/returns/{rid}/receive", {"items": [{"line": 1, "restock": False}], "skipRefund": True}, tok); check("recibir devolución", s == 200, d)
s, d = call("POST", f"/v1/returns/{rid}/refund", {}, tok); check("reembolsar devolución", s in (200, 204), d)
s, d = call("GET", "/v1/returns", tok=tok); check("lista de devoluciones", s == 200 and len(d.get("items", [])) >= 1, d)
s, d = call("GET", "/v1/notifications/messages", tok=tok); check("bitácora de correos", s == 200 and len(d.get("items", [])) >= 1, d)
if s == 200: print("     ", sorted({m.get("template") for m in d["items"]}))
s, d = call("GET", "/v1/notifications/templates", tok=tok); check("plantillas de correo", s == 200 and len(d.get("items", [])) >= 5, d)
s, d = call("POST", "/v1/notifications/templates/order_confirmed/preview", {}, tok); check("vista previa de plantilla", s == 200, d)
s, d = call("GET", "/v1/orders/export.csv", tok=tok); check("exportar pedidos CSV", s == 200 and "," in str(d), d)
s, d = call("POST", "/v1/order-views", {"name": "Por enviar", "filters": {"fulfillment": "unfulfilled"}}, tok); check("guardar vista", s in (200, 201), d)
s, d = call("GET", "/v1/customer-groups", tok=tok); check("segmentos", s == 200, d)
s, d = call("POST", "/v1/customer-groups/preview", {"rules": {"match": "all", "conditions": [{"field": "orders_count", "op": "gte", "value": 1}]}}, tok); check("vista previa de segmento", s == 200, d)
# ledger must balance
import subprocess
out = subprocess.run(["psql", "-h", "127.0.0.1", "-U", "postgres", "apexx", "-Atc", f"select count(*) from (select tx_id from ledger_entries where tenant_id='{T}' group by tx_id having sum(amount)<>0) x"], capture_output=True, text=True).stdout.strip()
check("libro mayor cuadrado tras reembolsos", out == "0", out)
# storefront account: OTP
s, d = call("POST", f"/s/{T}/api/account/code", {"email": "cliente.uno@example.com"}); check("pedir código de acceso", s in (200, 202, 204), d)
s, d = call("POST", f"/s/{T}/api/newsletter", {"email": "boletin@example.com"}); check("newsletter guarda consentimiento", s in (200, 201, 204), d)
print(f"\n{ok} ok, {fail} fallas")
