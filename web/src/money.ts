/**
 * Prices travel as integer minor units (centavos) with a currency code.
 * The UI lets people type pesos ("1,299.50", "1299,5", "$1299") and converts exactly,
 * without floating-point drift.
 */

/** parsePesos converts a user-typed amount into cents, or null if it is not a valid amount. */
export function parsePesos(input: string): number | null {
  let s = input.trim().replace(/^\$/, "").replace(/\s/g, "");
  if (s === "") return null;
  // A comma followed by exactly 1 or 2 digits at the end is a decimal separator ("1299,5").
  if (/,\d{1,2}$/.test(s) && !s.includes(".")) s = s.replace(/,(\d{1,2})$/, ".$1");
  s = s.replace(/,/g, "");
  if (!/^\d+(\.\d{0,2})?$/.test(s)) return null;
  const [whole = "0", frac = ""] = s.split(".");
  const cents = Number(whole) * 100 + Number((frac + "00").slice(0, 2));
  return Number.isSafeInteger(cents) ? cents : null;
}

/** centsToPesos renders cents as a plain editable string: 129950 → "1299.50". */
export function centsToPesos(cents: number): string {
  const neg = cents < 0;
  const a = Math.abs(Math.trunc(cents));
  return `${neg ? "-" : ""}${Math.floor(a / 100)}.${String(a % 100).padStart(2, "0")}`;
}

/** formatMoney renders cents for reading: 129950, "MXN" → "$1,299.50 MXN". */
export function formatMoney(cents: number, currency = "MXN"): string {
  const neg = cents < 0;
  const a = Math.abs(Math.trunc(cents));
  const whole = String(Math.floor(a / 100)).replace(/\B(?=(\d{3})+(?!\d))/g, ",");
  return `${neg ? "-" : ""}$${whole}.${String(a % 100).padStart(2, "0")} ${currency}`;
}

/** A value typed by a person that is not a valid number: the form says so instead of saving 0. */
export class InvalidNumber extends Error {}

/** readNumber reads "12.5", "12,5" or " 3 "; empty gives `empty`; anything else throws, naming the field. */
export function readNumber(text: string | number | null | undefined, field: string, empty = 0): number {
  const t = String(text ?? "").trim().replace(",", ".");
  if (t === "") return empty;
  if (!/^-?\d+(\.\d+)?$/.test(t)) throw new InvalidNumber(`Escribe un número válido en «${field}».`);
  return Number(t);
}

/** readPesos is parsePesos for a form: empty gives `empty`; a bad amount throws, naming the field. */
export function readPesos(text: string | null | undefined, field: string, empty = 0): number {
  const t = String(text ?? "").trim();
  if (t === "") return empty;
  const c = parsePesos(t);
  if (c === null) throw new InvalidNumber(`Escribe un monto válido en «${field}», por ejemplo 1299.50.`);
  return c;
}
