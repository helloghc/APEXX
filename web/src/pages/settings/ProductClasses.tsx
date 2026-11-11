import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Plus, Undo2 } from "lucide-react";
import { errorMessage } from "@/api";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { FormField } from "@/components/ui/form";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { toast } from "@/components/ui/sonner";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { ErrorBox } from "../common";
import { sapi, type ProductClassRow, type ProductHit } from "./api";
import { SearchInput, Section, Skeleton, useDebounced } from "./kit";

export const TAX_CLASS_LABEL: Record<string, string> = { general: "General (IVA de la región)", exento: "Exento", tasa0: "Tasa 0%" };

/**
 * ProductClasses lists the products that do not follow the defaults: another
 * tax class (exento, tasa 0%) or another shipping profile (pesado). It is
 * shown in Envíos → Perfiles and in Impuestos.
 */
export function ProductClasses({ canEdit, focus }: { canEdit: boolean; focus: "profile" | "tax" }) {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["settings", "product-classes"], queryFn: sapi.productClasses });
  const ship = useQuery({ queryKey: ["settings", "shipping"], queryFn: sapi.shipping });
  const [add, setAdd] = useState(false);
  const profiles = (ship.data?.profiles ?? []).filter((p) => p.kind !== "default");
  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ["settings", "product-classes"] });
    void qc.invalidateQueries({ queryKey: ["settings", "shipping"] });
  };
  const set = useMutation({
    mutationFn: (r: { id: string; taxClass: string; shippingProfile: string }) => sapi.setProductClass(r.id, { taxClass: r.taxClass, shippingProfile: r.shippingProfile }),
    onSuccess: () => {
      refresh();
      toast.success("Producto actualizado. Aplica al siguiente carrito.");
    },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const rows = q.data ?? [];

  return (
    <Section
      title={focus === "tax" ? "Productos con otra clase de impuesto" : "Productos con otro perfil"}
      description={
        focus === "tax"
          ? "Todos los productos pagan la tasa general de su región, salvo los de esta lista (por ejemplo libros a tasa 0% o servicios exentos)."
          : "Todos los productos usan el perfil estándar, salvo los de esta lista."
      }
      actions={
        canEdit && (
          <Button variant="outline" onClick={() => setAdd(true)}>
            <Plus /> Agregar producto
          </Button>
        )
      }
    >
      <ErrorBox error={q.error} onRetry={() => void q.refetch()} />
      {q.isPending && <Skeleton className="h-16 w-full" />}
      {q.data && rows.length === 0 && <p className="text-sm text-muted-foreground">Ningún producto tiene reglas propias: todos usan la tasa general y el perfil estándar.</p>}
      {rows.length > 0 && (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Producto</TableHead>
              <TableHead>Impuesto</TableHead>
              <TableHead>Perfil de envío</TableHead>
              {canEdit && <TableHead className="text-right">Acciones</TableHead>}
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((r) => (
              <TableRow key={r.productId} data-testid={`pclass-${r.productId}`}>
                <TableCell>
                  <span className="font-medium">{r.title}</span>
                  <div className="font-mono text-xs text-muted-foreground">
                    {r.productId} {r.missing && <Badge variant="warn">Ya no existe</Badge>}
                  </div>
                </TableCell>
                <TableCell className="min-w-44">
                  <ClassSelect row={r} disabled={!canEdit || set.isPending} onChange={(v) => set.mutate({ id: r.productId, taxClass: v, shippingProfile: r.shippingProfile })} />
                </TableCell>
                <TableCell className="min-w-44">
                  <Select
                    value={r.shippingProfile || "_"}
                    disabled={!canEdit || set.isPending}
                    onValueChange={(v) => set.mutate({ id: r.productId, taxClass: r.taxClass, shippingProfile: v === "_" ? "" : v })}
                  >
                    <SelectTrigger aria-label={`Perfil de envío de ${r.title}`}>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="_">Estándar</SelectItem>
                      {profiles.map((p) => (
                        <SelectItem key={p.id} value={p.id}>
                          {p.name}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </TableCell>
                {canEdit && (
                  <TableCell className="text-right">
                    <Button variant="ghost" size="sm" disabled={set.isPending} onClick={() => set.mutate({ id: r.productId, taxClass: "general", shippingProfile: "" })}>
                      <Undo2 /> Volver a lo general
                    </Button>
                  </TableCell>
                )}
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      {add && <AddDialog focus={focus} profiles={profiles} taken={rows.map((r) => r.productId)} onClose={() => setAdd(false)} onDone={refresh} />}
    </Section>
  );
}

function ClassSelect({ row, disabled, onChange }: { row: ProductClassRow; disabled: boolean; onChange: (v: string) => void }) {
  return (
    <Select value={row.taxClass} disabled={disabled} onValueChange={onChange}>
      <SelectTrigger aria-label={`Clase de impuesto de ${row.title}`}>
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {Object.entries(TAX_CLASS_LABEL).map(([k, l]) => (
          <SelectItem key={k} value={k}>
            {l}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}

function AddDialog({
  focus,
  profiles,
  taken,
  onClose,
  onDone,
}: {
  focus: "profile" | "tax";
  profiles: { id: string; name: string }[];
  taken: string[];
  onClose: () => void;
  onDone: () => void;
}) {
  const [search, setSearch] = useState("");
  const term = useDebounced(search);
  const hits = useQuery({ queryKey: ["settings", "product-search", term], queryFn: () => sapi.productSearch(term) });
  const [picked, setPicked] = useState<ProductHit | null>(null);
  const [taxClass, setTaxClass] = useState(focus === "tax" ? "tasa0" : "general");
  const [profile, setProfile] = useState(focus === "profile" ? (profiles[0]?.id ?? "") : "");
  const save = useMutation({
    mutationFn: () => sapi.setProductClass((picked as ProductHit).id, { taxClass, shippingProfile: profile }),
    onSuccess: () => {
      onDone();
      toast.success(`${picked?.title ?? "Producto"} actualizado. Aplica al siguiente carrito.`);
      onClose();
    },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const same = taxClass === "general" && !profile;
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Agregar producto</DialogTitle>
          <DialogDescription>Busca el producto y elige su clase de impuesto y su perfil de envío.</DialogDescription>
        </DialogHeader>
        <div className="grid gap-4">
          {picked ? (
            <div className="flex items-center justify-between gap-3 rounded-lg border bg-muted px-3 py-2">
              <div className="min-w-0">
                <div className="truncate text-sm font-medium">{picked.title}</div>
                <div className="font-mono text-xs text-muted-foreground">{picked.id}</div>
              </div>
              <Button variant="ghost" size="sm" onClick={() => setPicked(null)}>
                Cambiar
              </Button>
            </div>
          ) : (
            <div className="grid gap-2">
              <SearchInput value={search} onChange={setSearch} label="Buscar producto" placeholder="Nombre, marca o SKU" />
              <ul className="grid max-h-56 overflow-y-auto rounded-lg border bg-card" aria-label="Resultados">
                {hits.isPending && <li className="px-3 py-2 text-sm text-muted-foreground">Buscando…</li>}
                {hits.data?.length === 0 && <li className="px-3 py-2 text-sm text-muted-foreground">Ningún producto coincide.</li>}
                {(hits.data ?? []).map((h) => (
                  <li key={h.id} className="border-b last:border-0">
                    <button
                      type="button"
                      disabled={taken.includes(h.id)}
                      className="flex w-full cursor-pointer items-center justify-between gap-3 px-3 py-2 text-left hover:bg-muted disabled:cursor-not-allowed disabled:opacity-50"
                      onClick={() => setPicked(h)}
                    >
                      <span className="min-w-0">
                        <span className="block truncate text-sm">{h.title}</span>
                        <span className="block font-mono text-xs text-muted-foreground">{h.id}</span>
                      </span>
                      {taken.includes(h.id) && <Badge variant="secondary">Ya está en la lista</Badge>}
                    </button>
                  </li>
                ))}
              </ul>
            </div>
          )}
          <div className="grid gap-4 sm:grid-cols-2">
            <FormField id="pc-tax" label="Clase de impuesto">
              <Select value={taxClass} onValueChange={setTaxClass}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {Object.entries(TAX_CLASS_LABEL).map(([k, l]) => (
                    <SelectItem key={k} value={k}>
                      {l}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </FormField>
            <FormField id="pc-profile" label="Perfil de envío">
              <Select value={profile || "_"} onValueChange={(v) => setProfile(v === "_" ? "" : v)}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="_">Estándar</SelectItem>
                  {profiles.map((p) => (
                    <SelectItem key={p.id} value={p.id}>
                      {p.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </FormField>
          </div>
          {same && <p className="text-xs text-muted-foreground">Con clase general y perfil estándar el producto no necesita estar en la lista.</p>}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancelar
          </Button>
          <Button disabled={!picked || same || save.isPending} onClick={() => save.mutate()}>
            {save.isPending ? "Guardando…" : "Agregar"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
