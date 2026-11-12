import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Download } from "lucide-react";
import { errorMessage, request, type Me } from "@/api";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { toast } from "@/components/ui/sonner";
import { parsePesos } from "@/money";
import { can } from "@/perms";
import { download } from "../catalog/api";
import { Section, SubPage } from "./kit";

interface Job { id: string; kind: string; channel?: string; status: string; total: number; processed: number; failed: number; errors: { line: number; sku?: string; message: string }[]; createdAt: string; finishedAt?: string }
const STATUS: Record<string, string> = { queued: "En cola", running: "Aplicando", done: "Terminada", failed: "Se detuvo" };

/** Bulk changes: paste thousands of stock or price lines, follow the progress, and export the catalog. */
export function Bulk({ me }: { me: Me }) {
  const qc = useQueryClient();
  const canStock = can(me, "inventory", "edit"), canPrices = can(me, "pricing", "edit");
  const [kind, setKind] = useState<"stock" | "prices">("stock");
  const [text, setText] = useState("");
  const [channel, setChannel] = useState("mx");
  const [busy, setBusy] = useState(false);
  const [open, setOpen] = useState("");
  const jobs = useQuery({ queryKey: ["bulk-jobs"], queryFn: () => request<{ items: Job[] }>("GET", "/v1/bulk-operations").then((r) => r.items), refetchInterval: (q) => ((q.state.data ?? []).some((j) => j.status === "queued" || j.status === "running") ? 1000 : false) });
  const detail = useQuery({ queryKey: ["bulk-job", open], queryFn: () => request<Job>("GET", `/v1/bulk-operations/${open}`), enabled: !!open });
  const lines = text.split(/\r?\n/).map((l) => l.trim()).filter((l) => l && !/^sku[,;\t]/i.test(l));
  async function run() {
    const items = lines.map((l) => {
      const [sku, a, b] = l.split(/[,;\t]/).map((x) => x.trim());
      if (kind === "stock") return { sku, location: a, onHand: Number(b) };
      const amount = parsePesos(a ?? "") ?? -1, cmp = b ? parsePesos(b) ?? undefined : undefined;
      return { sku, amount, compareAt: cmp };
    });
    setBusy(true);
    try {
      const j = await request<Job>("POST", "/v1/bulk-operations", { kind, channel: kind === "prices" ? channel : undefined, items });
      toast.success(`${j.total} líneas recibidas${j.failed ? `; ${j.failed} con error se saltan` : ""}.`);
      setText(""); setOpen(j.id); void qc.invalidateQueries({ queryKey: ["bulk-jobs"] });
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <SubPage title="Cambios masivos" sub="Actualiza miles de existencias o precios de una vez. Las líneas con error se reportan y se saltan; el resto se aplica en bloques y puedes seguir el avance."
      actions={<Button variant="outline" onClick={() => void download("/v1/bulk/products.jsonl", "productos.jsonl").catch((e) => toast.error(errorMessage(e)))}><Download className="size-4" /> Exportar catálogo (JSONL)</Button>}>
      <Section title="Nueva operación" description={kind === "stock" ? "Una línea por SKU: sku, ubicación, existencias. Ejemplo: QA-00001-1, cdmx-1, 25" : "Una línea por SKU: sku, precio, precio antes (opcional). Ejemplo: QA-00001-1, 199.00, 249.00"}>
        <div className="flex flex-wrap gap-4 text-sm">
          <label className="flex items-center gap-2"><input type="radio" name="bk" checked={kind === "stock"} disabled={!canStock} onChange={() => setKind("stock")} /> Existencias</label>
          <label className="flex items-center gap-2"><input type="radio" name="bk" checked={kind === "prices"} disabled={!canPrices} onChange={() => setKind("prices")} /> Precios</label>
          {kind === "prices" && <label className="flex items-center gap-2">Canal <input aria-label="Canal" className="h-8 w-24 rounded-md border bg-background px-2 font-mono" value={channel} onChange={(e) => setChannel(e.target.value.trim())} /></label>}
        </div>
        <div className="grid gap-1.5"><Label htmlFor="bk-text">Líneas (pega desde tu hoja de cálculo)</Label><Textarea id="bk-text" rows={8} className="font-mono text-xs" value={text} onChange={(e) => setText(e.target.value)} placeholder={kind === "stock" ? "sku,ubicacion,existencias" : "sku,precio,precio_antes"} /></div>
        <div className="flex items-center gap-3"><Button disabled={busy || !lines.length || lines.length > 20000} onClick={() => void run()}>{busy ? "Enviando…" : `Aplicar ${lines.length} líneas`}</Button>{lines.length > 20000 && <span className="text-sm text-bad">Máximo 20,000 líneas por operación.</span>}</div>
      </Section>
      <Section title="Operaciones recientes">
        {(jobs.data ?? []).length === 0 && <p className="text-sm text-muted-foreground">Aún no hay operaciones.</p>}
        <div className="grid gap-2" data-bulk-jobs>
          {(jobs.data ?? []).map((j) => (
            <div key={j.id} className="grid gap-1 rounded-md border p-3 text-sm">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <span><strong>{j.kind === "stock" ? "Existencias" : `Precios (${j.channel})`}</strong> · {STATUS[j.status] ?? j.status} · {j.processed} de {j.total}{j.failed ? ` · ${j.failed} con error` : ""} · {new Date(j.createdAt).toLocaleString("es-MX", { dateStyle: "short", timeStyle: "short" })}</span>
                {j.failed > 0 && <Button size="sm" variant="ghost" onClick={() => setOpen(open === j.id ? "" : j.id)}>{open === j.id ? "Ocultar errores" : "Ver errores"}</Button>}
              </div>
              <div className="h-1.5 overflow-hidden rounded bg-muted" role="progressbar" aria-valuemin={0} aria-valuemax={j.total} aria-valuenow={j.processed}><div className="h-full bg-primary transition-all" style={{ width: `${(100 * j.processed) / Math.max(j.total, 1)}%` }} /></div>
              {open === j.id && detail.data && <ul className="text-xs text-bad">{detail.data.errors.map((e, i) => <li key={i}>Línea {e.line}{e.sku ? ` (${e.sku})` : ""}: {e.message}</li>)}</ul>}
            </div>
          ))}
        </div>
      </Section>
    </SubPage>
  );
}
