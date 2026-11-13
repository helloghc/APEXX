// Apexx tags runtime: wrapped as (function(C){ … })(config) in /s/{store}/api/tags.js.
// C = { v: version, banner: text, tags: [{id, name, kind, account, params, src, code, cat, fire, url}] }
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
// In the store's sandbox every page and event is marked, so analytics can
// leave the traffic out: ?apexx_env=sandbox in the address, apexx_env in the
// dataLayer and traffic_type "internal" for Google Analytics.
var SBX = C.env === "sandbox";
if (SBX) {
  try {
    var su = new URL(loc.href);
    if (su.searchParams.get("apexx_env") !== "sandbox") { su.searchParams.set("apexx_env", "sandbox"); history.replaceState(history.state, "", su.toString()); }
  } catch (e) {}
  w.dataLayer.push({ apexx_env: "sandbox" });
}
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
// The store's consent mode:
//   notice     tells the visitor and measures from the first page; the visitor can turn things off.
//   analytics  analytics without asking; advertising waits for permission.
//   opt_in     everything optional waits for permission (the EU and the UK ask for this).
// With Global Privacy Control, advertising stays off until the visitor turns it on (US state laws require it).
var MODE = C.mode || "notice";
var gpc = !!navigator.globalPrivacyControl;
var stored = readConsent();
var defaults = MODE === "opt_in" ? { analytics: false, marketing: false } : MODE === "analytics" ? { analytics: true, marketing: false } : { analytics: true, marketing: !gpc };
var consent = stored || defaults;
var allowed = function (t) { return t.cat === "necessary" || !!consent[t.cat]; };
// Google consent mode v2: Google tags know what the visitor allows, before and after a change.
var gconsent = function () {
  var a = consent.analytics ? "granted" : "denied", m = consent.marketing ? "granted" : "denied";
  return { analytics_storage: a, ad_storage: m, ad_user_data: m, ad_personalization: m };
};

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
  if (!w.gtag) { w.gtag = function () { w.dataLayer.push(arguments); }; w.gtag("consent", "default", gconsent()); w.gtag("js", new Date()); }
};
var param = function (t, k) { return (t.params && t.params[k]) || ""; };
var total = function (ec) { return ec && ec.value != null ? ec.value : undefined; };
var items = function (ec) { return (ec && ec.items) || []; };
var ids = function (ec) { return items(ec).map(function (i) { return i.item_id; }); };
var count = function (ec) { return items(ec).reduce(function (a, i) { return a + (i.quantity || 1); }, 0); };
var money = function (ec) { var o = {}; if (ec && ec.value != null) o.value = ec.value; if (ec && ec.currency) o.currency = ec.currency; return o; };

