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
