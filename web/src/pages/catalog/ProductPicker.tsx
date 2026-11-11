import { useQuery } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { Check, Search } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";
import { ErrorBox, StatusPill } from "../common";
import { cat, type ProductRow } from "./api";
import { priceRange } from "./Products";
import { Skeleton, Thumb, useDebounced } from "./shared";

/** ProductPicker is a search dialog to choose products (related products, manual collections). */
export function ProductPicker({
  open,
  title,
  exclude,
  onClose,
  onPick,
}: {
  open: boolean;
  title: string;
  exclude: string[];
  onClose: () => void;
  onPick: (ps: ProductRow[]) => void;
}) {
  const [q, setQ] = useState("");
  const dq = useDebounced(q.trim());
  const [picked, setPicked] = useState<ProductRow[]>([]);
  useEffect(() => {
    if (open) {
      setQ("");
      setPicked([]);
    }
  }, [open]);
  const res = useQuery({ queryKey: ["products", "picker", dq], queryFn: () => cat.products({ q: dq, sort: "title" }, "", 30), enabled: open });
  const items = res.data?.items ?? [];
  return (
    <Dialog open={open} onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="w-[min(640px,calc(100vw-32px))]">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>Busca por nombre, SKU o marca y marca los productos que quieras agregar.</DialogDescription>
        </DialogHeader>
        <div className="relative">
          <Search className="pointer-events-none absolute left-2.5 top-2.5 size-4 text-muted-foreground" aria-hidden="true" />
          <Input type="search" autoFocus className="pl-8" placeholder="Buscar productos" aria-label="Buscar productos" value={q} onChange={(e) => setQ(e.target.value)} />
        </div>
        <ErrorBox error={res.error} onRetry={() => void res.refetch()} />
        <ul className="grid max-h-[46vh] gap-1 overflow-y-auto" aria-label="Resultados">
          {res.isPending && Array.from({ length: 5 }, (_, i) => <Skeleton key={i} className="h-12" />)}
          {!res.isPending && items.length === 0 && <li className="py-6 text-center text-sm text-muted-foreground">Ningún producto coincide.</li>}
          {items.map((p) => {
            const used = exclude.includes(p.id);
            const on = picked.some((x) => x.id === p.id);
            return (
              <li key={p.id}>
                <button
                  type="button"
                  disabled={used}
                  aria-pressed={on}
                  onClick={() => setPicked((cur) => (on ? cur.filter((x) => x.id !== p.id) : [...cur, p]))}
                  className={cn(
                    "flex w-full items-center gap-3 rounded-lg border px-2 py-1.5 text-left text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                    on ? "border-primary bg-accent" : "border-transparent hover:bg-muted",
                    used && "opacity-50",
                  )}
                >
                  <Thumb media={p.thumb} size={36} />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate font-medium">{p.title}</span>
                    <span className="block truncate text-xs text-muted-foreground">
                      {used ? "Ya está agregado" : priceRange(p)}
                    </span>
                  </span>
                  <StatusPill status={p.status} />
                  <span className={cn("grid size-5 place-items-center rounded-full", on ? "bg-primary text-primary-foreground" : "border")}>{on && <Check className="size-3.5" />}</span>
                </button>
              </li>
            );
          })}
        </ul>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancelar
          </Button>
          <Button
            disabled={picked.length === 0}
            onClick={() => {
              onPick(picked);
              onClose();
            }}
          >
            Agregar {picked.length > 0 ? picked.length : ""}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
