// Apexx tags runtime: wrapped as (function(C){ … })(config) in /s/{store}/api/tags.js.
// C = { v: version, banner: text, tags: [{id, name, kind, account, label, src, code, cat, fire, url}] }
// The storefront queues commerce events in window.apexxEvents and announces
// new ones with the "apexx:track" event; each tag gets them in its format.
"use strict";
var d = document, w = window, b = d.body, loc = location;
// Never on payment, approval, quote or account pages (their policy forbids it too).
if (!b || /\/(checkout|cuenta|pagar|aprobar|cotizacion)(\/|$)/.test(loc.pathname)) return;
var tenant = b.dataset.tenant || loc.pathname.split("/")[2];
var KEY = "apexx_consent_" + tenant;
var debug = /[?&]apexx_tags=prueba\b/.test(loc.search);
try { if (debug) sessionStorage.setItem("apexx_tags_debug", "1"); else debug = sessionStorage.getItem("apexx_tags_debug") === "1"; } catch (e) {}
var tags = C.tags || [];
w.dataLayer = w.dataLayer || [];
var queue = w.apexxEvents = w.apexxEvents || [];

// ---------- test mode: what fired and why ----------
var log = [], panel = null;
var note = function (tag, what) {
  log.push({ at: new Date().toLocaleTimeString(), tag: tag, what: what });
  if (!debug) return;
  if (!panel) {
    panel = d.createElement("aside");
    panel.setAttribute("aria-label", "Prueba de etiquetas");
    panel.style.cssText = "position:fixed;right:12px;bottom:12px;z-index:2147483647;max-width:380px;max-height:50vh;overflow:auto;background:#111;color:#eee;font:12px/1.4 system-ui,sans-serif;padding:10px 12px;border-radius:8px;box-shadow:0 4px 18px rgba(0,0,0,.35)";
    d.body.appendChild(panel);
  }
  panel.innerHTML = "";
  var h = d.createElement("strong"); h.textContent = "Etiquetas · versión " + C.v + " (modo prueba)"; panel.appendChild(h);
  var close = d.createElement("button"); close.type = "button"; close.textContent = "Salir"; close.style.cssText = "float:right;font:inherit;background:#333;color:#eee;border:0;border-radius:4px;padding:0 6px;cursor:pointer";
  close.addEventListener("click", function () { try { sessionStorage.removeItem("apexx_tags_debug"); } catch (e) {} panel.remove(); panel = null; debug = false; });
  panel.appendChild(close);
  log.slice(-40).forEach(function (l) { var p = d.createElement("div"); p.textContent = l.at + " · " + l.tag + ": " + l.what; panel.appendChild(p); });
};

// ---------- consent ----------
var readConsent = function () { try { return JSON.parse(localStorage.getItem(KEY) || "null"); } catch (e) { return null; } };
var writeConsent = function (c) { try { localStorage.setItem(KEY, JSON.stringify({ analytics: !!c.analytics, marketing: !!c.marketing, at: Date.now() })); } catch (e) {} };
var consent = readConsent();
// Global Privacy Control: nothing optional unless the shopper turns it on.
if (!consent && navigator.globalPrivacyControl) { consent = { analytics: false, marketing: false }; writeConsent(consent); }
var allowed = function (t) { return t.cat === "necessary" || !!(consent && consent[t.cat]); };

// ---------- helpers ----------
var loaded = {};
var load = function (src) {
  if (loaded[src]) return;
  loaded[src] = 1;
  var s = d.createElement("script"); s.async = true; s.src = src; d.head.appendChild(s);
};
var enc = encodeURIComponent;
var urlOk = function (t) { return !t.url || (loc.pathname + loc.search).indexOf(t.url) >= 0; };
var fires = function (t, ev) { return (t.fire || []).indexOf(ev) >= 0; };
var gtagReady = function () {
  if (!w.gtag) { w.gtag = function () { w.dataLayer.push(arguments); }; w.gtag("js", new Date()); }
};
var items = function (ec) { return (ec && ec.items) || []; };
var ids = function (ec) { return items(ec).map(function (i) { return i.item_id; }); };
var count = function (ec) { return items(ec).reduce(function (a, i) { return a + (i.quantity || 1); }, 0); };
var money = function (ec) { var o = {}; if (ec && ec.value != null) o.value = ec.value; if (ec && ec.currency) o.currency = ec.currency; return o; };

