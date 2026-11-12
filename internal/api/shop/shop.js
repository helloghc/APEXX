// Apexx storefront: cart in localStorage, live simulation, checkout with Stripe.
(() => {
  const body = document.body;
  // Builder pages are static per revision, so they learn the store from the URL.
  const parts = location.pathname.split("/");
  const MK = document.documentElement.getAttribute("data-mk") || "";
  const tenant = body.dataset.tenant || parts[2], base = body.dataset.base || "/s/" + parts[2] + "/" + parts[3], channel = MK || body.dataset.channel || "mx";
  const api = "/s/" + tenant + "/api";
  // The language travels in the URL; links built here keep it.
  const HL = document.documentElement.getAttribute("data-hl") || "";
  // Language and market travel in the URL; links built here keep both.
  const withLang = (u) => {
    if (!u || u.charAt(0) === "#" || u.indexOf("//") === 0 || u.indexOf("/s/assets/") === 0 || /^[a-z][a-z0-9+.-]*:/i.test(u)) return u;
    const h = u.indexOf("#"), tail = h < 0 ? "" : u.slice(h);
    if (h >= 0) u = u.slice(0, h);
    if (HL && !/[?&]hl=/.test(u)) u += (u.indexOf("?") < 0 ? "?" : "&") + "hl=" + HL;
    if (MK && !/[?&]mk=/.test(u)) u += (u.indexOf("?") < 0 ? "?" : "&") + "mk=" + MK;
    return u + tail;
  };
  if (HL) {
    const plain = window.fetch.bind(window);
    window.fetch = (u, o = {}) => (typeof u === "string" && u.indexOf(api) === 0 ? plain(u, Object.assign({}, o, { headers: Object.assign({ "X-Apexx-Lang": HL }, o.headers || {}) })) : plain(u, o));
  }
  if (HL || MK) document.addEventListener("click", (e) => {
    const a = e.target.closest && e.target.closest("a[href]");
    if (a && !a.closest(".langs")) a.setAttribute("href", withLang(a.getAttribute("href")));
  }, true);
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
  const PLAN = { "1w": "cada semana", "2w": "cada 2 semanas", "1m": "cada mes", "2m": "cada 2 meses", "3m": "cada 3 meses", "6m": "cada 6 meses" };
  const planNote = (sku) => { const l = cart().find((x) => x.sku === sku); return l && l.plan && PLAN[l.plan] ? "<br><small class='muted'>Suscripción: entrega " + PLAN[l.plan] + "</small>" : ""; };
  // Only coupons the store confirmed as applied are kept; an invalid code is never stored or shown as applied.
  const coupons = () => store.get(CPN, []);
  const simulate = (zip, extra = {}) => post("/simulate", { channel, postalCode: zip || undefined, shippingOption: store.get(SHIP, "") || undefined, items: cart().map((l) => ({ sku: l.sku, qty: l.qty, plan: l.plan || undefined })), coupons: coupons(), ...extra });
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

  // Pages published from the designer carry the prices of the main channel;
  // in another market they are refreshed in its currency.
  if (MK) {
    const cards = Array.from(document.querySelectorAll(".pcard"));
    const handles = cards.map((c) => { const a = c.querySelector("a.pcard-link"); const m = a && (a.getAttribute("href") || "").match(/(?:^|\/)p\/([^/?#]+)/); return m ? m[1] : ""; });
    const want = handles.filter(Boolean);
    if (want.length) fetch(api + "/prices?channel=" + encodeURIComponent(MK) + "&handles=" + encodeURIComponent(want.slice(0, 60).join(","))).then((r) => r.ok ? r.json() : null).then((j) => {
      if (!j) return;
      cards.forEach((c, i) => {
        const p = j.items[handles[i]], el = c.querySelector(".pcard-price");
        if (!p || !el) return;
        el.textContent = "";
        if (p.compareAt) { const sale = document.createElement("span"); sale.className = "sale"; sale.textContent = p.price; const old = document.createElement("s"); old.textContent = p.compareAt; el.append(sale, " ", old); } else el.textContent = p.price;
      });
    }).catch(() => {});
  }

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
      const plan = buy.querySelector("input[name=mode]:checked")?.value === "sub" ? (buy.querySelector("#plan")?.value || "") : "";
      if (line) { line.qty = Math.min(50, line.qty + qty); line.plan = plan; } else c.push({ sku: s.value, qty, title: s.dataset.title, plan });
      saveCart(c);
      status.innerHTML = "Agregado al carrito. <a href='" + base + "/cart'>Ver carrito</a>";
    });
  }

  function renderTotals(r) {
    const t = r.totals, adj = (t.taxAdjust && t.taxAdjust.amount) || 0;
    const taxLines = r.tax && r.tax.lines && r.tax.lines.length ? r.tax.lines : [{ label: "IVA", tax: t.taxIncluded.amount }];
    const promos = (r.promotions || []).filter((p) => p.applied || !p.coupon).map((p) => "<p class='promo" + (p.applied ? " on" : "") + "'>" + (p.applied ? "✓ " : "") + esc(p.name) + ": " + esc(p.reason) +
      (p.coupon ? " <button type='button' class='linkbtn' data-coupon-del='" + esc(p.coupon) + "'>Quitar cupón " + esc(p.coupon) + "</button>" : "") + (p.hint ? " <span class='muted'>" + esc(p.hint) + "</span>" : "") + "</p>").join("");
    const msgs = (r.messages || []).map((m) => "<li>" + esc(m.text) + "</li>").join("");
    return (msgs ? "<ul class='msgs'>" + msgs + "</ul>" : "") + promos +
      "<table class='totals'><tr><td>Productos</td><td class='num'>" + money(t.items) + "</td></tr>" +
      (t.discounts.amount ? "<tr><td>Descuentos</td><td class='num'>−" + money(t.discounts) + "</td></tr>" : "") +
      "<tr><td>" + (r.shipping && r.shipping.kind === "pickup" ? "Recolección" : "Envío") + (r.shipping ? " (" + esc(r.shipping.name) + (r.shipping.estimate ? ". " + esc(r.shipping.estimate.text) : ", " + r.shipping.estimateDays + " días") + ")" : "") + "</td><td class='num'>" + (r.shipping ? (r.shipping.original ? "<s class='muted'>" + money(r.shipping.original) + "</s> " : "") + (r.shipping.price.amount ? money(r.shipping.price) : "Gratis") : ((r.messages || []).some((m) => m.code === "no_shipping_option") ? "Sin entrega disponible" : "Ingresa tu código postal")) + "</td></tr>" +
      // Where prices exclude tax it is added before the total; an exempt buyer
      // sees the tax taken out.
      (r.fees || []).map((f) => "<tr><td>" + esc(f.name) + " <span class='muted'>" + esc(f.reason) + "</span></td><td class='num'>" + money({ amount: f.amount, currency: t.grand.currency }) + "</td></tr>").join("") +
      (adj > 0 ? taxLines.map((x) => "<tr><td>" + esc(x.label) + "</td><td class='num'>" + money({ amount: x.tax, currency: t.grand.currency }) + "</td></tr>").join("") : "") +
      (adj < 0 ? "<tr><td>Impuestos (exento)</td><td class='num'>−" + money({ amount: -adj, currency: t.grand.currency }) + "</td></tr>" : "") +
      "<tr class='grand'><td>Total</td><td class='num'>" + money(t.grand) + "</td></tr>" +
      (adj === 0 ? taxLines.map((x) =>
        "<tr><td class='muted'>" + esc(x.label) + (x.rateBps === 0 ? "" : " incluido") + "</td><td class='num muted'>" + money({ amount: x.tax, currency: t.grand.currency }) + "</td></tr>").join("") : "") + "</table>";
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
      const rows = r.body.lines.map((l) => l.gift ? "<tr><td>" + esc(l.title) + "</td><td>" + l.qty + "</td><td class='num'>Gratis</td><td></td></tr>" : "<tr><td>" + esc(l.title) + planNote(l.sku) + (l.compareAt ? " <s class='muted'>" + money(l.compareAt) + "</s>" : "") + "</td>" +
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
      if (!cart().length) { location.href = withLang(base + "/cart"); return; }
      const r = await simulate($("#postal").value.trim(), { email: $("#email").value.trim() || undefined, paymentMethod: "card" });
      if (r.status === 200 && shipBox) shipBox.innerHTML = renderOptions(r.body);
      if (r.status === 200) { last = r.body; const lost = syncCoupons(r.body); if (lost.length) errBox.textContent = lost.join(" "); summary.innerHTML = r.body.lines.map((l) => "<p>" + l.qty + " × " + esc(l.title) + planNote(l.sku) + "</p>").join("") + renderTotals(r.body); payBtn.textContent = "Pagar " + money(r.body.totals.grand); paintWallet(); if (elements) elements.update({ amount: r.body.totals.grand.amount }); }
    }
    // Gift cards and store credit pay first; the card only pays what is left.
    let cards = [];
    const gcStatus = $("[data-gc-status]"), gcList = $("[data-gc-list]"), instBox = $("[data-installments]");
    const walletOf = () => { const g = last ? last.totals.grand.amount : 0; return Math.min(g, cards.reduce((a, c) => a + c.balance, 0)); };
    const instCount = () => { const sel = $("#msi"); return sel && Number(sel.value) > 1 ? Number(sel.value) : undefined; };
    function paintWallet() {
      if (!last) return;
      const cur = last.totals.grand.currency, g = last.totals.grand.amount, w = walletOf(), due = g - w;
      if (gcList) gcList.innerHTML = cards.map((c, i) => "<li>Tarjeta ••••" + esc(c.last4) + " · saldo " + money({ amount: c.balance, currency: cur }) + " <button type='button' class='linkbtn' data-gc-del='" + i + "'>Quitar</button></li>").join("");
      const old = $("[data-wallet-rows]", summary); if (old) old.remove();
      if (w > 0) summary.insertAdjacentHTML("beforeend", "<table class='totals' data-wallet-rows><tr><td>Tarjeta de regalo o saldo</td><td class='num'>−" + money({ amount: w, currency: cur }) + "</td></tr><tr class='grand'><td>A pagar</td><td class='num'>" + money({ amount: due, currency: cur }) + "</td></tr></table>");
      payBtn.textContent = due > 0 ? "Pagar " + money({ amount: due, currency: cur }) : "Confirmar pedido";
      payBox.hidden = due === 0 || onCredit();
      const ins = cfg.installments;
      if (instBox) {
        const keep = instCount();
        instBox.innerHTML = ins && ins.enabled && due >= (ins.minAmount || 0) && due > 0 ? "<div class='field'><label for='msi'>Meses sin intereses</label><select id='msi' name='msi'><option value='1'>Un solo pago de " + money({ amount: due, currency: cur }) + "</option>" +
          ins.plans.map((n) => "<option value='" + n + "'" + (n === keep ? " selected" : "") + ">" + n + " pagos de " + money({ amount: Math.ceil(due / n), currency: cur }) + " sin intereses</option>").join("") + "</select><p class='note'>Disponible con tarjetas de crédito participantes.</p></div>" : "";
      }
    }
    // Signed-in buyers of a company: company price, and credit when the store gives it.
    let company = null;
    const coBox = $("[data-company]"), onCredit = () => !!(company && $("#paycredit") && $("#paycredit").checked);
    const TERMS = { net15: "15 días", net30: "30 días", net60: "60 días" };
    fetch(api + "/company").then((x) => (x.status === 200 ? x.json() : null)).then((c) => {
      if (!c || !coBox) return;
      company = c; $("#email").value = c.email; $("#email").readOnly = true;
      coBox.innerHTML = "<p class='testnote'>Compras para <strong>" + esc(c.name) + "</strong>" + (c.discountBps ? " con " + c.discountBps / 100 + "% de precio de empresa" : "") + ".</p>" +
        (c.terms !== "prepaid" ? "<div class='testcards'>" + (c.creditOnly ? "" : "<label><input type='radio' name='paymode' value='card' checked> Pagar con tarjeta</label>") + "<label><input type='radio' name='paymode' id='paycredit' value='credit'" + (c.creditOnly ? " checked" : "") + "> Pagar a crédito de la empresa (" + (TERMS[c.terms] || "") + ") · disponible " + money({ amount: c.available, currency: "MXN" }) + "</label></div>" : "") +
        "<div class='field'><label for='po'>Orden de compra" + (c.requirePO ? "" : " (opcional)") + "</label><input id='po' name='po' maxlength='60' autocomplete='off'" + (c.requirePO ? " required" : "") + "></div>";
      coBox.addEventListener("change", () => { payBox.hidden = onCredit(); if (instBox && onCredit()) instBox.innerHTML = ""; payBtn.textContent = onCredit() ? "Confirmar pedido a crédito" : payBtn.textContent; if (!onCredit()) paintWallet(); });
      if (c.creditOnly) { payBox.hidden = true; payBtn.textContent = "Confirmar pedido a crédito"; }
      refresh();
    }).catch(() => {});
    const gcApply = $("[data-gc-apply]");
    if (gcApply) {
      gcApply.addEventListener("click", async () => {
        const code = $("#gc").value.trim().toUpperCase(); if (!code) { gcStatus.textContent = "Escribe el código de tu tarjeta."; return; }
        if (cards.some((c) => c.code.replace(/[^A-Z0-9]/g, "") === code.replace(/[^A-Z0-9]/g, ""))) { gcStatus.textContent = "Esa tarjeta ya está aplicada."; return; }
        gcApply.disabled = true;
        const r = await post("/giftcard", { code }).catch(() => ({ status: 0, body: {} }));
        gcApply.disabled = false;
        if (r.status === 200) { cards.push({ code, last4: r.body.last4, balance: r.body.balance.amount }); $("#gc").value = ""; gcStatus.textContent = "Tarjeta aplicada."; paintWallet(); }
        else gcStatus.textContent = r.status === 429 ? "Demasiados intentos. Espera un minuto." : (r.body.detail || "No pudimos validar la tarjeta. Intenta de nuevo.");
      });
      $("#gc").addEventListener("keydown", (e) => { if (e.key === "Enter") { e.preventDefault(); gcApply.click(); } });
      gcList.addEventListener("click", (e) => { const b = e.target.closest("[data-gc-del]"); if (b) { cards.splice(Number(b.dataset.gcDel), 1); gcStatus.textContent = "Tarjeta quitada."; paintWallet(); } });
    }
    $("#postal").addEventListener("change", () => { store.set(ZIP, $("#postal").value.trim()); refresh(); });
    $("#email").addEventListener("change", refresh); // promotions limited per customer are confirmed with the email
    // Remember the cart of a shopper who typed an email (abandoned cart recovery).
    const track = () => { const em = $("#email").value.trim(); if (/^\S+@\S+\.\S+$/.test(em) && cart().length) post("/cart/track", { email: em, name: $("#name").value.trim(), channel, site: base.split("/").pop(), items: cart().map((l) => ({ sku: l.sku, qty: l.qty, plan: l.plan || undefined })) }).catch(() => {}); };
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
      if (!(pickup() && !String(f.get("postal"))) && !(MK ? /^[A-Za-z0-9][A-Za-z0-9 -]{1,9}$/ : /^[0-9]{5}$/).test(String(f.get("postal")).trim())) { errBox.textContent = MK ? "Revisa tu código postal." : "El código postal debe tener 5 dígitos."; $("#postal").focus(); return; }
      payBtn.disabled = true; payBtn.textContent = "Procesando…";
      const covered = onCredit() || (!!last && walletOf() >= last.totals.grand.amount); // credit or gift cards pay it all: no card needed
      let pm = onCredit() ? "credit" : covered ? "pm_gift_card" : String(f.get("pm") || "");
      if (stripe && !covered) {
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
        channel, postalCode: String(f.get("postal")), items: cart().map((l) => ({ sku: l.sku, qty: l.qty, plan: l.plan || undefined })),
        shippingOption: last && last.shipping ? last.shipping.id : undefined, acceptedTerms: !!f.get("terms") || undefined,
        customer: { email: f.get("email"), name: f.get("name"), phone: f.get("phone") || "", address: f.get("address"), notes: f.get("notes") || undefined }, poNumber: company ? String(f.get("po") || "").trim() || undefined : undefined, paymentMethod: pm,
        coupons: coupons(), giftCards: cards.length ? cards.map((c) => c.code) : undefined, installments: instCount(), expectedTotal: last ? last.totals.grand.amount : undefined,
      }, { "Idempotency-Key": key });
      if (res.status === 201) {
        sessionStorage.removeItem("apexx_idem"); sessionStorage.setItem("apexx_last_order", JSON.stringify(res.body));
        store.set(KEY, []); store.set(CPN, []); location.href = withLang(base + "/gracias"); return;
      }
      sessionStorage.removeItem("apexx_idem");
      if (res.status === 202 && res.body.code === "approval_required") {
        store.set(KEY, []); store.set(CPN, []);
        const done = document.createElement("div"); done.className = "acct-box"; done.setAttribute("role", "status");
        const p1 = document.createElement("p"); p1.textContent = res.body.detail; const p2 = document.createElement("p");
        const a = document.createElement("a"); a.className = "btn"; a.href = withLang(base + "/cuenta?ver=empresa"); a.textContent = "Ver mis solicitudes"; p2.appendChild(a);
        done.append(p1, p2); form.replaceWith(done); return;
      }
      const msg = { payment_declined: "Tu pago fue rechazado. Prueba con otra tarjeta.", cart_changed: "Algunos productos cambiaron de precio o disponibilidad. Revisa el resumen.",
        insufficient_stock: "Ya no hay suficientes unidades de un producto. Ajusta tu carrito.", payment_pending: "No pudimos confirmar el pago todavía. No vuelvas a pagar: te avisaremos por correo.",
        temporarily_unavailable: "La tienda no pudo procesar tu compra en este momento. No se hizo ningún cargo; intenta de nuevo en unos segundos.", postal_code_required: "Revisa tu código postal.", no_shipping_option: "No hay entregas disponibles para tu código postal.", shipping_option_unavailable: "La opción de entrega cambió. Revisa las opciones y vuelve a intentar.",
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
      (o.feeLines || []).map((f) => "<tr><td>" + esc(f.name) + "</td><td class='num'>" + m(f.amount) + "</td></tr>").join("") +
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
      else if (e.key === "Enter" && active >= 0) { e.preventDefault(); location.href = withLang(list[active].getAttribute("href")); }
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

/* ==== subscription manage page ==== */
(() => {
  const box = document.querySelector("[data-subscription]"); if (!box) return;
  const tenant = document.body.dataset.tenant, out = box.querySelector("[data-sub-status]");
  box.addEventListener("click", async (e) => {
    const b = e.target.closest("[data-sub-act]"); if (!b) return;
    if (b.dataset.confirm && !confirm(b.dataset.confirm)) return;
    box.querySelectorAll("[data-sub-act]").forEach((x) => (x.disabled = true)); out.textContent = "Un momento…";
    const r = await fetch("/s/" + tenant + "/api/subscription/" + encodeURIComponent(box.dataset.subscription) + "/" + b.dataset.subAct, { method: "POST" }).then(async (x) => ({ ok: x.ok, body: await x.json().catch(() => ({})) })).catch(() => ({ ok: false, body: {} }));
    if (r.ok) location.reload();
    else { out.textContent = r.body.detail || "No pudimos hacer el cambio. Intenta de nuevo."; box.querySelectorAll("[data-sub-act]").forEach((x) => (x.disabled = false)); }
  });
})();

/* ==== public forms ==== */
(() => {
  const f = document.querySelector("[data-form]"); if (!f) return;
  const tenant = document.body.dataset.tenant, err = f.querySelector("[data-form-error]"), ok = document.querySelector("[data-form-ok]"), btn = f.querySelector("button[type=submit]");
  f.addEventListener("submit", async (e) => {
    e.preventDefault(); err.textContent = "";
    const data = {}; let website = "";
    for (const el of f.elements) { if (!el.name) continue; if (el.name === "website") website = el.value; else data[el.name] = el.type === "checkbox" ? (el.checked ? "true" : "") : el.value; }
    btn.disabled = true;
    const r = await fetch("/s/" + tenant + "/api/forms/" + encodeURIComponent(f.dataset.form), { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ data, website }) })
      .then(async (x) => ({ status: x.status, body: await x.json().catch(() => ({})) })).catch(() => ({ status: 0, body: {} }));
    btn.disabled = false;
    if (r.status === 201 || r.status === 204) { f.hidden = true; ok.hidden = false; ok.textContent = r.body.message || "¡Gracias! Recibimos tu información."; ok.focus && ok.focus(); return; }
    err.textContent = r.status === 429 ? "Enviaste varios formularios seguidos. Espera un minuto." : (r.body.detail || "No pudimos enviar el formulario. Intenta de nuevo.");
    const first = r.body.errors && r.body.errors[0] && f.elements[r.body.errors[0].field]; if (first && first.focus) first.focus();
  });
})();

/* ==== purchase approvals (B2B) ==== */
(() => {
  const box = document.querySelector("[data-approval]"); if (!box) return;
  const tenant = document.body.dataset.tenant, out = box.querySelector("[data-approval-status]");
  box.addEventListener("click", async (e) => {
    const b = e.target.closest("[data-approval-act]"); if (!b) return;
    if (b.dataset.confirm && !confirm(b.dataset.confirm)) return;
    box.querySelectorAll("[data-approval-act]").forEach((x) => (x.disabled = true)); out.textContent = "Un momento…";
    const r = await fetch("/s/" + tenant + "/api/approval/" + encodeURIComponent(box.dataset.approval) + "/" + b.dataset.approvalAct, { method: "POST" }).then(async (x) => ({ ok: x.ok, body: await x.json().catch(() => ({})) })).catch(() => ({ ok: false, body: {} }));
    if (r.ok && r.body.status === "pending") {
      // Their level is signed; the next level gets its own link by email.
      box.querySelectorAll("[data-approval-act]").forEach((x) => x.remove());
      out.textContent = "Aprobaste tu nivel. Enviamos la compra a quien aprueba el nivel " + r.body.nextLevel + "; el pedido se coloca cuando lo apruebe.";
      return;
    }
    if (r.ok) location.reload();
    else { out.textContent = r.body.detail || "No pudimos registrar tu decisión. Intenta de nuevo."; box.querySelectorAll("[data-approval-act]").forEach((x) => (x.disabled = false)); }
  });
})();

/* ==== quotes (B2B) ==== */
(() => {
  const box = document.querySelector("[data-quote]"); if (!box) return;
  const tenant = document.body.dataset.tenant, out = box.querySelector("[data-quote-status]");
  box.addEventListener("click", async (e) => {
    const b = e.target.closest("[data-quote-act]"); if (!b) return;
    if (b.dataset.confirm && !confirm(b.dataset.confirm)) return;
    const msg = (document.getElementById("quote-msg") || {}).value || "";
    if (b.dataset.quoteAct === "changes" && !msg.trim()) { out.textContent = "Escribe qué te gustaría cambiar."; return; }
    box.querySelectorAll("[data-quote-act]").forEach((x) => (x.disabled = true)); out.textContent = "Un momento…";
    const r = await fetch("/s/" + tenant + "/api/quotes/" + encodeURIComponent(box.dataset.quote) + "/" + b.dataset.quoteAct, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ message: msg }) })
      .then(async (x) => ({ ok: x.ok, body: await x.json().catch(() => ({})) })).catch(() => ({ ok: false, body: {} }));
    if (r.ok && r.body.payUrl) { location.href = r.body.payUrl; return; }
    if (r.ok) { location.reload(); return; }
    out.textContent = r.body.detail || "No pudimos registrar tu respuesta. Intenta de nuevo."; box.querySelectorAll("[data-quote-act]").forEach((x) => (x.disabled = false));
  });
})();

