import { useMutation } from "@tanstack/react-query";
import { useRef, useState } from "react";
import { errorMessage, request } from "@/api";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Textarea } from "@/components/ui/textarea";
import { toast } from "@/components/ui/sonner";
import { navigate } from "@/router";
import { send } from "../catalog/api";

interface Match { sku: string; title: string; productId: string; price?: string; available: number; exact: boolean }
interface Line { text: string; qty: number; matches: Match[] }
interface Result { lines: Line[]; matched: number }

/** Quote from a request: the buyer's list (text or PDF) becomes lines with
 * the matching SKUs; the rep reviews each line and creates a draft order. */
export function FromRequest() {
  const [text, setText] = useState("");
  const [res, setRes] = useState<Result | null>(null);
  const [pick, setPick] = useState<Record<number, string>>({});
  const [qty, setQty] = useState<Record<number, number>>({});
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const file = useRef<HTMLInputElement>(null);
  const done = (r: Result) => {
    setRes(r);
    setPick(Object.fromEntries(r.lines.map((l, i) => [i, l.matches[0]?.sku ?? ""])));
    setQty(Object.fromEntries(r.lines.map((l, i) => [i, l.qty])));
  };
  const read = useMutation({
    mutationFn: (v: { text?: string; file?: File }) => {
      if (v.file) { const fd = new FormData(); fd.append("file", v.file); return send<Result>("/v1/ai/rfq", fd); }
      return request<Result>("POST", "/v1/ai/rfq", { text: v.text });
    },
    onSuccess: done,
    onError: (e) => toast.error(errorMessage(e)),
  });
  const draft = useMutation({
    mutationFn: () => {
      const items = (res?.lines ?? []).map((_, i) => ({ sku: pick[i] ?? "", qty: qty[i] ?? 0 })).filter((x) => x.sku && x.qty > 0);
      return request<{ id: string }>("POST", "/v1/draft-orders", { channel: "mx", items, customer: { email: email.trim(), name: name.trim() }, postalCode: "", shipping: 0, discount: 0, note: "Desde una solicitud de cotización" });
    },
    onSuccess: () => { toast.success("Pedido borrador creado. Revísalo, asigna al cliente y envía el enlace de pago."); navigate("pedidos", "borradores"); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const chosen = res ? res.lines.filter((_, i) => pick[i]).length : 0;
  return (
    <Card data-from-request>
      <CardHeader>
        <CardTitle className="text-base">Cotización desde una solicitud</CardTitle>
        <CardDescription>Pega la lista del cliente o sube su PDF. Cada renglón con cantidad se empareja con tu catálogo (primero por SKU, luego por nombre), con tu precio y existencias. Tú revisas cada renglón antes de crear el pedido borrador.</CardDescription>
      </CardHeader>
      <CardContent className="grid gap-3 text-sm">
        <label htmlFor="rfq-text" className="sr-only">Lista del cliente</label>
        <Textarea id="rfq-text" rows={5} value={text} onChange={(e) => setText(e.target.value)} placeholder={"20 pzas Taladro percutor 650W\nBROCA-10 x 50\nCasco de seguridad 100 piezas"} />
        <div className="flex flex-wrap gap-2">
          <Button type="button" disabled={read.isPending || text.trim().length < 3} onClick={() => read.mutate({ text })}>{read.isPending ? "Leyendo…" : "Emparejar con mi catálogo"}</Button>
          <input ref={file} type="file" accept="application/pdf" className="sr-only" id="rfq-file" onChange={(e) => { const f = e.target.files?.[0]; if (f) read.mutate({ file: f }); e.target.value = ""; }} />
          <Button type="button" variant="outline" disabled={read.isPending} onClick={() => file.current?.click()}>Subir PDF</Button>
        </div>
        {res && <div className="grid gap-2" role="status" aria-live="polite">
          <p><span className="tabular-nums">{res.matched}</span> de <span className="tabular-nums">{res.lines.length}</span> renglones con producto sugerido.</p>
          <ul className="grid gap-2">
            {res.lines.map((l, i) => (
              <li key={i} className="grid gap-2 rounded-md border p-3 sm:grid-cols-[1fr_auto]">
                <div className="grid min-w-0 gap-1">
                  <p className="break-words text-muted-foreground">«{l.text}»</p>
                  {l.matches.length === 0 ? <p>Sin coincidencias en tu catálogo.</p> : (
                    <>
                      <label className="sr-only" htmlFor={`rfq-pick-${i}`}>Producto para «{l.text}»</label>
                      <select id={`rfq-pick-${i}`} className="h-9 min-w-0 rounded-md border bg-background px-2" value={pick[i] ?? ""} onChange={(e) => setPick({ ...pick, [i]: e.target.value })}>
                        <option value="">No incluir</option>
                        {l.matches.map((m) => <option key={m.sku} value={m.sku}>{m.title} · {m.sku}{m.price ? ` · ${m.price}` : ""} · {m.available} disp.{m.exact ? " · SKU exacto" : ""}</option>)}
                      </select>
                    </>
                  )}
                </div>
                <label className="flex items-center gap-2">Cantidad <input type="number" min={1} max={999999} className="h-9 w-24 rounded-md border bg-background px-2 text-right tabular-nums" value={qty[i] ?? 1} onChange={(e) => setQty({ ...qty, [i]: Number(e.target.value) })} /></label>
              </li>
            ))}
          </ul>
          <div className="flex flex-wrap items-end gap-2">
            <label className="grid gap-1"><span>Correo del cliente</span><input type="email" required autoComplete="off" className="h-9 w-64 max-w-full rounded-md border bg-background px-2" value={email} onChange={(e) => setEmail(e.target.value)} /></label>
            <label className="grid gap-1"><span>Nombre o empresa</span><input type="text" className="h-9 w-56 max-w-full rounded-md border bg-background px-2" value={name} onChange={(e) => setName(e.target.value)} /></label>
            <Button type="button" disabled={draft.isPending || chosen === 0 || !email.includes("@")} onClick={() => draft.mutate()}>Crear pedido borrador ({chosen})</Button>
          </div>
        </div>}
      </CardContent>
    </Card>
  );
}
