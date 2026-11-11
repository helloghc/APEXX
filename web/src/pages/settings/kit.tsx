import { useEffect, useState, type ReactNode } from "react";
import { Search, type LucideIcon } from "lucide-react";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";
import { centsToPesos, parsePesos } from "@/money";

/** SubPage is the frame of one Configuración screen: title, help line, actions. */
export function SubPage({ title, sub, actions, children }: { title: string; sub?: ReactNode; actions?: ReactNode; children: ReactNode }) {
  return (
    <div className="grid min-w-0 content-start gap-5">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="grid min-w-0 gap-1">
          <h1 className="text-2xl font-bold tracking-tight">{title}</h1>
          {sub && <p className="max-w-2xl text-sm text-muted-foreground">{sub}</p>}
        </div>
        {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
      </div>
      {children}
    </div>
  );
}

export function Section({
  title,
  description,
  actions,
  children,
  className,
}: {
  title: string;
  description?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  return (
    <Card className={className}>
      <CardHeader className="flex-row flex-wrap items-start justify-between gap-3">
        <div className="grid min-w-0 gap-1.5">
          <CardTitle>{title}</CardTitle>
          {description && <CardDescription>{description}</CardDescription>}
        </div>
        {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
      </CardHeader>
      <CardContent className="grid gap-4">{children}</CardContent>
    </Card>
  );
}

export function EmptyState({ icon: Icon, title, text, action }: { icon: LucideIcon; title: string; text: ReactNode; action?: ReactNode }) {
  return (
    <div className="grid justify-items-center gap-3 rounded-xl border border-dashed bg-card px-6 py-12 text-center">
      <span className="grid size-11 place-items-center rounded-full bg-accent text-accent-foreground">
        <Icon className="size-5" aria-hidden="true" />
      </span>
      <div className="grid gap-1">
        <h2 className="text-base font-semibold">{title}</h2>
        <p className="mx-auto max-w-md text-sm text-muted-foreground">{text}</p>
      </div>
      {action && <div className="flex flex-wrap justify-center gap-2">{action}</div>}
    </div>
  );
}

export function Skeleton({ className }: { className?: string }) {
  return <div className={cn("animate-pulse rounded-md bg-muted", className)} aria-hidden="true" />;
}

/** Loading shows placeholder rows while a screen loads its data. */
export function Loading({ rows = 4 }: { rows?: number }) {
  return (
    <div className="grid gap-3" role="status" aria-label="Cargando">
      <Skeleton className="h-9 w-full max-w-sm" />
      <div className="grid gap-px overflow-hidden rounded-xl border bg-card">
        {Array.from({ length: rows }, (_, i) => (
          <div key={i} className="flex items-center gap-4 border-b px-4 py-3.5 last:border-0">
            <Skeleton className="h-4 w-1/4" />
            <Skeleton className="h-4 w-1/3" />
            <Skeleton className="ml-auto h-4 w-16" />
          </div>
        ))}
      </div>
    </div>
  );
}

/** Switch is an accessible on/off control (role="switch"). */
export function Switch({
  checked,
  onChange,
  label,
  disabled,
  id,
}: {
  checked: boolean;
  onChange: (v: boolean) => void;
  label: string;
  disabled?: boolean;
  id?: string;
}) {
  return (
    <button
      id={id}
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      disabled={disabled}
      onClick={() => onChange(!checked)}
      className={cn(
        "relative inline-flex h-6 w-11 shrink-0 cursor-pointer items-center rounded-full border border-transparent transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background disabled:cursor-not-allowed disabled:opacity-50",
        checked ? "bg-primary" : "bg-input",
      )}
    >
      <span className={cn("block size-5 rounded-full bg-card shadow transition-transform", checked ? "translate-x-5" : "translate-x-0.5")} />
    </button>
  );
}

/** SwitchRow is a setting with a title, an explanation and a switch. */
export function SwitchRow({
  title,
  text,
  checked,
  onChange,
  disabled,
  children,
}: {
  title: string;
  text: ReactNode;
  checked: boolean;
  onChange: (v: boolean) => void;
  disabled?: boolean;
  children?: ReactNode;
}) {
  return (
    <div className="grid gap-3 border-b py-4 first:pt-0 last:border-0 last:pb-0">
      <div className="flex items-start justify-between gap-4">
        <div className="grid gap-1">
          <span className="text-sm font-medium">{title}</span>
          <span className="text-sm text-muted-foreground">{text}</span>
        </div>
        <Switch checked={checked} onChange={onChange} label={title} disabled={disabled} />
      </div>
      {children}
    </div>
  );
}

export function Confirm({
  open,
  title,
  text,
  confirmLabel,
  destructive = true,
  busy,
  onConfirm,
  onClose,
}: {
  open: boolean;
  title: string;
  text: ReactNode;
  confirmLabel: string;
  destructive?: boolean;
  busy?: boolean;
  onConfirm: () => void;
  onClose: () => void;
}) {
  return (
    <AlertDialog open={open} onOpenChange={(o) => !o && onClose()}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription asChild>
            <div className="text-sm text-muted-foreground">{text}</div>
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancelar</AlertDialogCancel>
          <AlertDialogAction
            destructive={destructive}
            disabled={busy}
            onClick={(e) => {
              e.preventDefault();
              onConfirm();
            }}
          >
            {busy ? "Un momento…" : confirmLabel}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

export function SearchInput({ value, onChange, placeholder, label }: { value: string; onChange: (v: string) => void; placeholder: string; label: string }) {
  return (
    <div className="relative w-full max-w-sm">
      <Search className="pointer-events-none absolute left-2.5 top-2.5 size-4 text-muted-foreground" aria-hidden="true" />
      <Input type="search" aria-label={label} className="pl-8" placeholder={placeholder} value={value} onChange={(e) => onChange(e.target.value)} />
    </div>
  );
}

/** useDebounced returns value after it stops changing for ms. */
export function useDebounced<T>(value: T, ms = 250): T {
  const [v, setV] = useState(value);
  useEffect(() => {
    const t = setTimeout(() => setV(value), ms);
    return () => clearTimeout(t);
  }, [value, ms]);
  return v;
}

/** MoneyInput edits cents as pesos; onChange gets null while the text is not a valid amount. */
export function MoneyInput({
  cents,
  onChange,
  id,
  className,
  ...aria
}: {
  cents: number;
  onChange: (cents: number | null) => void;
  id?: string;
  className?: string;
  "aria-label"?: string;
  "aria-invalid"?: boolean;
  "aria-describedby"?: string;
}) {
  const [text, setText] = useState(() => centsToPesos(cents));
  useEffect(() => {
    if (parsePesos(text) !== cents) setText(centsToPesos(cents));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [cents]);
  return (
    <div className={cn("relative", className)}>
      <span className="pointer-events-none absolute left-3 top-2 text-sm text-muted-foreground">$</span>
      <Input
        id={id}
        inputMode="decimal"
        className="pl-6 tabular-nums"
        value={text}
        onChange={(e) => {
          setText(e.target.value);
          onChange(parsePesos(e.target.value));
        }}
        onBlur={() => {
          const c = parsePesos(text);
          if (c !== null) setText(centsToPesos(c));
        }}
        {...aria}
      />
    </div>
  );
}

/** SaveBar appears when a form has unsaved changes. */
export function SaveBar({ dirty, busy, onSave, onDiscard, label = "Guardar cambios" }: { dirty: boolean; busy: boolean; onSave: () => void; onDiscard: () => void; label?: string }) {
  if (!dirty) return null;
  return (
    <div className="sticky bottom-3 z-10 flex flex-wrap items-center justify-between gap-3 rounded-xl border bg-card px-4 py-3 shadow-lg">
      <span className="text-sm font-medium">Tienes cambios sin guardar</span>
      <div className="flex gap-2">
        <Button variant="outline" onClick={onDiscard} disabled={busy}>
          Descartar
        </Button>
        <Button onClick={onSave} disabled={busy}>
          {busy ? "Guardando…" : label}
        </Button>
      </div>
    </div>
  );
}

export function ReadOnlyNote({ show, need = "Admin" }: { show: boolean; need?: string }) {
  if (!show) return null;
  return (
    <p className="rounded-md bg-muted px-3 py-2 text-sm text-muted-foreground">
      Puedes ver esta configuración. Para cambiarla necesitas nivel {need} en Configuración.
    </p>
  );
}

/** Chip is a small removable tag (states, postal ranges). */
export function Chips({ items, onRemove }: { items: { key: string; label: string }[]; onRemove?: (key: string) => void }) {
  if (items.length === 0) return null;
  return (
    <ul className="flex flex-wrap gap-1.5">
      {items.map((i) => (
        <li key={i.key} className="inline-flex items-center gap-1 rounded-full border bg-muted px-2.5 py-0.5 text-xs">
          {i.label}
          {onRemove && (
            <button type="button" className="cursor-pointer rounded-full text-muted-foreground hover:text-foreground" aria-label={`Quitar ${i.label}`} onClick={() => onRemove(i.key)}>
              ×
            </button>
          )}
        </li>
      ))}
    </ul>
  );
}