/* ==== quote request from the cart ==== */
(() => {
  const f = document.querySelector("[data-quote-request]"); if (!f) return;
  const tenant = document.body.dataset.tenant, out = f.querySelector("[data-quote-request-status]");
  const mk = document.documentElement.getAttribute("data-mk") || "";
  f.addEventListener("submit", async (e) => {
    e.preventDefault();
    let items = [];
    try { items = (JSON.parse(localStorage.getItem("apexx_cart_" + tenant) || "[]") || []).map((x) => ({ sku: x.sku, qty: x.qty })); } catch (err) { items = []; }
    if (!items.length) { out.textContent = "Agrega productos antes de pedir una cotización."; return; }
    const d = new FormData(f);
    if (!String(d.get("email") || "").includes("@") || !String(d.get("name") || "").trim()) { out.textContent = "Escribe tu nombre y tu correo."; return; }
    f.querySelector("button[type=submit]").disabled = true; out.textContent = "Un momento…";
    const r = await fetch("/s/" + tenant + "/api/quotes", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ channel: mk || document.body.dataset.channel || "mx", items, customer: { name: d.get("name"), email: d.get("email"), phone: d.get("phone") || "" }, message: d.get("message") || "", website: d.get("website") || "" }) })
      .then(async (x) => ({ ok: x.ok, status: x.status, body: await x.json().catch(() => ({})) })).catch(() => ({ ok: false, body: {} }));
    if (r.ok) { f.innerHTML = "<p class='acct-ok'>Recibimos tu solicitud de cotización #" + (r.body.number || "") + ". Te escribiremos a tu correo con la oferta.</p>" + (r.body.url ? "<p><a href='" + r.body.url + "'>Ver mi cotización</a></p>" : ""); return; }
    f.querySelector("button[type=submit]").disabled = false;
    out.textContent = r.status === 429 ? "Enviaste varias solicitudes seguidas. Espera un minuto." : (r.body.detail || "No pudimos enviar tu solicitud. Intenta de nuevo.");
  });
})();

