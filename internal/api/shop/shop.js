// Apexx storefront: cart in localStorage, live simulation, checkout with Stripe.
(() => {
  const body = document.body;
  // Builder pages are static per revision, so they learn the store from the URL.
  const parts = location.pathname.split("/");
  const tenant = body.dataset.tenant || parts[2], base = body.dataset.base || "/s/" + parts[2] + "/" + parts[3], channel = body.dataset.channel || "mx";
  const api = "/s/" + tenant + "/api";
  const KEY = "apexx_cart_" + tenant, ZIP = "apexx_zip_" + tenant, CPN = "apexx_coupons_" + tenant, SHIP = "apexx_ship_" + tenant;
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
  const simulate = (zip, extra = {}) => post("/simulate", { channel, postalCode: zip || undefined, shippingOption: store.get(SHIP, "") || undefined, items: cart().map((l) => ({ sku: l.sku, qty: l.qty })), coupons: coupons(), ...extra });
  // Drops stored codes that stopped applying and returns the reasons to show.
  const syncCoupons = (r) => {
    const res = r.coupons || [], lost = res.filter((c) => !c.applied && coupons().includes(c.code));
    if (lost.length) store.set(CPN, coupons().filter((c) => !lost.some((l) => l.code === c)));
    return lost.map((c) => c.reason);
  };
  // Delivery options: the shopper chooses; options that do not apply say why.
  function renderOptions(r) {
    const all = r.shippingOptions || [];
    if (all.length < 2 && !all.some((o) => !o.available || o.kind === "pickup")) return "";
    const sel = r.shipping ? r.shipping.id : "";
    const row = (o) => o.available
      ? "<label class='shipopt'><input type='radio' name='shipopt' value='" + esc(o.id) + "'" + (o.id === sel ? " checked" : "") + "> <span><strong>" + esc(o.name) + "</strong>" + (o.carrier ? " · " + esc(o.carrier) : "") +
        " — " + (o.price ? money({ amount: o.price, currency: r.totals.grand.currency }) : "Gratis") + "<br><span class='muted'>" + esc(o.estimate ? o.estimate.text : "") +
        (o.kind === "pickup" && o.pickupAddress ? " · " + esc(o.pickupAddress) : "") + (o.freeMissing ? " · Te faltan " + money({ amount: o.freeMissing, currency: r.totals.grand.currency }) + " para envío gratis" : "") + "</span></span></label>"
      : "<p class='muted'>" + esc(o.name) + ": " + esc(o.reason) + "</p>";
    const on = all.filter((o) => o.available), off = all.filter((o) => !o.available);
    return "<fieldset class='shipopts' data-shipopts><legend>Entrega</legend>" + on.map(row).join("") +
      (off.length ? "<details><summary class='muted'>Opciones no disponibles (" + off.length + ")</summary>" + off.map(row).join("") + "</details>" : "") + "</fieldset>";
  }
  const onShipChange = (root, again) => root.addEventListener("change", (e) => { if (e.target.name === "shipopt") { store.set(SHIP, e.target.value); again(); } });
  count();

  // Quick add buttons on product cards (shown by themes that want them).
  document.addEventListener("click", (e) => {
    const b = e.target.closest("[data-quick-add]"); if (!b) return;
    const c = cart(); const sku = b.dataset.quickAdd; const l = c.find((x) => x.sku === sku);
    if (l) l.qty = Math.min(50, l.qty + 1); else c.push({ sku, qty: 1, title: b.dataset.title || sku });
    saveCart(c); b.classList.add("added"); b.textContent = "Agregado ✓";
    setTimeout(() => { b.classList.remove("added"); b.textContent = "Agregar"; }, 1600);
  });

  // Newsletter: saves the email as a customer with marketing consent.
  document.querySelectorAll("[data-newsletter]").forEach((f) => f.addEventListener("submit", async (e) => {
    e.preventDefault(); const i = f.querySelector("input[type=email]"), btn = f.querySelector("button");
    if (!i.value.includes("@")) { i.focus(); return; }
    btn.disabled = true;
    const r = await post("/newsletter", { email: i.value.trim() }).catch(() => ({ status: 0 }));
    btn.disabled = false;
    const ok = f.parentElement.querySelector("[data-newsletter-ok]");
    if (r.status === 204) { f.hidden = true; if (ok) { ok.textContent = "¡Listo! Te escribiremos pronto."; ok.hidden = false; } return; }
    if (ok) { ok.textContent = r.status === 400 ? "Revisa tu correo e intenta de nuevo." : "No pudimos guardar tu correo. Intenta más tarde."; ok.hidden = false; }
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
      "<tr><td>" + (r.shipping && r.shipping.kind === "pickup" ? "Recolección" : "Envío") + (r.shipping ? " (" + esc(r.shipping.name) + (r.shipping.estimate ? ". " + esc(r.shipping.estimate.text) : ", " + r.shipping.estimateDays + " días") + ")" : "") + "</td><td class='num'>" + (r.shipping ? (r.shipping.original ? "<s class='muted'>" + money(r.shipping.original) + "</s> " : "") + (r.shipping.price.amount ? money(r.shipping.price) : "Gratis") : "Ingresa tu código postal") + "</td></tr>" +
      "<tr class='grand'><td>Total</td><td class='num'>" + money(t.grand) + "</td></tr>" +
      ((r.tax && r.tax.lines && r.tax.lines.length ? r.tax.lines : [{ label: "IVA", tax: t.taxIncluded.amount }]).map((x) =>
        "<tr><td class='muted'>" + esc(x.label) + (x.rateBps === 0 ? "" : " incluido") + "</td><td class='num muted'>" + money({ amount: x.tax, currency: t.grand.currency }) + "</td></tr>").join("")) + "</table>";
  }

  // Cart page.
  const view = $("[data-cart-view]");
  // Reminder email link: ?recuperar=<token> brings the saved cart back.
  const recover = new URLSearchParams(location.search).get("recuperar");
  const restored = view && recover ? fetch(api + "/cart/restore?token=" + encodeURIComponent(recover)).then((r) => r.ok ? r.json() : null).then((j) => {
    if (j && j.items && j.items.length) saveCart(j.items.map((l) => ({ sku: l.sku, qty: l.qty, title: l.title })));
    history.replaceState(null, "", location.pathname);
  }).catch(() => {}) : Promise.resolve();
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
      view.innerHTML = "<table><thead><tr><th>Producto</th><th>Cantidad</th><th class='num'>Total</th><th><span class='sr'>Acciones</span></th></tr></thead><tbody>" + rows + "</tbody></table>" + renderOptions(r.body) + renderTotals(r.body);
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
    onShipChange(view, render);
    restored.then(render);
  }

  // Checkout page.
  const form = $("[data-checkout]");
  if (form) {
    const summary = $("[data-summary]"), errBox = $("[data-error]"), payBox = $("[data-payment]"), payBtn = $("[data-pay]"), shipBox = $("[data-ship-options]"), extras = $("[data-checkout-extras]");
    const pickup = () => !!(last && last.shipping && last.shipping.kind === "pickup");
    $("#postal").value = store.get(ZIP, "");
    let cfg = { mode: "sandbox" }, stripe = null, elements = null, last = null;
    async function refresh() {
      if (!cart().length) { location.href = base + "/cart"; return; }
      const r = await simulate($("#postal").value.trim(), { email: $("#email").value.trim() || undefined, paymentMethod: "card" });
      if (r.status === 200 && shipBox) shipBox.innerHTML = renderOptions(r.body);
      if (r.status === 200) { last = r.body; const lost = syncCoupons(r.body); if (lost.length) errBox.textContent = lost.join(" "); summary.innerHTML = r.body.lines.map((l) => "<p>" + l.qty + " × " + esc(l.title) + "</p>").join("") + renderTotals(r.body); payBtn.textContent = "Pagar " + money(r.body.totals.grand); if (elements) elements.update({ amount: r.body.totals.grand.amount }); }
    }
    $("#postal").addEventListener("change", () => { store.set(ZIP, $("#postal").value.trim()); refresh(); });
    $("#email").addEventListener("change", refresh); // promotions limited per customer are confirmed with the email
    // Remember the cart of a shopper who typed an email (abandoned cart recovery).
    const track = () => { const em = $("#email").value.trim(); if (/^\S+@\S+\.\S+$/.test(em) && cart().length) post("/cart/track", { email: em, name: $("#name").value.trim(), channel, site: base.split("/").pop(), items: cart().map((l) => ({ sku: l.sku, qty: l.qty })) }).catch(() => {}); };
    $("#email").addEventListener("change", track); $("#name").addEventListener("change", track);
    if (shipBox) onShipChange(shipBox, refresh);
    (async () => {
      try { cfg = await (await fetch(api + "/config")).json(); } catch {}
      // Checkout rules of the store: required phone, note for the store, terms.
      const co = cfg.checkout || {};
      if (co.phoneRequired) { const l = $("label[for=phone]"); if (l) l.textContent = "Teléfono"; $("#phone").required = true; }
      if (extras) extras.innerHTML = (co.notesField ? "<div class='field'><label for='notes'>Nota para la tienda (opcional)</label><textarea id='notes' name='notes' maxlength='500' rows='2'></textarea></div>" : "") +
        (co.termsCheckbox ? "<label class='shipopt'><input type='checkbox' name='terms' id='terms' required> <span>Acepto los " + (co.termsPolicy ? "<a href='" + base + "/politicas/" + esc(co.termsPolicy) + "' target='_blank' rel='noopener'>" + esc((co.termsTitle || "términos y condiciones").toLowerCase()) + "</a>" : "términos y condiciones") + "</span></label>" : "");
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
      const co = cfg.checkout || {};
      for (const [k, label] of [["email", "tu correo"], ["name", "tu nombre"]].concat(co.phoneRequired ? [["phone", "tu teléfono"]] : [], pickup() ? [] : [["address", "tu dirección"], ["postal", "tu código postal"]])) {
        if (!String(f.get(k) || "").trim()) { errBox.textContent = "Falta " + label + "."; form.elements[k].focus(); return; }
      }
      if (co.termsCheckbox && !f.get("terms")) { errBox.textContent = "Acepta los términos y condiciones para continuar."; $("#terms").focus(); return; }
      if (!(pickup() && !String(f.get("postal"))) && !/^[0-9]{5}$/.test(String(f.get("postal")))) { errBox.textContent = "El código postal debe tener 5 dígitos."; $("#postal").focus(); return; }
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
        shippingOption: last && last.shipping ? last.shipping.id : undefined, acceptedTerms: !!f.get("terms") || undefined,
        customer: { email: f.get("email"), name: f.get("name"), phone: f.get("phone") || "", address: f.get("address"), notes: f.get("notes") || undefined }, paymentMethod: pm,
        coupons: coupons(), expectedTotal: last ? last.totals.grand.amount : undefined,
      }, { "Idempotency-Key": key });
      if (res.status === 201) {
        sessionStorage.removeItem("apexx_idem"); sessionStorage.setItem("apexx_last_order", JSON.stringify(res.body));
        store.set(KEY, []); store.set(CPN, []); location.href = base + "/gracias"; return;
      }
      sessionStorage.removeItem("apexx_idem");
      const msg = { payment_declined: "Tu pago fue rechazado. Prueba con otra tarjeta.", cart_changed: "Algunos productos cambiaron de precio o disponibilidad. Revisa el resumen.",
        insufficient_stock: "Ya no hay suficientes unidades de un producto. Ajusta tu carrito.", payment_pending: "No pudimos confirmar el pago todavía. No vuelvas a pagar: te avisaremos por correo.",
        postal_code_required: "Revisa tu código postal.", no_shipping_option: "No hay entregas disponibles para tu código postal.", shipping_option_unavailable: "La opción de entrega cambió. Revisa las opciones y vuelve a intentar.",
        account_required: "Esta tienda solo vende a clientes con cuenta.", payment_account_missing: "La tienda todavía no puede recibir pagos." }[res.body.code];
      payBtn.disabled = false; await refresh();
      errBox.textContent = msg || res.body.detail || "No pudimos completar la compra. Intenta de nuevo.";
    });
  }

  // Payment link page (draft orders and order balances).
  const payForm = $("[data-pay-link]");
  if (payForm) {
    const errBox = $("[data-error]", payForm), payBox = $("[data-payment]", payForm), payBtn = $("[data-pay]", payForm), label = payBtn.textContent;
    let stripe = null, elements = null;
    (async () => {
      let cfg = { mode: "sandbox" }; try { cfg = await (await fetch(api + "/config")).json(); } catch {}
      if (cfg.mode === "stripe" && cfg.publishableKey) {
        await new Promise((ok, bad) => { const s = document.createElement("script"); s.src = "https://js.stripe.com/v3/"; s.onload = ok; s.onerror = bad; document.head.appendChild(s); });
        stripe = window.Stripe(cfg.publishableKey, cfg.stripeAccount ? { stripeAccount: cfg.stripeAccount } : {});
        elements = stripe.elements({ mode: "setup", currency: (cfg.currency || "mxn").toLowerCase(), paymentMethodCreation: "manual" });
        payBox.innerHTML = "<div id='pe'></div>"; elements.create("payment").mount("#pe");
      } else if (cfg.mode === "unavailable") {
        payBox.innerHTML = "<p class='error'>La tienda todavía no puede recibir pagos.</p>"; payBtn.disabled = true;
      } else {
        payBox.innerHTML = "<p class='testnote'>Modo de prueba: no se hace ningún cargo real.</p><div class='testcards'>" +
          "<label><input type='radio' name='pm' value='pm_card_visa' checked> Tarjeta de prueba que se aprueba</label>" +
          "<label><input type='radio' name='pm' value='pm_card_chargeDeclined'> Tarjeta de prueba que se rechaza</label></div>";
      }
    })();
    payForm.addEventListener("submit", async (e) => {
      e.preventDefault(); errBox.textContent = ""; payBtn.disabled = true; payBtn.textContent = "Procesando…";
      let pm = String(new FormData(payForm).get("pm") || "");
      if (stripe) {
        const sub = await elements.submit();
        const r = sub.error ? sub : await stripe.createPaymentMethod({ elements });
        if (r.error) { errBox.textContent = r.error.message; payBtn.disabled = false; payBtn.textContent = label; return; }
        pm = r.paymentMethod.id;
      }
      const res = await post("/pay", { token: payForm.dataset.payLink, paymentMethod: pm }).catch(() => ({ status: 0, body: {} }));
      if (res.status === 200 && res.body.paid) { location.reload(); return; }
      errBox.textContent = res.body.code === "payment_declined" ? "Tu pago fue rechazado. Prueba con otra tarjeta." : (res.body.detail || "No pudimos procesar el pago. Intenta de nuevo.");
      payBtn.disabled = false; payBtn.textContent = label;
    });
  }

  // Thank-you page.
  const thanks = $("[data-thanks]");
  if (thanks) {
    let o = null; try { o = JSON.parse(sessionStorage.getItem("apexx_last_order") || "null"); } catch {}
    if (!o) { thanks.innerHTML = "<h1>¡Gracias!</h1><p>No encontramos un pedido reciente en este navegador.</p>"; return; }
    const m = (v) => money({ amount: v, currency: o.currency });
    thanks.innerHTML = "<h1>¡Gracias por tu compra!</h1><p>Tu pedido <strong>#" + esc(o.displayNumber || o.number) + "</strong> está confirmado y pagado.</p>" +
      (o.shippingMethod ? "<p>" + esc(o.shippingMethod.name) + (o.shippingMethod.estimate ? ": " + esc(o.shippingMethod.estimate.text.toLowerCase()) : "") + (o.shippingMethod.pickupAddress ? " · " + esc(o.shippingMethod.pickupAddress) : "") + ".</p>" : "") +
      "<table><tbody>" + o.lines.map((l) => "<tr><td>" + l.qty + " × " + esc(l.title) + "</td><td class='num'>" + m(l.total) + "</td></tr>").join("") +
      "<tr><td>Envío</td><td class='num'>" + m(o.shipping) + "</td></tr>" + (o.discounts ? "<tr><td>Descuentos</td><td class='num'>−" + m(o.discounts) + "</td></tr>" : "") +
      "<tr class='grand'><td><strong>Total pagado</strong></td><td class='num'><strong>" + m(o.grandTotal) + "</strong></td></tr></tbody></table>" +
      "<p class='muted'>Te enviaremos la factura por correo en unos minutos.</p>";
  }
})();

