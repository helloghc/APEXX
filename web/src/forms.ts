/** Client-side validation mirrors the API rules so people see problems before saving. */

export function validateProduct(p: {
  id: string;
  handle: string;
  title: string;
  variants: { sku: string }[] | null;
}): Record<string, string> {
  const e: Record<string, string> = {};
  if (!p.id.trim()) e.id = "Escribe un identificador (por ejemplo P-1001).";
  else if (/[\s/]/.test(p.id)) e.id = "El identificador no puede tener espacios ni “/”.";
  if (!p.title.trim()) e.title = "Escribe el nombre del producto.";
  if (!p.handle.trim()) e.handle = "Escribe la dirección web del producto.";
  else if (!/^[a-z0-9]+(-[a-z0-9]+)*$/.test(p.handle)) e.handle = "Usa minúsculas, números y guiones (por ejemplo tenis-rojos).";
  const skus = (p.variants ?? []).map((v) => v.sku.trim());
  if (skus.length === 0) e.variants = "Agrega al menos una variante con su SKU.";
  else if (skus.some((s) => !s)) e.variants = "Cada variante necesita un SKU.";
  else if (new Set(skus).size !== skus.length) e.variants = "Hay SKUs repetidos.";
  return e;
}

export function validateMember(m: { email: string; name: string; password: string }): Record<string, string> {
  const e: Record<string, string> = {};
  if (!/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(m.email.trim())) e.email = "Escribe un correo válido.";
  if (m.password.length < 12) e.password = "La contraseña temporal debe tener al menos 12 caracteres.";
  return e;
}

/** validateWebhookUrl returns an error message, or null when the URL is acceptable. */
export function validateWebhookUrl(url: string): string | null {
  let u: URL;
  try {
    u = new URL(url);
  } catch {
    return "Escribe una URL completa, por ejemplo https://hooks.mi-erp.com/apexx.";
  }
  if (u.protocol !== "https:") return "La URL debe usar https.";
  return null;
}

export function slugify(s: string): string {
  return s
    .normalize("NFD")
    .replace(/[̀-ͯ]/g, "")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
}
