// Every form in the panel shows the same kind of error: a message under the
// field, in the panel's language, announced to screen readers and tied to the
// field with aria-describedby. The browser's own bubble (in the browser's
// language, different on every browser) is replaced.

function messageFor(el: HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement): string {
  const v = el.validity;
  if (v.valueMissing) return "Este campo es obligatorio";
  if (v.typeMismatch && el.type === "email") return "Escribe un correo válido.";
  if (v.typeMismatch && el.type === "url") return "Escribe una URL https completa.";
  if (v.badInput || (v.stepMismatch && el.type === "number")) return "Escribe un número válido.";
  if (v.rangeUnderflow || v.rangeOverflow) return "El número está fuera del rango permitido.";
  if (v.tooLong || v.tooShort) return "Revisa el largo del texto.";
  return "Revisa este dato.";
}

let focusedThisRound = false;

function show(el: HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement) {
  const id = (el.id || el.name || "campo") + "-autoerr";
  let p = document.getElementById(id);
  if (!p) {
    p = document.createElement("p");
    p.id = id;
    p.dataset.autoerr = "";
    p.setAttribute("role", "alert");
    p.className = "mt-1 text-sm text-bad";
    el.insertAdjacentElement("afterend", p);
  }
  p.textContent = messageFor(el);
  el.setAttribute("aria-invalid", "true");
  const by = (el.getAttribute("aria-describedby") || "").split(" ").filter(Boolean);
  if (!by.includes(id)) el.setAttribute("aria-describedby", [...by, id].join(" "));
}

function clear(el: Element) {
  if (!(el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement || el instanceof HTMLSelectElement)) return;
  if (!el.validity.valid) return;
  const id = (el.id || el.name || "campo") + "-autoerr";
  document.getElementById(id)?.remove();
  if (el.getAttribute("aria-invalid") === "true" && !el.dataset.serverInvalid) el.removeAttribute("aria-invalid");
}

export function startFieldErrors() {
  document.addEventListener(
    "invalid",
    (e) => {
      const el = e.target as HTMLInputElement;
      if (!el || !("validity" in el)) return;
      e.preventDefault();
      show(el);
      if (!focusedThisRound) {
        focusedThisRound = true;
        el.focus();
        setTimeout(() => (focusedThisRound = false), 0);
      }
    },
    true,
  );
  document.addEventListener("input", (e) => clear(e.target as Element), true);
  document.addEventListener("change", (e) => clear(e.target as Element), true);
}
