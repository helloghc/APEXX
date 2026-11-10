import { useState, type ReactNode } from "react";
import { AlertCircle, CheckCircle2, Copy } from "lucide-react";
import { errorMessage } from "@/api";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

export function Page({ title, sub, actions, children }: { title: string; sub?: ReactNode; actions?: ReactNode; children: ReactNode }) {
  return (
    <main className="mx-auto grid w-full max-w-6xl content-start gap-5 px-4 py-6 md:px-8">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="grid gap-1">
          <h1 className="text-2xl font-bold tracking-tight">{title}</h1>
          {sub && <div className="text-sm text-muted-foreground">{sub}</div>}
        </div>
        {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
      </div>
      {children}
    </main>
  );
}

export function Notice({ tone, children, className }: { tone: "ok" | "warn" | "bad"; children: ReactNode; className?: string }) {
  const styles = { ok: "bg-ok-soft text-ok", warn: "bg-warn-soft text-warn", bad: "bg-bad-soft text-bad" }[tone];
  const Icon = tone === "ok" ? CheckCircle2 : AlertCircle;
  return (
    <div className={cn("flex items-start gap-2 rounded-md px-3 py-2 text-sm", styles, className)} role={tone === "bad" ? "alert" : "status"}>
      <Icon className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
      <div className="min-w-0">{children}</div>
    </div>
  );
}

export function ErrorBox({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  if (!error) return null;
  return (
    <Notice tone="bad">
      <span>{errorMessage(error)}</span>{" "}
      {onRetry && (
        <Button variant="link" size="sm" className="h-auto p-0 text-bad underline" onClick={onRetry}>
          Reintentar
        </Button>
      )}
    </Notice>
  );
}

/** Secret shows a value once with a copy button; the API never returns it again. */
export function Secret({ label, value }: { label: string; value: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="grid gap-2">
      <span className="text-sm font-medium">{label}</span>
      <code className="block select-all break-all rounded-md border bg-muted p-2 font-mono text-xs" data-testid="secret">
        {value}
      </code>
      <div className="flex flex-wrap items-center gap-2">
        <Button
          variant="outline"
          size="sm"
          onClick={() => {
            navigator.clipboard
              ?.writeText(value)
              .then(() => setCopied(true))
              .catch(() => setCopied(false));
          }}
        >
          <Copy /> {copied ? "Copiado" : "Copiar"}
        </Button>
        <span className="text-xs text-muted-foreground">Guárdalo ahora: no se volverá a mostrar.</span>
      </div>
    </div>
  );
}

const STATUS: Record<string, { label: string; tone: "ok" | "warn" | "bad" }> = {
  pending_payment: { label: "Pago pendiente", tone: "warn" },
  paid: { label: "Pagado", tone: "ok" },
  fulfilled: { label: "Enviado", tone: "ok" },
  cancelled: { label: "Cancelado", tone: "bad" },
  refunded: { label: "Reembolsado", tone: "bad" },
  active: { label: "Activo", tone: "ok" },
  draft: { label: "Borrador", tone: "warn" },
  archived: { label: "Archivado", tone: "bad" },
  disabled: { label: "Desactivado", tone: "bad" },
  delivered: { label: "Entregado", tone: "ok" },
  pending: { label: "Pendiente", tone: "warn" },
  dead: { label: "Fallido", tone: "bad" },
  issued: { label: "Emitida", tone: "ok" },
  failed: { label: "Falló", tone: "bad" },
  voided: { label: "Anulada", tone: "bad" },
  captured: { label: "Cobrado", tone: "ok" },
  authorized: { label: "Autorizado", tone: "warn" },
};

export function StatusPill({ status }: { status: string }) {
  const s = STATUS[status] ?? { label: status, tone: "warn" as const };
  return <Badge variant={s.tone}>{s.label}</Badge>;
}

export function statusLabel(status: string): string {
  return STATUS[status]?.label ?? status;
}

export function dateTime(s: string | null | undefined): string {
  if (!s) return "—";
  const d = new Date(s);
  return Number.isNaN(d.getTime()) ? "—" : d.toLocaleString("es-MX", { dateStyle: "medium", timeStyle: "short" });
}