/* ==== analytics beacon: begin (no cookies; honours DNT and GPC) ==== */
(() => {
  const n = navigator, d = document, b = d.body, t = b.dataset.tenant;
  if (!t || n.doNotTrack == "1" || window.doNotTrack == "1" || n.globalPrivacyControl || n.webdriver) return;
  let q = [], m, r = "";
  try { r = new URL(d.referrer).hostname; if (r == location.hostname) r = ""; } catch {}
  const flush = () => {
    if (!q.length) return;
    const body = JSON.stringify({ c: b.dataset.channel || "", r, e: q.splice(0, 20) });
    try { n.sendBeacon("/s/" + t + "/api/events", body); } catch {}
  };
  const push = (e) => { q.push(e); clearTimeout(m); if (q.length > 9) flush(); else m = setTimeout(flush, 1500); };
  push({ t: "pv", p: location.pathname });
  const buy = d.querySelector("[data-buy]");
  if (buy) buy.addEventListener("submit", () => {
    const s = buy.querySelector("input[name=sku]:checked"), i = d.getElementById("qty");
    if (s) push({ t: "ac", s: s.value, q: Math.max(1, Math.min(50, parseInt(i && i.value, 10) || 1)) });
  });
  if (d.querySelector("[data-thanks]")) try {
    const o = JSON.parse(sessionStorage.getItem("apexx_last_order")), k = "apexx_tracked";
    if (o && sessionStorage.getItem(k) != o.id) { sessionStorage.setItem(k, o.id); push({ t: "pu", o: o.id, v: o.grandTotal }); }
  } catch {}
  d.addEventListener("visibilitychange", () => d.visibilityState == "hidden" && flush());
  addEventListener("pagehide", flush);
})();
/* ==== analytics beacon: end ==== */

