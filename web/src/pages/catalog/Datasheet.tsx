import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useRef } from "react";
import { FileText } from "lucide-react";
import { errorMessage, request } from "@/api";
import { pq } from "./productQueries";
import { Button } from "@/components/ui/button";
import { toast } from "@/components/ui/sonner";
import { send } from "./api";

interface Sheet { file: string; text: string; lines: number; at: string }

/** Spec sheet: the merchant uploads the manufacturer's PDF; its text shows
 * on the product page and answers shoppers' questions. */
export function Datasheet({ productId, canEdit }: { productId: string; canEdit: boolean }) {
  const qc = useQueryClient();
  const input = useRef<HTMLInputElement>(null);
  const q = useQuery(pq.datasheet<Sheet>(productId));
  const up = useMutation({
    mutationFn: (f: File) => { const fd = new FormData(); fd.append("file", f); return send<Sheet>(`/v1/products/${encodeURIComponent(productId)}/datasheet`, fd, undefined, "PUT"); },
    onSuccess: (s) => { toast.success(`Ficha técnica leída: ${s.lines} renglones. Ya aparece en la tienda.`); void qc.invalidateQueries({ queryKey: ["datasheet", productId] }); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const del = useMutation({
    mutationFn: () => request("DELETE", `/v1/products/${encodeURIComponent(productId)}/datasheet`, undefined, { as: "none" }),
    onSuccess: () => { toast.success("Ficha técnica quitada."); void qc.invalidateQueries({ queryKey: ["datasheet", productId] }); },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const s = q.data;
  return (
    <div className="grid gap-2 text-sm" data-datasheet>
      {s ? <div className="flex flex-wrap items-center gap-2"><FileText className="size-4" aria-hidden="true" /><span className="font-medium">{s.file}</span><span className="text-muted-foreground">· {s.lines} renglones leídos</span></div>
        : <p className="text-muted-foreground">Sin ficha técnica. Sube el PDF del fabricante: leemos su texto, lo mostramos en la ficha y responde las preguntas de tus clientes. Solo se guarda el texto.</p>}
      {s && <details><summary className="cursor-pointer text-primary">Ver el texto leído</summary><pre className="mt-2 max-h-60 overflow-auto whitespace-pre-wrap rounded-md border bg-muted/40 p-2 text-xs">{s.text}</pre></details>}
      {canEdit && <div className="flex flex-wrap gap-2">
        <input ref={input} type="file" accept="application/pdf" className="sr-only" id="datasheet-file" tabIndex={-1} aria-hidden="true" onChange={(e) => { const f = e.target.files?.[0]; if (f) up.mutate(f); e.target.value = ""; }} />
        <Button type="button" variant="outline" size="sm" disabled={up.isPending} onClick={() => input.current?.click()}>{up.isPending ? "Leyendo el PDF…" : s ? "Reemplazar PDF" : "Subir PDF"}</Button>
        {s && <Button type="button" variant="outline" size="sm" disabled={del.isPending} onClick={() => del.mutate()}>Quitar</Button>}
      </div>}
    </div>
  );
}
