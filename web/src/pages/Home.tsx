import { useQuery } from "@tanstack/react-query";
import { api, type Me } from "@/api";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { can, visibleNav } from "@/perms";
import { href } from "@/router";
import { dateTime, ErrorBox, Page } from "./common";

function Stat({ value, label }: { value: string; label: string }) {
  return (
    <div className="rounded-lg border bg-card p-3">
      <div className="text-xl font-bold tabular-nums">{value}</div>
      <div className="text-xs text-muted-foreground">{label}</div>
    </div>
  );
}

export function Home({ me }: { me: Me }) {
  const fresh = useQuery({ queryKey: ["freshness"], queryFn: api.freshness, enabled: can(me, "settings", "read"), refetchInterval: 15_000 });
  const audit = useQuery({ queryKey: ["audit"], queryFn: api.auditVerify, enabled: can(me, "settings", "admin") });
  const shortcuts = visibleNav(me).filter((n) => n.route !== "inicio");

  return (
    <Page title={`Hola, ${me.name || me.email || "equipo"}`} sub={`Tienda ${me.tenant}`}>
      {can(me, "settings", "read") && (
        <Card>
          <CardHeader>
            <CardTitle>Estado de la tienda</CardTitle>
          </CardHeader>
          <CardContent className="grid gap-3">
            <ErrorBox error={fresh.error} onRetry={() => void fresh.refetch()} />
            {fresh.data && (
              <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
                <Stat value={`${fresh.data.avgLagMs.toFixed(1)} ms`} label="Tiempo promedio para que un cambio se vea en la tienda" />
                <Stat value={`${fresh.data.maxLagMs.toFixed(1)} ms`} label="Tiempo máximo registrado" />
                <Stat value={fresh.data.eventsApplied.toLocaleString("es-MX")} label={`Cambios aplicados desde ${dateTime(fresh.data.loadedAt)}`} />
                <Stat value={String(fresh.data.errors)} label="Errores de actualización" />
              </div>
            )}
            {audit.data && (
              <p className="text-sm">
                Registro de auditoría:{" "}
                {audit.data.intact ? <Badge variant="ok">Íntegro</Badge> : <Badge variant="bad">Alterado desde el registro {audit.data.firstBrokenSeq}</Badge>}
              </p>
            )}
          </CardContent>
        </Card>
      )}
      <Card>
        <CardHeader>
          <CardTitle>Accesos rápidos</CardTitle>
        </CardHeader>
        <CardContent>
          {shortcuts.length === 0 ? (
            <p className="text-sm text-muted-foreground">Tu acceso todavía no incluye ninguna sección. Pide permisos a un administrador.</p>
          ) : (
            <div className="flex flex-wrap gap-2">
              {shortcuts.map((n) => (
                <Button key={n.route} variant="outline" asChild>
                  <a href={href(n.route)}>{n.label}</a>
                </Button>
              ))}
            </div>
          )}
        </CardContent>
      </Card>
    </Page>
  );
}