// Each provider: start (loads it and counts the page view) and send (one event).
var P = {
  gtm: {
    start: function (t) {
      w.dataLayer.push({ "gtm.start": Date.now(), event: "gtm.js" });
      load("https://www.googletagmanager.com/gtm.js?id=" + enc(t.account));
    },
    send: function () { /* the container reads the dataLayer itself */ }
  },
  ga4: {
    start: function (t) { gtagReady(); load("https://www.googletagmanager.com/gtag/js?id=" + enc(t.account)); w.gtag("config", t.account); },
    send: function (t, ev, ec) { if (ev !== "page_view") w.gtag("event", ev, Object.assign({ send_to: t.account }, ec || {})); }
  },
  gads: {
    start: function (t) { gtagReady(); load("https://www.googletagmanager.com/gtag/js?id=" + enc(t.account)); w.gtag("config", t.account); },
    send: function (t, ev, ec) {
      if (!fires(t, ev)) return false;
      var p = Object.assign({ send_to: t.account + (t.label ? "/" + t.label : "") }, money(ec));
      if (ec && ec.transaction_id) p.transaction_id = ec.transaction_id;
      w.gtag("event", "conversion", p);
    }
  },
  meta: {
    names: { view_item: "ViewContent", add_to_cart: "AddToCart", begin_checkout: "InitiateCheckout", purchase: "Purchase" },
    start: function (t) {
      if (!w.fbq) {
        var f = w.fbq = function () { f.callMethod ? f.callMethod.apply(f, arguments) : f.queue.push(arguments); };
        if (!w._fbq) w._fbq = f;
        f.push = f; f.loaded = true; f.version = "2.0"; f.queue = [];
        load("https://connect.facebook.net/en_US/fbevents.js");
      }
      w.fbq("init", t.account);
      w.fbq("trackSingle", t.account, "PageView");
    },
    send: function (t, ev, ec) {
      var n = this.names[ev]; if (!n) return false;
      var p = Object.assign({ content_ids: ids(ec), content_type: "product", num_items: count(ec) }, money(ec));
      // The order id is the event id: the platform drops the server's copy of the same purchase.
      w.fbq("trackSingle", t.account, n, p, ec && ec.transaction_id ? { eventID: ec.transaction_id } : undefined);
    }
  },
  tiktok: {
    names: { view_item: "ViewContent", add_to_cart: "AddToCart", begin_checkout: "InitiateCheckout", purchase: "CompletePayment" },
    start: function (t) {
      if (!w.ttq) {
        var q = w.ttq = [];
        q.methods = ["page", "track", "identify", "instances", "debug", "on", "off", "once", "ready", "alias", "group", "enableCookie", "disableCookie"];
        q.setAndDefer = function (o, m) { o[m] = function () { o.push([m].concat([].slice.call(arguments, 0))); }; };
        q.methods.forEach(function (m) { q.setAndDefer(q, m); });
        q.instance = function (id) { var e = q._i[id] || []; q.methods.forEach(function (m) { q.setAndDefer(e, m); }); return e; };
        q._i = {}; q._t = {}; q._o = {};
        q.load = function (id) { q._i[id] = []; q._i[id]._u = "https://analytics.tiktok.com/i18n/pixel/events.js"; q._t[id] = +new Date(); q._o[id] = {}; load("https://analytics.tiktok.com/i18n/pixel/events.js?sdkid=" + enc(id) + "&lib=ttq"); };
      }
      w.ttq.load(t.account);
      w.ttq.instance(t.account).page();
    },
    send: function (t, ev, ec) {
      var n = this.names[ev]; if (!n) return false;
      var p = Object.assign({ content_type: "product", contents: items(ec).map(function (i) { return { content_id: i.item_id, content_name: i.item_name, quantity: i.quantity || 1, price: i.price }; }) }, money(ec));
      w.ttq.instance(t.account).track(n, p, ec && ec.transaction_id ? { event_id: ec.transaction_id } : undefined);
    }
  },
  pinterest: {
    names: { view_item: "pagevisit", add_to_cart: "addtocart", purchase: "checkout" },
    start: function (t) {
      if (!w.pintrk) {
        var p = w.pintrk = function () { p.queue.push(Array.prototype.slice.call(arguments)); };
        p.queue = []; p.version = "3.0";
        load("https://s.pinimg.com/ct/core.js");
      }
      w.pintrk("load", t.account);
      w.pintrk("page");
    },
    send: function (t, ev, ec) {
      var n = this.names[ev]; if (!n) return false;
      var p = Object.assign({ order_quantity: count(ec), line_items: items(ec).map(function (i) { return { product_id: i.item_id, product_name: i.item_name, product_quantity: i.quantity || 1, product_price: i.price }; }) }, money(ec));
      if (ec && ec.transaction_id) { p.order_id = ec.transaction_id; p.event_id = ec.transaction_id; }
      w.pintrk("track", n, p);
    }
  },
  script: {
    start: function (t) { if (fires(t, "page_view")) load(t.src); else return "espera su evento"; },
    send: function (t, ev) { if (!fires(t, ev)) return false; load(t.src); }
  },
  code: {
    start: function (t) { run(t, "page_view", null); },
    send: function (t, ev, ec) { if (!fires(t, ev)) return false; run(t, ev, ec); }
  }
};