/* ==== quick order: SKU and quantity per line, or a CSV ==== */
(() => {
  const f = document.querySelector("[data-quick-order]"); if (!f) return;
  const body = document.body, tenant = body.dataset.tenant, mk = document.documentElement.getAttribute("data-mk") || "";
  const ta = f.querySelector("#qo-lines"), out = f.querySelector("[data-quick-status]"), res = f.querySelector("[data-quick-result]"), add = f.querySelector("[data-quick-add]");
  const esc = (s) => String(s).replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
  let ready = [];
  const parse = (text) => {
    const lines = [], bad = [];
    text.split(/\r?\n/).forEach((raw, i) => {
      const l = raw.trim(); if (!l) return;
      const parts = l.split(/[,;\t ]+/).filter(Boolean);
      const sku = parts[0], qty = Number(parts[1] || "1");
      if (i === 0 && /sku/i.test(sku)) return; // header row
      if (!sku || !Number.isInteger(qty) || qty < 1 || qty > 100000) bad.push(i + 1); else lines.push({ sku, qty });
    });
    const merged = {}; lines.forEach((x) => { merged[x.sku] = (merged[x.sku] || 0) + x.qty; });
    return { items: Object.entries(merged).map(([sku, qty]) => ({ sku, qty })), bad };
  };
  f.querySelector("#qo-file").addEventListener("change", (e) => {
    const file = e.target.files && e.target.files[0]; if (!file) return;
    if (file.size > 1048576) { out.textContent = "El archivo pesa más de 1 MB."; return; }
    const r = new FileReader(); r.onload = () => { ta.value = String(r.result || ""); check(); }; r.readAsText(file);
  });
  const check = async () => {
    const { items, bad } = parse(ta.value); ready = []; add.disabled = true; res.innerHTML = "";
    if (bad.length) { out.textContent = "Revisa las líneas " + bad.slice(0, 10).join(", ") + ": escribe SKU y cantidad."; return; }
    if (!items.length) { out.textContent = "Escribe al menos un SKU."; return; }
    if (items.length > 200) { out.textContent = "Hasta 200 productos por pedido rápido."; return; }
    out.textContent = "Revisando…";
    const r = await fetch("/s/" + tenant + "/api/simulate", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ channel: mk || body.dataset.channel || "mx", items }) })
      .then(async (x) => ({ ok: x.ok, body: await x.json().catch(() => ({})) })).catch(() => ({ ok: false, body: {} }));
    if (!r.ok) { out.textContent = r.body.detail || "No pudimos revisar tu lista. Intenta de nuevo."; return; }
    const found = {}; (r.body.lines || []).forEach((l) => { found[l.sku] = l; });
    const notes = {}; (r.body.messages || []).forEach((m) => { if (m.sku) notes[m.sku] = m.text; });
    let rows = "", okCount = 0;
    items.forEach((it) => {
      const l = found[it.sku];
      const okLine = l && l.qty > 0;
      if (okLine) { ready.push({ sku: it.sku, qty: l.qty }); okCount++; }
      rows += "<tr><td><code>" + esc(it.sku) + "</code><br><small>" + esc(l ? l.title : "") + "</small></td><td class='num'>" + it.qty + "</td><td class='num'>" + (l ? l.qty : 0) + "</td><td>" + esc(notes[it.sku] || (okLine ? "Listo" : "No se encontró")) + "</td></tr>";
    });
    res.innerHTML = "<table><thead><tr><th>Producto</th><th class='num'>Pediste</th><th class='num'>Se agrega</th><th>Estado</th></tr></thead><tbody>" + rows + "</tbody></table>";
    out.textContent = okCount ? okCount + " de " + items.length + " productos listos para agregar." : "Ningún producto se puede agregar.";
    add.disabled = okCount === 0;
  };
  f.querySelector("[data-quick-check]").addEventListener("click", check);
  f.addEventListener("submit", (e) => {
    e.preventDefault(); if (!ready.length) return;
    let cart = []; try { cart = JSON.parse(localStorage.getItem("apexx_cart_" + tenant) || "[]") || []; } catch (err) { cart = []; }
    ready.forEach((x) => { const l = cart.find((c) => c.sku === x.sku && !c.plan); if (l) l.qty += x.qty; else cart.push({ sku: x.sku, qty: x.qty }); });
    try { localStorage.setItem("apexx_cart_" + tenant, JSON.stringify(cart)); } catch (err) { out.textContent = "Tu navegador no permite guardar el carrito."; return; }
    location.href = (body.dataset.base || "") + "/cart" + location.search;
  });
})();

