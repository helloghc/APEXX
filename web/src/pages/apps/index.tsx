import { useQuery } from "@tanstack/react-query";
import { request } from "@/api";
import { href } from "@/router";
import { ErrorBox } from "../common";

export interface AppExt { id: string; name: string; adminPage: boolean; orderBlock: boolean }
export const appExtensions = () => request<{ items: AppExt[] }>("GET", "/v1/app-extensions").then((r) => r.items);

/** AppFrame shows an app's view in a sandboxed frame with a fresh signed session. */
export function AppFrame({ id, place, order, height }: { id: string; place: "page" | "order"; order?: string; height: number | string }) {
  const q = useQuery({ queryKey: ["app-session", id, place, order], queryFn: () => request<{ url: string }>("POST", `/v1/app-extensions/${id}/session`, { place, order }), staleTime: 4 * 60_000 });
  if (q.error) return <ErrorBox error={q.error} />;
  if (!q.data) return <div className="animate-pulse rounded-md bg-muted" style={{ height }} />;
  return <iframe title={`App ${id}`} src={q.data.url} sandbox="allow-scripts allow-forms allow-popups allow-downloads" referrerPolicy="no-referrer" className="w-full rounded-md border bg-background" style={{ height }} data-app-frame />;
}

/** Apps: the pages that installed apps add to the panel. */
export function AppsHub({ route }: { route: string[] }) {
  const q = useQuery({ queryKey: ["app-extensions"], queryFn: appExtensions });
  if (q.error) return <ErrorBox error={q.error} />;
  const pages = (q.data ?? []).filter((a) => a.adminPage);
  const current = pages.find((a) => a.id === route[1]) ?? pages[0];
  return (
    <div className="grid gap-4">
      <div className="flex flex-wrap items-center gap-2">
        <h1 className="mr-4 text-2xl font-bold tracking-tight">Apps</h1>
        {pages.map((a) => <a key={a.id} href={href("apps", a.id)} className={`rounded-md px-3 py-1.5 text-sm ${current?.id === a.id ? "bg-primary text-primary-foreground" : "hover:bg-muted"}`}>{a.name}</a>)}
      </div>
      {!q.data ? <div className="h-64 animate-pulse rounded-md bg-muted" /> : current ? <AppFrame key={current.id} id={current.id} place="page" height="78vh" /> : <p className="text-sm text-muted-foreground">Ninguna app instalada agrega páginas al panel. Instálalas en Configuración › Apps.</p>}
    </div>
  );
}