// The store's own code: its file defines apexxCode[id]; it runs once loaded.
var pending = {};
var run = function (t, ev, ec) {
  if (ev === "page_view" && !fires(t, "page_view")) return;
  var call = function () {
    var fn = w.apexxCode && w.apexxCode[t.id];
    if (!fn) return;
    try { fn({ event: ev, ecommerce: ec, page: loc.pathname, store: tenant }); note(t.name, "código ejecutado en " + ev); }
    catch (e) { note(t.name, "su código falló: " + (e && e.message)); }
  };
  if (w.apexxCode && w.apexxCode[t.id]) return call();
  (pending[t.id] = pending[t.id] || []).push(call);
  if (pending[t.id].length > 1) return;
  var s = d.createElement("script"); s.async = true; s.src = t.code;
  s.onload = function () { (pending[t.id] || []).forEach(function (f) { f(); }); pending[t.id] = []; };
  s.onerror = function () { note(t.name, "no se pudo cargar su código"); };
  d.head.appendChild(s);
};

// ---------- start and deliver ----------
var started = {};
var startTag = function (t) {
  if (started[t.id]) return true;
  var p = P[t.kind];
  if (!p) return false;
  if (!urlOk(t)) { note(t.name, "no aplica en esta dirección"); return false; }
  if (!allowed(t)) { note(t.name, "espera el permiso de " + (t.cat === "analytics" ? "analítica" : "publicidad")); return false; }
  started[t.id] = true;
  try { var r = p.start(t); note(t.name, r || "cargada"); } catch (e) { note(t.name, "falló al cargar: " + (e && e.message)); }
  return true;
};
var deliver = function (t, ev, ec) {
  try { if (P[t.kind].send(t, ev, ec) !== false) note(t.name, "recibió " + ev + (ec && ec.value != null ? " (" + ec.value + " " + (ec.currency || "") + ")" : "")); }
  catch (e) { note(t.name, "falló con " + ev + ": " + (e && e.message)); }
};
var sent = {}; // tag -> events already delivered (by position in the queue)
var flush = function () {
  tags.forEach(function (t) {
    if (!startTag(t)) return;
    var from = sent[t.id] || 0;
    for (var i = from; i < queue.length; i++) deliver(t, queue[i].event, queue[i].ecommerce);
    sent[t.id] = queue.length;
  });
};
w.addEventListener("apexx:track", flush);

// ---------- banner ----------
var banner = null;
var show = function () {
  if (banner) banner.remove();
  var c = consent || { analytics: false, marketing: false };
  var has = function (k) { return tags.some(function (t) { return t.cat === k; }); };
  banner = d.createElement("div"); banner.className = "consent"; banner.setAttribute("role", "dialog"); banner.setAttribute("aria-label", "Cookies y privacidad"); banner.dataset.consent = "";
  var p = d.createElement("p"); p.textContent = C.banner || "Usamos cookies y herramientas de terceros para medir visitas y mostrarte anuncios. Tú decides."; banner.appendChild(p);
  var det = d.createElement("details"), sum = d.createElement("summary"); sum.textContent = "Configurar"; det.appendChild(sum);
  [["analytics", "Analítica"], ["marketing", "Publicidad"]].forEach(function (k) {
    if (!has(k[0])) return;
    var l = d.createElement("label"), i = d.createElement("input"); i.type = "checkbox"; i.dataset.c = k[0]; i.checked = !!c[k[0]];
    l.appendChild(i); l.appendChild(d.createTextNode(" " + k[1])); det.appendChild(l);
  });
  var save = d.createElement("button"); save.type = "button"; save.className = "btn btn-ghost"; save.dataset.consentSave = ""; save.textContent = "Guardar"; det.appendChild(save);
  banner.appendChild(det);
  var acts = d.createElement("div"); acts.className = "consent-actions";
  var none = d.createElement("button"); none.type = "button"; none.className = "btn btn-ghost"; none.dataset.consentNone = ""; none.textContent = "Solo necesarias";
  var all = d.createElement("button"); all.type = "button"; all.className = "btn"; all.dataset.consentAll = ""; all.textContent = "Aceptar todo";
  acts.appendChild(none); acts.appendChild(all); banner.appendChild(acts);
  b.appendChild(banner);
  banner.addEventListener("click", function (e) {
    var x = e.target.closest("button"); if (!x) return;
    var n;
    if (x.hasAttribute("data-consent-all")) n = { analytics: true, marketing: true };
    else if (x.hasAttribute("data-consent-none")) n = { analytics: false, marketing: false };
    else if (x.hasAttribute("data-consent-save")) n = { analytics: !!banner.querySelector("[data-c=analytics]:checked"), marketing: !!banner.querySelector("[data-c=marketing]:checked") };
    if (!n) return;
    var before = consent;
    consent = n; writeConsent(n); banner.remove(); banner = null;
    // Taking consent back only takes effect on a fresh page.
    if (before && ((before.analytics && !n.analytics) || (before.marketing && !n.marketing))) { loc.reload(); return; }
    flush(); // what the shopper just allowed starts now, with the events of this page
  });
};
var optional = tags.some(function (t) { return t.cat !== "necessary"; });
if (optional && !consent) show();
if (optional) {
  var foot = d.querySelector("footer") || b;
  var a = d.createElement("button"); a.type = "button"; a.className = "linkbtn consent-open"; a.textContent = "Preferencias de cookies"; a.addEventListener("click", show);
  foot.appendChild(a);
}
w.apexxTags = { version: C.v, log: log, consent: function () { return consent; } };
flush();
