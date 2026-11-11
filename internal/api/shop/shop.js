// Apexx storefront: cart in localStorage, live simulation, checkout with Stripe.
(() => {
  const body = document.body;
  // Builder pages are static per revision, so they learn the store from the URL.
  const parts = location.pathname.split("/");
  const tenant = body.dataset.tenant || parts[2], base = body.dataset.base || "/s/" + parts[2] + "/" + parts[3], channel = body.dataset.channel || "mx";
  const api = "/s/" + tenant + "/api";
  const KEY = "apexx_cart_" + tenant, ZIP = "apexx_zip_" + tenant, CPN = "apexx_coupons_" + tenant;
  const $ = (s, el = document) => el.querySelector(s);
  const store = {
    get(k, d) { try { const v = localStorage.getItem(k); return v ? JSON.parse(v) : d; } catch { return d; } },
    set(k, v) { try { localStorage.setItem(k, JSON.stringify(v)); } catch {} },
  };
  const cart = () => store.get(KEY, []);
  const saveCart = (c) => { store.set(KEY, c.filter((l) => l.qty > 0)); count(); };
  const count = () => { const n = cart().reduce((a, l) => a + l.qty, 0); document.querySelectorAll("[data-cart-count]").forEach((e) => (e.textContent = n)); };
  const money = (m) => m ? "$" + (m.amount / 100).toLocaleString("es-MX", { minimumFractionDigits: 2, maximumFractionDigits: 2 }) + " " + m.currency : "";
  const esc = (s) => String(s).replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
  async function post(path, data, headers = {}) {
    const r = await fetch(api + path, { method: "POST", headers: { "Content-Type": "application/json", ...headers }, body: JSON.stringify(data) });
    let j = {}; try { j = await r.json(); } catch {}
    return { status: r.status, body: j };
  }
  // Only coupons the store confirmed as applied are kept; an invalid code is never stored or shown as applied.
  const coupons = () => store.get(CPN, []);
  const simulate = (zip, extra = {}) => post("/simulate", { channel, postalCode: zip || undefined, items: cart().map((l) => ({ sku: l.sku, qty: l.qty })), coupons: coupons(), ...extra });
  // Drops stored codes that stopped applying and returns the reasons to show.
  const syncCoupons = (r) => {
    const res = r.coupons || [], lost = res.filter((c) => !c.applied && coupons().includes(c.code));
    if (lost.length) store.set(CPN, coupons().filter((c) => !lost.some((l) => l.code === c)));
    return lost.map((c) => c.reason);
  };
  count();

  // Quick add buttons on product cards (shown by themes that want them).
  document.addEventListener("click", (e) => {
    const b = e.target.closest("[data-quick-add]"); if (!b) return;
    const c = cart(); const sku = b.dataset.quickAdd; const l = c.find((x) => x.sku === sku);
    if (l) l.qty = Math.min(50, l.qty + 1); else c.push({ sku, qty: 1, title: b.dataset.title || sku });
    saveCart(c); b.classList.add("added"); b.textContent = "Agregado ✓";
    setTimeout(() => { b.classList.remove("added"); b.textContent = "Agregar"; }, 1600);
  });

  // Newsletter: confirmation on the page (sending to an email tool arrives with connectors).
  document.querySelectorAll("[data-newsletter]").forEach((f) => f.addEventListener("submit", (e) => {
    e.preventDefault(); const i = f.querySelector("input[type=email]");
    if (!i.value.includes("@")) { i.focus(); return; }
    f.hidden = true; const ok = f.parentElement.querySelector("[data-newsletter-ok]"); if (ok) ok.hidden = false;
  }));

  // Product page: add to cart.
  const buy = $("[data-buy]");
  if (buy) {
    const status = $("[data-buy-status]", buy), out = $("[data-price-out]", buy);
    const sel = () => $("input[name=sku]:checked", buy);
    const show = () => { const s = sel(); if (s) out.innerHTML = s.dataset.compare ? "<span class='sale'>" + esc(s.dataset.price) + "</span> <s>" + esc(s.dataset.compare) + "</s>" : esc(s.dataset.price); };
    // A variant with its own image shows it in the gallery when chosen.
    const pic = $("[data-pdp-img]");
    const showImg = () => { const s = sel(); if (!pic || !s || !s.dataset.img) return; pic.closest("picture")?.querySelectorAll("source").forEach((x) => x.remove()); pic.srcset = s.dataset.imgset; pic.src = s.dataset.img; };
    buy.addEventListener("change", () => { show(); showImg(); });
    buy.addEventListener("submit", (e) => {
      e.preventDefault();
      const s = sel(); if (!s) { status.textContent = "Elige una opción."; return; }
      const qty = Math.max(1, Math.min(50, parseInt($("#qty").value, 10) || 1));
      const c = cart(); const line = c.find((l) => l.sku === s.value);
      if (line) line.qty = Math.min(50, line.qty + qty); else c.push({ sku: s.value, qty, title: s.dataset.title });
      saveCart(c);
      status.innerHTML = "Agregado al carrito. <a href='" + base + "/cart'>Ver carrito</a>";
    });
  }

  function renderTotals(r) {
    const t = r.totals;
    const promos = (r.promotions || []).filter((p) => p.applied || !p.coupon).map((p) => "<p class='promo" + (p.applied ? " on" : "") + "'>" + (p.applied ? "✓ " : "") + esc(p.name) + ": " + esc(p.reason) +
      (p.coupon ? " <button type='button' class='linkbtn' data-coupon-del='" + esc(p.coupon) + "'>Quitar cupón " + esc(p.coupon) + "</button>" : "") + (p.hint ? " <span class='muted'>" + esc(p.hint) + "</span>" : "") + "</p>").join("");
    const msgs = (r.messages || []).map((m) => "<li>" + esc(m.text) + "</li>").join("");
    return (msgs ? "<ul class='msgs'>" + msgs + "</ul>" : "") + promos +
      "<table class='totals'><tr><td>Productos</td><td class='num'>" + money(t.items) + "</td></tr>" +
      (t.discounts.amount ? "<tr><td>Descuentos</td><td class='num'>−" + money(t.discounts) + "</td></tr>" : "") +
      "<tr><td>Envío" + (r.shipping ? " (" + esc(r.shipping.name) + ", " + r.shipping.estimateDays + " días)" : "") + "</td><td class='num'>" + (r.shipping ? (r.shipping.original ? "<s class='muted'>" + money(r.shipping.original) + "</s> " : "") + (r.shipping.price.amount ? money(r.shipping.price) : "Gratis") : "Ingresa tu código postal") + "</td></tr>" +
      "<tr class='grand'><td>Total</td><td class='num'>" + money(t.grand) + "</td></tr>" +
      "<tr><td class='muted'>IVA incluido</td><td class='num muted'>" + money(t.taxIncluded) + "</td></tr></table>";
  }

  // Cart page.
  const view = $("[data-cart-view]");
  if (view) {
    const zipForm = $("[data-zip]"), zipIn = $("#zip"), couponForm = $("[data-coupon]"), couponStatus = $("[data-coupon-status]");
    zipIn.value = store.get(ZIP, "");
    async function render() {
      const c = cart();
      if (!c.length) { view.innerHTML = "<p>Tu carrito está vacío. <a href='" + base + "/c'>Ver productos</a></p>"; $("[data-to-checkout]").hidden = true; return; }
      const r = await simulate(store.get(ZIP, ""));
      if (r.status !== 200) { view.innerHTML = "<p class='error'>No pudimos calcular tu carrito: " + esc(r.body.detail || "intenta de nuevo") + "</p>"; return; }
      const lostCoupons = syncCoupons(r.body);
      if (lostCoupons.length && couponStatus) couponStatus.textContent = lostCoupons.join(" ");
      const rows = r.body.lines.map((l) => l.gift ? "<tr><td>" + esc(l.title) + "</td><td>" + l.qty + "</td><td class='num'>Gratis</td><td></td></tr>" : "<tr><td>" + esc(l.title) + (l.compareAt ? " <s class='muted'>" + money(l.compareAt) + "</s>" : "") + "</td>" +
        "<td><span class='qty'><button type='button' data-dec='" + esc(l.sku) + "' aria-label='Quitar uno de " + esc(l.title) + "'>−</button><span>" + l.qty + "</span>" +
        "<button type='button' data-inc='" + esc(l.sku) + "' aria-label='Agregar uno de " + esc(l.title) + "'>+</button></span></td>" +
        "<td class='num'>" + money(l.total) + "</td><td><button type='button' class='linkbtn' data-del='" + esc(l.sku) + "'>Quitar</button></td></tr>").join("");
      view.innerHTML = "<table><thead><tr><th>Producto</th><th>Cantidad</th><th class='num'>Total</th><th><span class='sr'>Acciones</span></th></tr></thead><tbody>" + rows + "</tbody></table>" + renderTotals(r.body);
      // Keep local quantities in sync with what the store can actually sell.
      const fixed = c.map((x) => { const l = r.body.lines.find((y) => y.sku === x.sku && !y.gift); return l ? { ...x, qty: l.qty } : x; });
      store.set(KEY, fixed.filter((l) => l.qty > 0)); count();
    }
    view.addEventListener("click", (e) => {
      const b = e.target.closest("button"); if (!b) return;
      if (b.dataset.couponDel) { store.set(CPN, coupons().filter((x) => x !== b.dataset.couponDel)); if (couponStatus) couponStatus.textContent = "Quitamos el cupón " + b.dataset.couponDel + "."; render(); return; }
      const c = cart(); const sku = b.dataset.inc || b.dataset.dec || b.dataset.del; const l = c.find((x) => x.sku === sku); if (!l) return;
      if (b.dataset.inc) l.qty = Math.min(50, l.qty + 1); else if (b.dataset.dec) l.qty -= 1; else l.qty = 0;
      saveCart(c); render();
    });
    zipForm.addEventListener("submit", (e) => { e.preventDefault(); store.set(ZIP, zipIn.value.trim()); render(); });
    if (couponForm) couponForm.addEventListener("submit", async (e) => {
      e.preventDefault();
      const code = $("#coupon").value.trim().toUpperCase(); if (!code) { couponStatus.textContent = "Escribe el código de tu cupón."; return; }
      if (!cart().length) { couponStatus.textContent = "Agrega productos antes de usar un cupón."; return; }
      // Ask the store first; the code is kept only if it really applies.
      const r = await simulate(store.get(ZIP, ""), { coupons: [...coupons().filter((x) => x !== code), code] });
      const res = r.status === 200 ? (r.body.coupons || []).find((x) => x.code === code) : null;
      if (res && res.applied) { store.set(CPN, [...coupons().filter((x) => x !== code), code]); $("#coupon").value = ""; couponStatus.textContent = res.reason + "."; render(); }
      else couponStatus.textContent = res ? res.reason : (r.body.detail || "No pudimos validar el cupón. Intenta de nuevo.");
    });
    render();
  }

  // Checkout page.
  const form = $("[data-checkout]");
  if (form) {
    const summary = $("[data-summary]"), errBox = $("[data-error]"), payBox = $("[data-payment]"), payBtn = $("[data-pay]");
    $("#postal").value = store.get(ZIP, "");
    let cfg = { mode: "sandbox" }, stripe = null, elements = null, last = null;
    async function refresh() {
      if (!cart().length) { location.href = base + "/cart"; return; }
      const r = await simulate($("#postal").value.trim(), { email: $("#email").value.trim() || undefined, paymentMethod: "card" });
      if (r.status === 200) { last = r.body; const lost = syncCoupons(r.body); if (lost.length) errBox.textContent = lost.join(" "); summary.innerHTML = r.body.lines.map((l) => "<p>" + l.qty + " × " + esc(l.title) + "</p>").join("") + renderTotals(r.body); payBtn.textContent = "Pagar " + money(r.body.totals.grand); if (elements) elements.update({ amount: r.body.totals.grand.amount }); }
    }
    $("#postal").addEventListener("change", () => { store.set(ZIP, $("#postal").value.trim()); refresh(); });
    $("#email").addEventListener("change", refresh); // promotions limited per customer are confirmed with the email
    (async () => {
      try { cfg = await (await fetch(api + "/config")).json(); } catch {}
      await refresh();
      if (cfg.mode === "stripe" && cfg.publishableKey) {
        await new Promise((ok, bad) => { const s = document.createElement("script"); s.src = "https://js.stripe.com/v3/"; s.onload = ok; s.onerror = bad; document.head.appendChild(s); });
        stripe = window.Stripe(cfg.publishableKey, cfg.stripeAccount ? { stripeAccount: cfg.stripeAccount } : {});
        elements = stripe.elements({ mode: "payment", amount: last ? last.totals.grand.amount : 100, currency: (cfg.currency || "mxn").toLowerCase(), paymentMethodCreation: "manual" });
        payBox.innerHTML = "<div id='pe'></div>"; elements.create("payment").mount("#pe");
      } else {
        payBox.innerHTML = "<p class='testnote'>Modo de prueba: no se hace ningún cargo real.</p><div class='testcards'>" +
          "<label><input type='radio' name='pm' value='pm_card_visa' checked> Tarjeta de prueba que se aprueba</label>" +
          "<label><input type='radio' name='pm' value='pm_card_chargeDeclined'> Tarjeta de prueba que se rechaza</label></div>";
      }
    })();
    form.addEventListener("submit", async (e) => {
      e.preventDefault(); errBox.textContent = "";
      const f = new FormData(form);
      for (const [k, label] of [["email", "tu correo"], ["name", "tu nombre"], ["address", "tu dirección"], ["postal", "tu código postal"]]) {
        if (!String(f.get(k) || "").trim()) { errBox.textContent = "Falta " + label + "."; form.elements[k].focus(); return; }
      }
      if (!/^[0-9]{5}$/.test(String(f.get("postal")))) { errBox.textContent = "El código postal debe tener 5 dígitos."; $("#postal").focus(); return; }
      payBtn.disabled = true; payBtn.textContent = "Procesando…";
      let pm = String(f.get("pm") || "");
      if (stripe) {
        const sub = await elements.submit();
        if (sub.error) { errBox.textContent = sub.error.message; payBtn.disabled = false; refresh(); return; }
        const r = await stripe.createPaymentMethod({ elements, params: { billing_details: { email: f.get("email"), name: f.get("name") } } });
        if (r.error) { errBox.textContent = r.error.message; payBtn.disabled = false; refresh(); return; }
        pm = r.paymentMethod.id;
      }
      // Same cart + same attempt → same key, so a double click never charges twice.
      let key = sessionStorage.getItem("apexx_idem");
      if (!key) { key = (crypto.randomUUID ? crypto.randomUUID() : String(Date.now()) + Math.random()).replace(/[^a-z0-9-]/gi, ""); sessionStorage.setItem("apexx_idem", key); }
      const res = await post("/checkout", {
        channel, postalCode: String(f.get("postal")), items: cart().map((l) => ({ sku: l.sku, qty: l.qty })),
        customer: { email: f.get("email"), name: f.get("name"), phone: f.get("phone") || "", address: f.get("address") }, paymentMethod: pm,
        coupons: coupons(), expectedTotal: last ? last.totals.grand.amount : undefined,
      }, { "Idempotency-Key": key });
      if (res.status === 201) {
        sessionStorage.removeItem("apexx_idem"); sessionStorage.setItem("apexx_last_order", JSON.stringify(res.body));
        store.set(KEY, []); store.set(CPN, []); location.href = base + "/gracias"; return;
      }
      sessionStorage.removeItem("apexx_idem");
      const msg = { payment_declined: "Tu pago fue rechazado. Prueba con otra tarjeta.", cart_changed: "Algunos productos cambiaron de precio o disponibilidad. Revisa el resumen.",
        insufficient_stock: "Ya no hay suficientes unidades de un producto. Ajusta tu carrito.", payment_pending: "No pudimos confirmar el pago todavía. No vuelvas a pagar: te avisaremos por correo.",
        postal_code_required: "Revisa tu código postal.", payment_account_missing: "La tienda todavía no puede recibir pagos." }[res.body.code];
      payBtn.disabled = false; await refresh();
      errBox.textContent = msg || res.body.detail || "No pudimos completar la compra. Intenta de nuevo.";
    });
  }

  // Thank-you page.
  const thanks = $("[data-thanks]");
  if (thanks) {
    let o = null; try { o = JSON.parse(sessionStorage.getItem("apexx_last_order") || "null"); } catch {}
    if (!o) { thanks.innerHTML = "<h1>¡Gracias!</h1><p>No encontramos un pedido reciente en este navegador.</p>"; return; }
    const m = (v) => money({ amount: v, currency: o.currency });
    thanks.innerHTML = "<h1>¡Gracias por tu compra!</h1><p>Tu pedido <strong>#" + o.number + "</strong> está confirmado y pagado.</p>" +
      "<table><tbody>" + o.lines.map((l) => "<tr><td>" + l.qty + " × " + esc(l.title) + "</td><td class='num'>" + m(l.total) + "</td></tr>").join("") +
      "<tr><td>Envío</td><td class='num'>" + m(o.shipping) + "</td></tr>" + (o.discounts ? "<tr><td>Descuentos</td><td class='num'>−" + m(o.discounts) + "</td></tr>" : "") +
      "<tr class='grand'><td><strong>Total pagado</strong></td><td class='num'><strong>" + m(o.grandTotal) + "</strong></td></tr></tbody></table>" +
      "<p class='muted'>Te enviaremos la factura por correo en unos minutos.</p>";
  }
})();
