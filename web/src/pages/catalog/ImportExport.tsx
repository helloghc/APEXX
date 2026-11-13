import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { CheckCircle2, Download, FileSpreadsheet, Upload } from "lucide-react";
import { errorMessage, type Me } from "@/api";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { toast } from "@/components/ui/sonner";
import { can } from "@/perms";
import { dateTime, ErrorBox, Notice } from "../common";
import { cat, download, type ImportJob } from "./api";
import { CatalogPage } from "./shared";

const STATUS: Record<ImportJob["status"], { label: string; variant: "secondary" | "outline" | "default" | "bad" | "ok" }> = {
  preview: { label: "En revisión", variant: "outline" },
  running: { label: "Importando", variant: "default" },
  done: { label: "Terminada", variant: "ok" },
  failed: { label: "Con errores", variant: "bad" },
};

export function ImportExport({ me }: { me: Me }) {
  const qc = useQueryClient();
  const canEdit = can(me, "catalog", "edit");
  const fileRef = useRef<HTMLInputElement>(null);
  const [job, setJob] = useState<ImportJob | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const history = useQuery({ queryKey: ["imports"], queryFn: cat.imports });

  // Follow a running import until it finishes.
  useEffect(() => {
    if (job?.status !== "running") return;
    const t = setInterval(() => {
      cat.importJob(job.id).then((j) => {
        setJob(j);
        if (j.status !== "running") {
          void qc.invalidateQueries({ queryKey: ["imports"] });
          void qc.invalidateQueries({ queryKey: ["products"] });
          void qc.invalidateQueries({ queryKey: ["catalog-summary"] });
        }
      }).catch(() => undefined);
    }, 800);
    return () => clearInterval(t);
  }, [job, qc]);

  async function pick(file: File | undefined) {
    if (!file) return;
    setBusy(true);
    setError(null);
    try {
      setJob(await cat.previewImport(file));
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
      if (fileRef.current) fileRef.current.value = "";
    }
  }
  async function apply() {
    if (!job) return;
    setBusy(true);
    try {
      setJob(await cat.applyImport(job.id));
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }
  const dl = (path: string, name: string) => void download(path, name).catch((e) => toast.error(errorMessage(e)));
  const s = job?.summary;

  return (
    <CatalogPage tab="importar" title="Importar y exportar" sub="Carga o actualiza cientos de productos con una hoja de cálculo. Antes de aplicar nada verás qué va a cambiar y qué filas tienen errores.">
      <div className="grid gap-5 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle className="text-base">Importar productos</CardTitle>
            <CardDescription>Archivo CSV de hasta 10 MB. Una fila por variante; las filas con la misma dirección forman un producto.</CardDescription>
          </CardHeader>
          <CardContent className="flex flex-wrap gap-2">
            <input ref={fileRef} type="file" accept=".csv,text/csv" className="sr-only" id="csv-file" tabIndex={-1} aria-hidden="true" onChange={(e) => void pick(e.target.files?.[0])} />
            <Button disabled={!canEdit || busy} onClick={() => fileRef.current?.click()}><Upload aria-hidden="true" /> {busy && !job ? "Revisando…" : "Elegir archivo CSV"}</Button>
            <Button variant="outline" onClick={() => dl("/v1/catalog/import-template.csv", "plantilla-productos.csv")}><FileSpreadsheet aria-hidden="true" /> Descargar plantilla</Button>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle className="text-base">Exportar catálogo</CardTitle>
            <CardDescription>Descarga todos los productos con variantes, precios y existencias. El archivo se puede editar y volver a importar.</CardDescription>
          </CardHeader>
          <CardContent>
            <Button variant="outline" onClick={() => dl("/v1/catalog/export.csv", "productos.csv")}><Download aria-hidden="true" /> Exportar todo a CSV</Button>
          </CardContent>
        </Card>
      </div>
      <ErrorBox error={error} />

      {job && s && (
        <Card>
          <CardHeader className="flex-row items-start justify-between gap-3 space-y-0">
            <div className="grid gap-1">
              <CardTitle className="text-base">{job.filename}</CardTitle>
              <CardDescription>{s.rows} filas · {s.products} productos · {s.variants} variantes</CardDescription>
            </div>
            <Badge variant={STATUS[job.status].variant}>{STATUS[job.status].label}</Badge>
          </CardHeader>
          <CardContent className="grid gap-4">
            <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
              {[["Se crean", s.creates], ["Se actualizan", s.updates], ["Con errores", s.invalid], ["Filas", s.rows]].map(([label, n]) => (
                <div key={label as string} className="rounded-md border p-3">
                  <div className="text-2xl font-bold tabular-nums">{n}</div>
                  <div className="text-xs text-muted-foreground">{label}</div>
                </div>
              ))}
            </div>
            {s.warnings?.map((w) => <Notice key={w} tone="warn">{w}</Notice>)}

            {job.errors.length > 0 && (
              <div className="grid gap-2">
                <h3 className="text-sm font-semibold">Filas con errores ({job.errors.length})</h3>
                <div className="max-h-64 overflow-auto rounded-md border">
                  <Table>
                    <TableHeader><TableRow><TableHead className="w-16">Fila</TableHead><TableHead>Columna</TableHead><TableHead>Problema</TableHead></TableRow></TableHeader>
                    <TableBody>
                      {job.errors.slice(0, 200).map((e, i) => (
                        <TableRow key={i}><TableCell className="tabular-nums">{e.row}</TableCell><TableCell className="font-mono text-xs">{e.column}</TableCell><TableCell>{e.message}</TableCell></TableRow>
                      ))}
                    </TableBody>
                  </Table>
                </div>
                <p className="text-xs text-muted-foreground">Los productos con errores no se importan; el resto sí. Corrige el archivo y vuelve a subirlo para completar.</p>
              </div>
            )}

            {job.status === "preview" && s.sample && s.sample.length > 0 && (
              <div className="grid gap-2">
                <h3 className="text-sm font-semibold">Vista previa</h3>
                <div className="max-h-64 overflow-auto rounded-md border">
                  <Table>
                    <TableHeader><TableRow><TableHead>Producto</TableHead><TableHead className="text-right">Variantes</TableHead><TableHead>Acción</TableHead></TableRow></TableHeader>
                    <TableBody>
                      {s.sample.map((r) => (
                        <TableRow key={r.row}>
                          <TableCell><div className="font-medium">{r.title}</div><div className="font-mono text-xs text-muted-foreground">{r.handle}</div></TableCell>
                          <TableCell className="text-right tabular-nums">{r.variants}</TableCell>
                          <TableCell>{r.invalid ? <Badge variant="bad">Con errores</Badge> : r.exists ? <Badge variant="outline">Actualizar</Badge> : <Badge variant="secondary">Crear</Badge>}</TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                </div>
              </div>
            )}

            {job.status === "running" && (
              <div className="grid gap-1">
                <div className="h-2 overflow-hidden rounded-full bg-muted" role="progressbar" aria-valuemin={0} aria-valuemax={job.total} aria-valuenow={job.processed}>
                  <div className="h-full bg-primary transition-all" style={{ width: `${job.total ? (job.processed / job.total) * 100 : 0}%` }} />
                </div>
                <p className="text-sm text-muted-foreground">{job.processed} de {job.total} productos…</p>
              </div>
            )}
            {(job.status === "done" || job.status === "failed") && (
              <Notice tone={job.failed ? "warn" : "ok"}>
                <CheckCircle2 className="mr-1 inline size-4" aria-hidden="true" />
                {job.created} creados, {job.updated} actualizados{job.failed ? `, ${job.failed} fallaron` : ""}. Ya están visibles en el catálogo.
              </Notice>
            )}
            {job.status === "preview" && (
              <div className="flex flex-wrap gap-2">
                <Button onClick={() => void apply()} disabled={busy || s.creates + s.updates === 0}>
                  {busy ? "Iniciando…" : `Importar ${s.creates + s.updates} productos`}
                </Button>
                <Button variant="outline" onClick={() => setJob(null)}>Cancelar</Button>
              </div>
            )}
          </CardContent>
        </Card>
      )}

      <Card>
        <CardHeader><CardTitle className="text-base">Historial de importaciones</CardTitle></CardHeader>
        <CardContent>
          {(history.data ?? []).length === 0 ? (
            <p className="text-sm text-muted-foreground">Todavía no se ha importado ningún archivo.</p>
          ) : (
            <div className="overflow-x-auto">
              <Table>
                <TableHeader><TableRow><TableHead>Archivo</TableHead><TableHead>Fecha</TableHead><TableHead>Por</TableHead><TableHead className="text-right">Creados</TableHead><TableHead className="text-right">Actualizados</TableHead><TableHead>Estado</TableHead></TableRow></TableHeader>
                <TableBody>
                  {history.data!.map((j) => (
                    <TableRow key={j.id} className="cursor-pointer" onClick={() => setJob(j)}>
                      <TableCell className="font-medium">{j.filename}</TableCell>
                      <TableCell>{dateTime(j.createdAt)}</TableCell>
                      <TableCell className="text-muted-foreground">{j.createdBy}</TableCell>
                      <TableCell className="text-right tabular-nums">{j.created}</TableCell>
                      <TableCell className="text-right tabular-nums">{j.updated}</TableCell>
                      <TableCell><Badge variant={STATUS[j.status].variant}>{STATUS[j.status].label}</Badge></TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
        </CardContent>
      </Card>
    </CatalogPage>
  );
}