/* ==== reorder lists ==== */
(() => {
  const tenant = document.body.dataset.tenant;
  const cartKey = "apexx_cart_" + tenant;
  const readCart = () => { try { return JSON.parse(localStorage.getItem(cartKey) || "[]") || []; } catch (e) { return []; } };
  const send = (method, path, body) => fetch("/s/" + tenant + "/api/lists" + path, { method, headers: { "Content-Type": "application/json" }, body: body ? JSON.stringify(body) : undefined })
    .then(async (x) => ({ ok: x.ok, status: x.status, body: await x.json().catch(() => ({})) })).catch(() => ({ ok: false, status: 0, body: {} }));
  const save = document.querySelector("[data-save-list]");
  if (save) save.addEventListener("click", async () => {
    const out = document.querySelector("[data-save-list-status]");
    const items = readCart().filter((l) => !l.plan).map((l) => ({ sku: l.sku, qty: l.qty }));
    if (!items.length) { out.textContent = "Agrega productos antes de guardar una lista."; return; }
    const name = prompt("Nombre de la lista", "Pedido de cada mes");
    if (name === null) return;
    const r = await send("POST", "", { name: name.trim() || "Mi lista", items });
    if (r.ok) out.innerHTML = "Lista guardada. Encuéntrala en <a href='" + (document.body.dataset.base || "") + "/cuenta?ver=listas'>Mis listas</a>.";
    else out.textContent = r.status === 401 ? "Entra a tu cuenta para guardar listas." : (r.body.detail || "No pudimos guardar la lista. Intenta de nuevo.");
  });
  document.querySelectorAll("[data-reorder]").forEach((box) => {
    const out = box.querySelector("[data-reorder-status]");
    box.querySelector("[data-reorder-add]").addEventListener("click", () => {
      const cart = readCart();
      box.querySelectorAll("tr[data-sku]").forEach((tr) => {
        const sku = tr.dataset.sku, qty = Number(tr.dataset.qty) || 1;
        const l = cart.find((c) => c.sku === sku && !c.plan); if (l) l.qty += qty; else cart.push({ sku, qty });
      });
      try { localStorage.setItem(cartKey, JSON.stringify(cart)); } catch (e) { out.textContent = "Tu navegador no permite guardar el carrito."; return; }
      location.href = (document.body.dataset.base || "") + "/cart";
    });
    box.querySelector("[data-reorder-delete]").addEventListener("click", async (e) => {
      if (!confirm(e.currentTarget.dataset.confirm)) return;
      const r = await send("DELETE", "/" + encodeURIComponent(box.dataset.reorder));
      if (r.ok) box.remove(); else out.textContent = r.body.detail || "No pudimos borrar la lista. Intenta de nuevo.";
    });
  });
})();