/* ==== search suggestions, favorites and reviews ==== */
(() => {
  const d = document, body = d.body, parts = location.pathname.split("/");
  const tenant = body.dataset.tenant || parts[2], base = body.dataset.base || "/s/" + parts[2] + "/" + parts[3];
  if (!tenant) return;
  const api = "/s/" + tenant + "/api", site = base.split("/").pop(), WISH = "apexx_wish_" + tenant;
  const esc = (s) => String(s).replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
  const get = () => { try { return JSON.parse(localStorage.getItem(WISH) || "[]"); } catch { return []; } };
  const set = (v) => { try { localStorage.setItem(WISH, JSON.stringify(v.slice(0, 100))); } catch {} };
  const heart = "<svg viewBox='0 0 24 24' width='18' height='18' aria-hidden='true'><path d='M12 20s-7-4.4-7-9.6A4 4 0 0 1 12 8a4 4 0 0 1 7 2.4C19 15.6 12 20 12 20z' fill='none' stroke='currentColor' stroke-width='1.8' stroke-linejoin='round'/></svg>";

  // --- favorites: kept in this browser, and in the account when signed in ---
  const paint = () => { const w = get(); d.querySelectorAll("[data-wish]").forEach((b) => { const on = w.includes(b.dataset.wish); b.setAttribute("aria-pressed", on); b.classList.toggle("is-on", on); const t = b.querySelector("[data-wish-text]"); if (t) t.textContent = on ? "Guardado en favoritos" : "Guardar en favoritos"; }); };
  const cardHTML = (c) => "<li class='pcard'><a class='pcard-link' href='" + esc(c.url) + "'>" + (c.img || "<div class='pcard-media ph ph-" + c.tone + "' aria-hidden='true'><span>" + esc(c.initial) + "</span></div>") +
    "<div class='pcard-body'>" + (c.brand ? "<p class='pcard-brand'>" + esc(c.brand) + "</p>" : "") + "<h3 class='pcard-title'>" + esc(c.title) + "</h3><p class='pcard-price'>" +
    (c.compareAt ? "<span class='sale'>" + esc(c.price) + "</span> <s>" + esc(c.compareAt) + "</s>" : esc(c.price)) + "</p></div></a>" + (c.soldOut ? "<p class='pcard-badge'>Agotado</p>" : "") + "</li>";
  function decorate(root) {
    root.querySelectorAll(".pcard").forEach((li) => {
      if (li.querySelector("[data-wish]")) return;
      const a = li.querySelector("a[href*='/p/']"); if (!a) return;
      const handle = a.getAttribute("href").split("/p/")[1].split(/[?#]/)[0], title = (li.querySelector(".pcard-title") || a).textContent.trim();
      const b = d.createElement("button"); b.type = "button"; b.className = "wish wish-card"; b.dataset.wish = handle; b.setAttribute("aria-label", "Guardar " + title + " en favoritos"); b.innerHTML = heart; li.appendChild(b);
    });
    paint();
  }
  d.addEventListener("click", (e) => {
    const b = e.target.closest("[data-wish]"); if (!b) return;
    e.preventDefault();
    const h = b.dataset.wish, w = get(), on = !w.includes(h);
    set(on ? [h, ...w] : w.filter((x) => x !== h)); paint();
    fetch(api + "/wishlist/" + encodeURIComponent(h), { method: on ? "PUT" : "DELETE" }).catch(() => {}); // only sticks when signed in
    const page = d.querySelector("[data-favorites]"); if (page && !on) renderFavorites();
  });
  const tools = d.querySelector(".hdr-tools");
  if (tools && !tools.querySelector(".hdr-wish")) { const a = d.createElement("a"); a.className = "hdr-wish"; a.href = base + "/favoritos"; a.setAttribute("aria-label", "Favoritos"); a.innerHTML = heart; tools.insertBefore(a, tools.querySelector(".cartlink")); }
  async function renderFavorites() {
    const page = d.querySelector("[data-favorites]"); if (!page) return;
    const w = get();
    if (!w.length) { page.innerHTML = "<p class='muted'>Todavía no guardas productos. Toca el corazón de un producto para tenerlo aquí.</p><p><a class='btn' href='" + base + "/c'>Ver productos</a></p>"; return; }
    const r = await fetch(api + "/cards", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ handles: w, site }) }).then((x) => x.json()).catch(() => ({ items: [] }));
    page.innerHTML = "<ul class='pgrid cols-4'>" + (r.items || []).map(cardHTML).join("") + "</ul>"; decorate(page);
  }
  (async () => {
    decorate(d);
    if (d.querySelector("[data-favorites]")) {
      // Signed in: this browser and the account end up with the same list.
      const r = await fetch(api + "/wishlist").then((x) => (x.ok ? x.json() : null)).catch(() => null);
      if (r) { const local = get(), remote = r.handles || []; local.filter((h) => !remote.includes(h)).forEach((h) => fetch(api + "/wishlist/" + encodeURIComponent(h), { method: "PUT" }).catch(() => {})); set([...new Set([...local, ...remote])]); }
      renderFavorites();
    }
  })();

  // --- search suggestions while typing ---
  d.querySelectorAll("form[role=search] input[name=q], input[type=search][name=q]").forEach((input, n) => {
    if (input.dataset.suggest) return; input.dataset.suggest = "1";
    const form = input.form, box = d.createElement("div"), id = "sg-" + n;
    box.className = "suggest"; box.id = id; box.setAttribute("role", "listbox"); box.hidden = true;
    form.style.position = "relative"; form.appendChild(box);
    input.setAttribute("autocomplete", "off"); input.setAttribute("role", "combobox"); input.setAttribute("aria-controls", id); input.setAttribute("aria-expanded", "false"); input.setAttribute("aria-autocomplete", "list");
    let timer = 0, seq = 0, active = -1;
    const items = () => [...box.querySelectorAll("[role=option]")];
    const close = () => { box.hidden = true; active = -1; input.setAttribute("aria-expanded", "false"); input.removeAttribute("aria-activedescendant"); };
    const mark = () => items().forEach((el, i) => { el.classList.toggle("is-active", i === active); if (i === active) input.setAttribute("aria-activedescendant", el.id); });
    async function run() {
      const q = input.value.trim(), mine = ++seq;
      if (q.length < 2) { close(); return; }
      const r = await fetch(api + "/suggest?site=" + encodeURIComponent(site) + "&q=" + encodeURIComponent(q)).then((x) => x.json()).catch(() => null);
      if (!r || mine !== seq || d.activeElement !== input) return;
      const all = base + "/c?q=" + encodeURIComponent(q);
      let k = 0; const opt = (href, html) => "<a role='option' id='" + id + "-" + k++ + "' href='" + esc(href) + "'>" + html + "</a>";
      box.innerHTML = (r.categories || []).map((c) => opt(c.url, "<span class='sg-cat'>En " + esc(c.name) + "</span>")).join("") +
        (r.products || []).map((p) => opt(p.url, "<span class='sg-img'>" + (p.img || "<span class='ph ph-" + p.tone + "'><span>" + esc(p.initial) + "</span></span>") + "</span><span class='sg-t'>" + esc(p.title) + "<small>" + esc(p.price) + "</small></span>")).join("") +
        (r.total ? opt(all, "<span class='sg-all'>Ver los " + r.total + " resultados</span>") : "<p class='sg-none'>Sin resultados para “" + esc(q) + "”. Prueba con otra palabra.</p>");
      box.hidden = false; active = -1; input.setAttribute("aria-expanded", "true");
    }
    input.addEventListener("input", () => { clearTimeout(timer); timer = setTimeout(run, 140); });
    input.addEventListener("keydown", (e) => {
      const list = items();
      if (e.key === "Escape") { close(); return; }
      if (box.hidden || !list.length) return;
      if (e.key === "ArrowDown") { e.preventDefault(); active = (active + 1) % list.length; mark(); }
      else if (e.key === "ArrowUp") { e.preventDefault(); active = (active - 1 + list.length) % list.length; mark(); }
      else if (e.key === "Enter" && active >= 0) { e.preventDefault(); location.href = list[active].href; }
    });
    d.addEventListener("click", (e) => { if (!form.contains(e.target)) close(); });
  });

  // --- reviews ---
  const rf = d.querySelector("[data-review-form]");
  if (rf) rf.addEventListener("submit", async (e) => {
    e.preventDefault();
    const f = new FormData(rf), out = rf.querySelector("[data-review-status]"), btn = rf.querySelector("button[type=submit]");
    if (!f.get("rating")) { out.textContent = "Elige cuántas estrellas le das."; return; }
    btn.disabled = true; out.textContent = "Enviando…";
    const r = await fetch(api + "/reviews/" + encodeURIComponent(rf.dataset.reviewForm), { method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ rating: Number(f.get("rating")), title: f.get("title"), body: f.get("body"), name: f.get("name"), email: f.get("email") }) }).then(async (x) => ({ ok: x.ok, status: x.status, body: await x.json().catch(() => ({})) })).catch(() => ({ ok: false, body: {} }));
    btn.disabled = false;
    if (r.ok) { rf.reset(); out.textContent = "¡Gracias! Tu opinión se publicará cuando la tienda la revise."; }
    else out.textContent = r.status === 429 ? "Demasiados intentos. Espera un minuto." : (r.body.detail || "No pudimos enviar tu opinión. Intenta de nuevo.");
  });
})();