// Each provider: start (loads it and counts the page view) and send (one event).
var P = {
  gtm: {
    start: function (t) {
      if (!w.gtag) { w.gtag = function () { w.dataLayer.push(arguments); }; w.gtag("consent", "default", gconsent()); }
      w.dataLayer.push({ "gtm.start": Date.now(), event: "gtm.js" });
      load("https://www.googletagmanager.com/gtm.js?id=" + enc(t.account));
    },
    send: function () { /* the container reads the dataLayer itself */ }
  },
  ga4: {
    start: function (t) { gtagReady(); load("https://www.googletagmanager.com/gtag/js?id=" + enc(t.account)); w.gtag("config", t.account, SBX ? { traffic_type: "internal", apexx_env: "sandbox" } : {}); },
    send: function (t, ev, ec) { if (ev !== "page_view") w.gtag("event", ev, Object.assign({ send_to: t.account }, SBX ? { traffic_type: "internal", apexx_env: "sandbox" } : {}, ec || {})); }
  },
  gads: {
    start: function (t) { gtagReady(); load("https://www.googletagmanager.com/gtag/js?id=" + enc(t.account)); w.gtag("config", t.account); },
    send: function (t, ev, ec) {
      if (!fires(t, ev)) return false;
      var label = (t.params && t.params.label) || "";
      var p = Object.assign({ send_to: t.account + (label ? "/" + label : "") }, money(ec));
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
  // ---------- Adobe Experience Platform Tags + Adobe Client Data Layer (XDM commerce) ----------
  adobe: {
    names: { view_item: "commerce.productViews", add_to_cart: "commerce.productListAdds", view_cart: "commerce.productListViews", begin_checkout: "commerce.checkouts", purchase: "commerce.purchases" },
    start: function (t) {
      w.adobeDataLayer = w.adobeDataLayer || [];
      w.adobeDataLayer.push({ event: "web.webpagedetails.pageViews", xdm: { eventType: "web.webpagedetails.pageViews", web: { webPageDetails: { URL: loc.href, name: d.title, pageViews: { value: 1 } } } } });
      load(t.account);
    },
    send: function (t, ev, ec) {
      var n = this.names[ev]; if (!n) return false;
      var key = n.split(".")[1], xdm = { eventType: n, commerce: {}, productListItems: items(ec).map(function (i) { return { SKU: i.item_id, name: i.item_name, quantity: i.quantity || 1, priceTotal: i.price != null ? i.price * (i.quantity || 1) : undefined, currencyCode: ec.currency }; }) };
      xdm.commerce[key] = { value: 1 };
      if (ev === "purchase") xdm.commerce.order = { purchaseID: ec.transaction_id, priceTotal: ec.value, currencyCode: ec.currency };
      w.adobeDataLayer.push({ event: n, xdm: xdm, ecommerce: ec });
    }
  },
  // ---------- Snap Pixel ----------
  snapchat: {
    names: { view_item: "VIEW_CONTENT", add_to_cart: "ADD_CART", begin_checkout: "START_CHECKOUT", purchase: "PURCHASE" },
    start: function (t) {
      if (!w.snaptr) {
        var a = w.snaptr = function () { a.handleRequest ? a.handleRequest.apply(a, arguments) : a.queue.push(arguments); };
        a.queue = [];
        load("https://sc-static.net/scevent.min.js");
      }
      w.snaptr("init", t.account, {});
      w.snaptr("track", "PAGE_VIEW");
    },
    send: function (t, ev, ec) {
      var n = this.names[ev]; if (!n) return false;
      var p = { item_ids: ids(ec), number_items: count(ec) };
      if (total(ec) != null) p.price = total(ec);
      if (ec && ec.currency) p.currency = ec.currency;
      if (ec && ec.transaction_id) { p.transaction_id = ec.transaction_id; p.client_dedup_id = ec.transaction_id; }
      w.snaptr("track", n, p);
    }
  },
  // ---------- X (Twitter) pixel ----------
  x: {
    start: function (t) {
      if (!w.twq) {
        var e = w.twq = function () { e.exe ? e.exe.apply(e, arguments) : e.queue.push(arguments); };
        e.version = "1.1"; e.queue = [];
        load("https://static.ads-twitter.com/uwt.js");
      }
      w.twq("config", t.account);
    },
    send: function (t, ev, ec) {
      var id = param(t, "ev_" + ev); if (!id) return false;
      var p = { contents: items(ec).map(function (i) { return { content_id: i.item_id, content_name: i.item_name, content_price: i.price, num_items: i.quantity || 1 }; }) };
      if (total(ec) != null) p.value = total(ec);
      if (ec && ec.currency) p.currency = ec.currency;
      if (ec && ec.transaction_id) p.conversion_id = ec.transaction_id;
      w.twq("event", id, p);
    }
  },
  // ---------- LinkedIn Insight Tag ----------
  linkedin: {
    start: function (t) {
      w._linkedin_partner_id = t.account;
      w._linkedin_data_partner_ids = w._linkedin_data_partner_ids || [];
      if (w._linkedin_data_partner_ids.indexOf(t.account) < 0) w._linkedin_data_partner_ids.push(t.account);
      if (!w.lintrk) { var l = w.lintrk = function (a, b) { l.q.push([a, b]); }; l.q = []; }
      load("https://snap.licdn.com/li.lms-analytics/insight.min.js");
    },
    send: function (t, ev, ec) {
      var id = param(t, "conv_" + ev); if (!id) return false;
      var p = { conversion_id: Number(id) };
      if (total(ec) != null) p.conversion_value = total(ec);
      if (ec && ec.currency) p.currency = ec.currency;
      if (ec && ec.transaction_id) p.event_id = ec.transaction_id;
      w.lintrk("track", p);
    }
  },
  // ---------- Microsoft Advertising UET ----------
  microsoft: {
    pages: { view_item: "product", add_to_cart: "cart", view_cart: "cart", begin_checkout: "cart", purchase: "purchase" },
    start: function (t) {
      w.uetq = w.uetq || [];
      if (!w.UET) {
        var s = d.createElement("script"); s.async = true; s.src = "https://bat.bing.com/bat.js";
        s.onload = function () { var o = { ti: t.account, enableAutoSpaTracking: true }; o.q = w.uetq; w.uetq = new w.UET(o); w.uetq.push("pageLoad"); };
        d.head.appendChild(s);
      }
    },
    send: function (t, ev, ec) {
      var pt = this.pages[ev]; if (!pt) return false;
      var p = { ecomm_prodid: ids(ec), ecomm_pagetype: pt };
      if (total(ec) != null) { p.revenue_value = total(ec); p.ecomm_totalvalue = total(ec); }
      if (ec && ec.currency) p.currency = ec.currency;
      if (ec && ec.transaction_id) p.transaction_id = ec.transaction_id;
      w.uetq.push("event", ev, p);
    }
  },
  // ---------- Reddit Pixel ----------
  reddit: {
    names: { view_item: "ViewContent", add_to_cart: "AddToCart", purchase: "Purchase" },
    start: function (t) {
      if (!w.rdt) {
        var p = w.rdt = function () { p.sendEvent ? p.sendEvent.apply(p, arguments) : p.callQueue.push(arguments); };
        p.callQueue = [];
        load("https://www.redditstatic.com/ads/pixel.js");
      }
      w.rdt("init", t.account);
      w.rdt("track", "PageVisit");
    },
    send: function (t, ev, ec) {
      var n = this.names[ev]; if (!n) return false;
      var p = { itemCount: count(ec), products: items(ec).map(function (i) { return { id: i.item_id, name: i.item_name }; }) };
      if (total(ec) != null) p.value = total(ec);
      if (ec && ec.currency) p.currency = ec.currency;
      if (ec && ec.transaction_id) p.conversionId = ec.transaction_id;
      w.rdt("track", n, p);
    }
  },
  // ---------- heat maps and session recordings ----------
  hotjar: {
    start: function (t) {
      w.hj = w.hj || function () { (w.hj.q = w.hj.q || []).push(arguments); };
      w._hjSettings = { hjid: Number(t.account), hjsv: 6 };
      load("https://static.hotjar.com/c/hotjar-" + enc(t.account) + ".js?sv=6");
    },
    send: function (t, ev) { w.hj("event", ev); }
  },
  clarity: {
    start: function (t) {
      w.clarity = w.clarity || function () { (w.clarity.q = w.clarity.q || []).push(arguments); };
      load("https://www.clarity.ms/tag/" + enc(t.account));
    },
    send: function (t, ev) { w.clarity("event", ev); }
  },
  crazyegg: {
    start: function (t) { load("https://script.crazyegg.com/pages/scripts/" + t.account.slice(0, 4) + "/" + t.account.slice(4) + ".js"); },
    send: function () { return false; }
  },
  // ---------- chat ----------
  whatsapp: {
    start: function (t) {
      var msg = param(t, "message");
      var item = queue.filter(function (q) { return q.event === "view_item"; })[0];
      if (item && item.ecommerce && item.ecommerce.items && item.ecommerce.items[0]) msg = (msg ? msg + " " : "") + "(" + item.ecommerce.items[0].item_name + ")";
      var a = d.createElement("a");
      a.className = "wa-float" + (param(t, "position") === "left" ? " wa-left" : "");
      a.href = "https://wa.me/" + t.account + (msg ? "?text=" + enc(msg) : "");
      a.target = "_blank"; a.rel = "noopener";
      a.setAttribute("aria-label", "Escríbenos por WhatsApp");
      a.innerHTML = "<svg viewBox='0 0 32 32' width='26' height='26' aria-hidden='true'><path fill='currentColor' d='M16 3a13 13 0 0 0-11.2 19.6L3 29l6.6-1.7A13 13 0 1 0 16 3zm0 23.7a10.7 10.7 0 0 1-5.5-1.5l-.4-.2-3.9 1 1-3.8-.3-.4A10.7 10.7 0 1 1 16 26.7zm5.9-8c-.3-.2-1.9-.9-2.2-1s-.5-.2-.7.2-.8 1-1 1.2-.4.2-.7.1a8.8 8.8 0 0 1-4.4-3.8c-.3-.6.3-.5.9-1.7.1-.2 0-.4 0-.5l-1-2.4c-.3-.6-.5-.5-.7-.5h-.6a1.2 1.2 0 0 0-.9.4 3.7 3.7 0 0 0-1.1 2.7 6.5 6.5 0 0 0 1.3 3.4 14.6 14.6 0 0 0 5.6 5c2.1.9 2.9 1 4 .8a3.4 3.4 0 0 0 2.2-1.6 2.8 2.8 0 0 0 .2-1.6c-.1-.1-.3-.2-.7-.4z'/></svg>";
      var text = param(t, "text");
      if (text) { var sp = d.createElement("span"); sp.textContent = text; a.appendChild(sp); }
      b.appendChild(a);
      return "botón visible";
    },
    send: function () { return false; }
  },
  tawk: {
    start: function (t) { w.Tawk_API = w.Tawk_API || {}; w.Tawk_LoadStart = new Date(); load("https://embed.tawk.to/" + enc(t.account) + "/" + enc(param(t, "widget") || "default")); },
    send: function () { return false; }
  },
  crisp: {
    start: function (t) { w.$crisp = w.$crisp || []; w.CRISP_WEBSITE_ID = t.account; load("https://client.crisp.chat/l.js"); },
    send: function () { return false; }
  },
  tidio: {
    start: function (t) { load("https://code.tidio.co/" + enc(t.account) + ".js"); },
    send: function () { return false; }
  },
  intercom: {
    start: function (t) {
      w.intercomSettings = Object.assign(w.intercomSettings || {}, { api_base: "https://api-iam.intercom.io", app_id: t.account });
      if (!w.Intercom) { var i = function () { i.c(arguments); }; i.q = []; i.c = function (args) { i.q.push(args); }; w.Intercom = i; }
      load("https://widget.intercom.io/widget/" + enc(t.account));
    },
    send: function () { return false; }
  },
  zendesk: {
    start: function (t) {
      if (d.getElementById("ze-snippet")) return;
      var s = d.createElement("script"); s.id = "ze-snippet"; s.async = true; s.src = "https://static.zdassets.com/ekr/snippet.js?key=" + enc(t.account); d.head.appendChild(s);
    },
    send: function () { return false; }
  },
  hubspot: {
    start: function (t) {
      if (d.getElementById("hs-script-loader")) return;
      var s = d.createElement("script"); s.id = "hs-script-loader"; s.async = true; s.defer = true; s.src = "https://js.hs-scripts.com/" + enc(t.account) + ".js"; d.head.appendChild(s);
    },
    send: function () { return false; }
  },
  trengo: {
    start: function (t) { w.Trengo = w.Trengo || {}; w.Trengo.key = t.account; load("https://static.widget.trengo.eu/embed.js"); },
    send: function () { return false; }
  },
  jivo: {
    start: function (t) { load("https://code.jivosite.com/widget/" + enc(t.account)); },
    send: function () { return false; }
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
  var c = consent;
  var has = function (k) { return tags.some(function (t) { return t.cat === k; }); };
  banner = d.createElement("div"); banner.className = "consent"; banner.setAttribute("role", "dialog"); banner.setAttribute("aria-label", "Cookies y privacidad"); banner.dataset.consent = "";
  var p = d.createElement("p"); p.textContent = C.banner || (MODE === "notice" ? "Usamos cookies y herramientas de terceros para medir visitas y mostrarte anuncios. Puedes cambiarlo cuando quieras en «Preferencias de cookies»." : "Usamos cookies y herramientas de terceros para medir visitas y mostrarte anuncios. Tú decides."); banner.appendChild(p);
  var det = d.createElement("details"), sum = d.createElement("summary"); sum.textContent = "Configurar"; det.appendChild(sum);
  [["analytics", "Analítica"], ["marketing", "Publicidad"]].forEach(function (k) {
    if (!has(k[0])) return;
    var l = d.createElement("label"), i = d.createElement("input"); i.type = "checkbox"; i.dataset.c = k[0]; i.checked = !!c[k[0]];
    l.appendChild(i); l.appendChild(d.createTextNode(" " + k[1])); det.appendChild(l);
  });
  var save = d.createElement("button"); save.type = "button"; save.className = "btn btn-ghost"; save.dataset.consentSave = ""; save.textContent = "Guardar"; det.appendChild(save);
  banner.appendChild(det);
  var acts = d.createElement("div"); acts.className = "consent-actions";
  if (MODE === "notice" && !stored) {
    // An informative notice: what is on stays on; "Configurar" turns things off.
    var ok = d.createElement("button"); ok.type = "button"; ok.className = "btn"; ok.dataset.consentOk = ""; ok.textContent = "Entendido";
    acts.appendChild(ok);
  } else {
    var none = d.createElement("button"); none.type = "button"; none.className = "btn btn-ghost"; none.dataset.consentNone = ""; none.textContent = "Solo necesarias";
    var all = d.createElement("button"); all.type = "button"; all.className = "btn"; all.dataset.consentAll = ""; all.textContent = "Aceptar todo";
    acts.appendChild(none); acts.appendChild(all);
  }
  banner.appendChild(acts);
  b.appendChild(banner);
  banner.addEventListener("click", function (e) {
    var x = e.target.closest("button"); if (!x) return;
    var n;
    if (x.hasAttribute("data-consent-ok")) n = { analytics: consent.analytics, marketing: consent.marketing };
    else if (x.hasAttribute("data-consent-all")) n = { analytics: true, marketing: true };
    else if (x.hasAttribute("data-consent-none")) n = { analytics: false, marketing: false };
    else if (x.hasAttribute("data-consent-save")) n = { analytics: !!banner.querySelector("[data-c=analytics]:checked"), marketing: !!banner.querySelector("[data-c=marketing]:checked") };
    if (!n) return;
    var before = consent;
    consent = n; stored = n; writeConsent(n); banner.remove(); banner = null;
    if (w.gtag) w.gtag("consent", "update", gconsent());
    // Taking consent back only takes effect on a fresh page.
    if ((before.analytics && !n.analytics) || (before.marketing && !n.marketing)) { loc.reload(); return; }
    flush(); // what the shopper just allowed starts now, with the events of this page
  });
};
var optional = tags.some(function (t) { return t.cat !== "necessary"; });
var hasMarketing = tags.some(function (t) { return t.cat === "marketing"; });
// The banner: once per visitor, and only when there is something to tell or ask.
if (optional && !stored && (MODE !== "analytics" || hasMarketing)) show();
if (optional) {
  var foot = d.querySelector("footer") || b;
  var a = d.createElement("button"); a.type = "button"; a.className = "linkbtn consent-open"; a.textContent = "Preferencias de cookies"; a.addEventListener("click", show);
  foot.appendChild(a);
}
w.apexxTags = { version: C.v, mode: MODE, env: SBX ? "sandbox" : "production", log: log, consent: function () { return consent; } };
flush();