/* ==== company portal ==== */
(() => {
  const tenant = document.body.dataset.tenant;
  const send = (method, path, body) => fetch("/s/" + tenant + "/api/account/company" + path, { method, headers: { "Content-Type": "application/json" }, body: body ? JSON.stringify(body) : undefined })
    .then(async (x) => ({ ok: x.ok, status: x.status, body: await x.json().catch(() => ({})) })).catch(() => ({ ok: false, status: 0, body: {} }));
  document.querySelectorAll("[data-request-cancel]").forEach((b) => b.addEventListener("click", async () => {
    if (!confirm(b.dataset.confirm)) return;
    const tr = b.closest("[data-request]");
    const r = await send("POST", "/requests/" + encodeURIComponent(tr.dataset.request) + "/cancel");
    if (r.ok) { b.replaceWith(document.createTextNode(" · Cancelada")); } else alert(r.body.detail || "No pudimos cancelar la solicitud. Intenta de nuevo.");
  }));
  const f = document.querySelector("[data-members]");
  if (!f) return;
  const rows = f.querySelector("[data-member-rows]"), out = f.querySelector("[data-members-status]");
  const wire = (tr) => tr.querySelector("[data-member-remove]").addEventListener("click", () => tr.remove());
  rows.querySelectorAll("[data-member]").forEach(wire);
  f.querySelector("[data-member-add]").addEventListener("click", () => {
    const first = rows.querySelector("[data-member]");
    if (!first) return;
    const tr = first.cloneNode(true);
    tr.querySelectorAll("input").forEach((i) => { if (i.type === "checkbox") i.checked = false; else i.value = ""; });
    tr.querySelectorAll("select").forEach((s) => { s.selectedIndex = 0; });
    rows.appendChild(tr); wire(tr); tr.querySelector("input[name=email]").focus();
  });
  const cents = (v) => { const n = Number(String(v).replace(/[$,\s]/g, "")); return Number.isFinite(n) && n > 0 ? Math.round(n * 100) : 0; };
  f.addEventListener("submit", async (e) => {
    e.preventDefault();
    const items = [...rows.querySelectorAll("[data-member]")].map((tr) => {
      const v = (n) => tr.querySelector("[name=" + n + "]");
      const level = Number(v("level").value) || 0;
      return { email: v("email").value.trim(), monthlyLimit: cents(v("cap").value), location: v("location").value, approver: level > 0, level: level || 1,
        admin: v("admin").checked, awayUntil: level > 0 ? v("away").value : "", substitute: level > 0 ? v("substitute").value.trim() : "" };
    }).filter((m) => m.email);
    out.textContent = "Guardando…";
    const r = await send("PUT", "/members", { items });
    out.textContent = r.ok ? "Compradores guardados." : (r.body.detail || "No pudimos guardar. Intenta de nuevo.");
  });
})();

