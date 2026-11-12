import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import { Sparkles } from "lucide-react";
import { errorMessage, request } from "@/api";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { href } from "@/router";

interface CopilotAnswer {
  text: string;
  kind: "data" | "help" | "unknown" | "forbidden";
  links: { label: string; route: string }[];
  rows?: { label: string; value: string; route?: string }[];
}

const EXAMPLES = ["¿Cuánto vendí esta semana?", "¿Qué está agotado?", "¿Cómo importo productos?"];

/** Copilot: figures from the reports and how-to help, each with the screen
 * where it lives. Every answer respects the person's permissions. */
export function Copilot() {
  const [q, setQ] = useState("");
  const ask = useMutation({ mutationFn: (text: string) => request<CopilotAnswer>("POST", "/v1/ai/copilot", { q: text }) });
  const send = (text: string) => { const t = text.trim(); if (t.length >= 3) { setQ(t); ask.mutate(t); } };
  const a = ask.data;
  return (
    <Card data-copilot>
      <CardHeader className="pb-3">
        <CardTitle className="flex items-center gap-2 text-base"><Sparkles className="size-4" aria-hidden="true" /> Pregúntale a Apexx</CardTitle>
        <CardDescription>Ventas, lo más vendido, lo agotado, lo que buscan sin encontrar, o cómo hacer algo en el panel. Las cifras salen de tus reportes.</CardDescription>
      </CardHeader>
      <CardContent className="grid gap-3 text-sm">
        <form className="flex flex-wrap gap-2" onSubmit={(e) => { e.preventDefault(); send(q); }}>
          <label htmlFor="copilot-q" className="sr-only">Tu pregunta</label>
          <input id="copilot-q" className="h-9 min-w-0 flex-1 basis-60 rounded-md border bg-background px-3" value={q} maxLength={300} onChange={(e) => setQ(e.target.value)} placeholder="Escribe tu pregunta" />
          <Button type="submit" disabled={ask.isPending || q.trim().length < 3}>{ask.isPending ? "Buscando…" : "Preguntar"}</Button>
        </form>
        {!a && !ask.error && <div className="flex flex-wrap gap-2">{EXAMPLES.map((x) => <Button key={x} type="button" size="sm" variant="outline" onClick={() => send(x)}>{x}</Button>)}</div>}
        <div role="status" aria-live="polite" className="grid gap-2">
          {ask.error && <p className="text-destructive">{errorMessage(ask.error)}</p>}
          {a && <>
            <p className={a.kind === "unknown" || a.kind === "forbidden" ? "text-muted-foreground" : undefined}>{a.text}</p>
            {a.rows && a.rows.length > 0 && <ul className="grid gap-1 rounded-md border p-2">{a.rows.map((r, i) => (
              <li key={i} className="flex flex-wrap justify-between gap-x-3"><span className="min-w-0 break-words">{r.route ? <a className="text-primary hover:underline" href={href(...r.route.split("/"))}>{r.label}</a> : r.label}</span><span className="tabular-nums text-muted-foreground">{r.value}</span></li>))}</ul>}
            {a.links.length > 0 && <p>{a.links.map((l) => <a key={l.route} className="text-primary hover:underline" href={href(...l.route.split("/"))}>{l.label}</a>)}</p>}
          </>}
        </div>
      </CardContent>
    </Card>
  );
}