/* ==== company catalog: a company that buys only what it negotiated ==== */
(() => {
  const body = document.body, tenant = body.dataset.tenant; if (!tenant) return;
  const key = "apexx_co_" + tenant, channel = new URLSearchParams(location.search).get("mk") || body.dataset.channel || "mx";
  const paint = (el, v) => { el.textContent = ""; if (v.compareAt) { const sale = document.createElement("span"); sale.className = "sale"; sale.textContent = v.price; const old = document.createElement("s"); old.textContent = v.compareAt; el.append(sale, " ", old); } else el.textContent = v.price; };
  const prices = () => {
    // The company's own prices on the cards and product shown.
    const cards = [...document.querySelectorAll(".pcard")], hOf = (c) => { const a = c.querySelector("a.pcard-link"); const m = a && /\/p\/([^/?#]+)/.exec(a.getAttribute("href") || ""); return m ? decodeURIComponent(m[1]) : ""; };
    const here = (/\/p\/([^/?#]+)/.exec(location.pathname) || [])[1];
    const want = [...new Set(cards.map(hOf).concat(here ? [decodeURIComponent(here)] : []).filter(Boolean))].slice(0, 60);
    if (!want.length) return;
    fetch("/s/" + tenant + "/api/company/prices?channel=" + encodeURIComponent(channel) + "&handles=" + encodeURIComponent(want.join(","))).then((x) => (x.status === 200 ? x.json() : null)).then((j) => {
      if (!j) return;
      cards.forEach((c) => { const v = j.handles[hOf(c)], el = c.querySelector(".pcard-price"); if (v && el) paint(el, v); });
      const radios = document.querySelectorAll("form[data-buy] input[name=sku]"), out = document.querySelector("[data-price-out]");
      radios.forEach((r) => { const v = j.skus[r.value]; if (v) { r.dataset.price = v.price; r.dataset.compare = v.compareAt || ""; } });
      const chosen = document.querySelector("form[data-buy] input[name=sku]:checked") || radios[0];
      const v = chosen ? j.skus[chosen.value] : here && j.handles[decodeURIComponent(here)];
      if (v && out) paint(out, v);
    }).catch(() => {});
  };
  const apply = (c) => {
    if (c && c.group) prices();
    if (!c || !c.restricted) return;
    const ok = new Set(c.handles || []), handleOf = (href) => { const m = /\/p\/([^/?#]+)/.exec(href || ""); return m ? decodeURIComponent(m[1]) : ""; };
    document.querySelectorAll(".pcard").forEach((card) => { const a = card.querySelector("a.pcard-link"); if (a && !ok.has(handleOf(a.getAttribute("href")))) { card.hidden = true; card.style.display = "none"; } });
    const here = handleOf(location.pathname), buy = document.querySelector("form[data-buy]");
    if (here && buy && !ok.has(here)) {
      buy.querySelectorAll("button, input, select").forEach((x) => (x.disabled = true));
      const note = document.createElement("p"); note.className = "note"; note.textContent = "Este producto no está en el catálogo de tu empresa."; buy.after(note);
    }
  };
  // Signing in or out happens in "Mi cuenta": ask again after visiting it.
  if (/\/cuenta/.test(location.pathname)) { try { sessionStorage.removeItem(key); } catch (e) { /* private mode */ } }
  let cached = null; try { cached = JSON.parse(sessionStorage.getItem(key) || "null"); } catch (e) { cached = null; }
  if (cached && cached.at > Date.now() - 300000 && cached.channel === channel) { apply(cached.c); return; }
  fetch("/s/" + tenant + "/api/company?channel=" + encodeURIComponent(channel)).then((x) => (x.status === 200 ? x.json() : null)).then((c) => {
    try { sessionStorage.setItem(key, JSON.stringify({ at: Date.now(), channel, c })); } catch (e) { /* private mode */ }
    apply(c);
  }).catch(() => {});
})();

/* ==== company autopay: the company administrator saves a card ==== */
(() => {
  const box = document.querySelector("[data-autopay]"); if (!box) return;
  const tenant = document.body.dataset.tenant, api = "/s/" + tenant + "/api", out = box.querySelector("[data-autopay-status]");
  const call = (method, body) => fetch(api + "/account/company/autopay", { method, headers: { "Content-Type": "application/json" }, body: body ? JSON.stringify(body) : undefined })
    .then(async (x) => ({ ok: x.ok, body: await x.json().catch(() => ({})) })).catch(() => ({ ok: false, body: {} }));
  const stop = box.querySelector("[data-autopay-stop]");
  if (stop) stop.addEventListener("click", async () => {
    if (!confirm(stop.dataset.confirm)) return;
    const r = await call("DELETE"); if (r.ok) location.reload(); else out.textContent = r.body.detail || "No pudimos apagarlo. Intenta de nuevo.";
  });
  const f = box.querySelector("[data-autopay-form]"); if (!f) return;
  const slot = f.querySelector("[data-autopay-card]"), btn = f.querySelector("button[type=submit]");
  let stripe = null, elements = null;
  (async () => {
    let cfg = { mode: "sandbox" }; try { cfg = await (await fetch(api + "/config")).json(); } catch (e) { /* sandbox */ }
    if (cfg.mode === "stripe" && cfg.publishableKey) {
      await new Promise((ok, bad) => { const s = document.createElement("script"); s.src = "https://js.stripe.com/v3/"; s.onload = ok; s.onerror = bad; document.head.appendChild(s); });
      stripe = window.Stripe(cfg.publishableKey, cfg.stripeAccount ? { stripeAccount: cfg.stripeAccount } : {});
      elements = stripe.elements({ mode: "setup", currency: (cfg.currency || "mxn").toLowerCase(), paymentMethodCreation: "manual" });
      slot.innerHTML = "<div id='ap-pe'></div>"; elements.create("payment").mount("#ap-pe");
    } else if (cfg.mode === "unavailable") {
      slot.innerHTML = "<p class='error'>La tienda todavía no puede recibir pagos.</p>"; btn.disabled = true;
    } else {
      slot.innerHTML = "<p class='testnote'>Modo de prueba: no se hace ningún cargo real.</p><div class='testcards'>" +
        "<label><input type='radio' name='pm' value='pm_card_visa' checked> Tarjeta de prueba que se aprueba</label>" +
        "<label><input type='radio' name='pm' value='pm_card_chargeDeclined'> Tarjeta de prueba que se rechaza</label></div>";
    }
  })();
  f.addEventListener("submit", async (e) => {
    e.preventDefault(); btn.disabled = true; out.textContent = "Guardando…";
    let pm = String(new FormData(f).get("pm") || "");
    if (stripe) {
      const sub = await elements.submit(); const r = sub.error ? sub : await stripe.createPaymentMethod({ elements });
      if (r.error) { out.textContent = r.error.message; btn.disabled = false; return; }
      pm = r.paymentMethod.id;
    }
    const r = await call("PUT", { paymentMethod: pm });
    if (r.ok) { location.reload(); return; }
    out.textContent = r.body.detail || "No pudimos guardar la tarjeta. Intenta de nuevo."; btn.disabled = false;
  });
})();
