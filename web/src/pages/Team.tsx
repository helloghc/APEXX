import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { KeyRound, UserPlus } from "lucide-react";
import { api, errorMessage, fieldErrors, type Level, type Me, type Member, type Permissions } from "@/api";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { FormField } from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { toast } from "@/components/ui/sonner";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { validateMember } from "@/forms";
import { can, compact, LEVEL_LABEL, LEVELS, MODULES, presetOf, PRESETS } from "@/perms";
import { accountApi, TeamAccess } from "./account/index";
import { dateTime, ErrorBox, Notice, Page, Secret, StatusPill } from "./common";

export function Team({ me }: { me: Me }) {
  const q = useQuery({ queryKey: ["members"], queryFn: api.members });
  const isAdmin = can(me, "team", "admin");
  const [invite, setInvite] = useState(false);
  const [edit, setEdit] = useState<Member | null>(null);
  const [keyOpen, setKeyOpen] = useState(false);

  return (
    <Page
      title="Equipo"
      sub="Puestos ilimitados: invita a todo tu equipo y define qué puede hacer cada persona en cada módulo."
      actions={
        isAdmin && (
          <>
            <Button variant="outline" onClick={() => setKeyOpen(true)}>
              <KeyRound /> Crear API key
            </Button>
            <Button onClick={() => setInvite(true)}>
              <UserPlus /> Crear cuenta con contraseña
            </Button>
          </>
        )
      }
    >
      <ErrorBox error={q.error} onRetry={() => void q.refetch()} />
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Persona</TableHead>
            <TableHead>Acceso</TableHead>
            <TableHead>Estado</TableHead>
            <TableHead>Último ingreso</TableHead>
            {isAdmin && <TableHead className="text-right">Acciones</TableHead>}
          </TableRow>
        </TableHeader>
        <TableBody>
          {(q.data?.items ?? []).map((m) => (
            <TableRow key={m.id} data-testid={`member-${m.email}`}>
              <TableCell>
                <strong>{m.name || m.email}</strong>
                <div className="text-xs text-muted-foreground">{m.email}</div>
              </TableCell>
              <TableCell>{m.owner ? <Badge>Dueño</Badge> : <PermSummary perms={m.permissions} />}</TableCell>
              <TableCell>
                <StatusPill status={m.status} />
                {m.twoStep && <Badge variant="ok" className="ml-2">2 pasos</Badge>}
              </TableCell>
              <TableCell className="text-sm">{dateTime(m.lastLoginAt)}</TableCell>
              {isAdmin && (
                <TableCell className="text-right">
                  {!m.owner && (
                    <Button variant="outline" size="sm" onClick={() => setEdit(m)}>
                      Editar acceso
                    </Button>
                  )}
                  {m.twoStep && m.id !== me.id && (
                    <Button
                      variant="ghost"
                      size="sm"
                      title="Úsalo si la persona perdió su teléfono y sus códigos de respaldo"
                      onClick={() =>
                        window.confirm(`¿Quitar la verificación en dos pasos de ${m.name || m.email}? Podrá entrar solo con su contraseña hasta que la active de nuevo.`) &&
                        void accountApi.reset(m.id).then(() => { toast.success("Verificación en dos pasos restablecida."); void q.refetch(); }).catch((e) => toast.error(errorMessage(e)))
                      }
                    >
                      Restablecer 2 pasos
                    </Button>
                  )}
                </TableCell>
              )}
            </TableRow>
          ))}
        </TableBody>
      </Table>
      {q.isPending && <p className="text-sm text-muted-foreground">Cargando equipo…</p>}
      {!isAdmin && <p className="text-sm text-muted-foreground">Puedes ver el equipo. Para invitar o cambiar accesos necesitas nivel Admin en Equipo.</p>}

      <TeamAccess isAdmin={isAdmin} />

      {invite && <MemberDialog onClose={() => setInvite(false)} />}
      {edit && <MemberDialog member={edit} onClose={() => setEdit(null)} />}
      {keyOpen && <ApiKeyDialog onClose={() => setKeyOpen(false)} />}
    </Page>
  );
}

function PermSummary({ perms }: { perms: Permissions }) {
  const p = presetOf(perms);
  if (p) return <Badge variant="secondary">{PRESETS.find((x) => x.id === p)!.label}</Badge>;
  const parts = MODULES.filter((m) => perms[m.id] && perms[m.id] !== "none").map((m) => `${m.label}: ${LEVEL_LABEL[perms[m.id]!]}`);
  return <span className="text-xs">{parts.length ? parts.join(" · ") : "Sin acceso"}</span>;
}

function PermissionMatrix({ value, onChange }: { value: Permissions; onChange: (p: Permissions) => void }) {
  const preset = presetOf(value);
  return (
    <div className="grid gap-3">
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-sm font-medium">Accesos rápidos:</span>
        {PRESETS.map((p) => (
          <Button key={p.id} type="button" size="sm" variant={preset === p.id ? "default" : "outline"} onClick={() => onChange({ ...p.perms })}>
            {p.label}
          </Button>
        ))}
      </div>
      <div className="overflow-x-auto rounded-lg border">
        <table className="w-full text-sm">
          <caption className="sr-only">Nivel de acceso por módulo</caption>
          <thead className="bg-muted">
            <tr>
              <th className="px-3 py-2 text-left text-xs font-semibold text-muted-foreground">Módulo</th>
              {LEVELS.map((l) => (
                <th key={l} className="px-2 py-2 text-center text-xs font-semibold text-muted-foreground">
                  {LEVEL_LABEL[l]}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {MODULES.map((m) => {
              const cur: Level = value[m.id] ?? "none";
              return (
                <tr key={m.id} className="border-t">
                  <th scope="row" className="px-3 py-2 text-left font-medium">
                    {m.label}
                  </th>
                  {LEVELS.map((l) => (
                    <td key={l} className="px-2 py-2 text-center">
                      <input
                        type="radio"
                        className="size-4 cursor-pointer accent-[var(--accent)]"
                        name={`perm-${m.id}`}
                        aria-label={`${m.label}: ${LEVEL_LABEL[l]}`}
                        checked={cur === l}
                        onChange={() => onChange({ ...value, [m.id]: l })}
                      />
                    </td>
                  ))}
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function MemberDialog({ member, onClose }: { member?: Member; onClose: () => void }) {
  const qc = useQueryClient();
  const [email, setEmail] = useState(member?.email ?? "");
  const [name, setName] = useState(member?.name ?? "");
  const [password, setPassword] = useState("");
  const [perms, setPerms] = useState<Permissions>(member?.permissions ?? { ...PRESETS[1]!.perms });
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [err, setErr] = useState<string | null>(null);

  const save = useMutation({
    mutationFn: async () => {
      if (member) return api.updateMember(member.id, { permissions: compact(perms) });
      return api.createMember({ email: email.trim(), name: name.trim(), password, permissions: compact(perms) });
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["members"] });
      toast.success(member ? "Acceso actualizado" : `Invitación lista: comparte la contraseña temporal con ${email.trim()}`);
      onClose();
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      setErr(errorMessage(e));
    },
  });
  const toggle = useMutation({
    mutationFn: () => api.updateMember(member!.id, { status: member!.status === "active" ? "disabled" : "active" }),
    onSuccess: (m) => {
      void qc.invalidateQueries({ queryKey: ["members"] });
      toast.success(m.status === "disabled" ? "Miembro desactivado: su sesión se cerró" : "Miembro reactivado");
      onClose();
    },
    onError: (e) => setErr(errorMessage(e)),
  });

  return (
    <Dialog open onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="w-[min(720px,calc(100vw-32px))]">
        <DialogHeader>
          <DialogTitle>{member ? `Acceso de ${member.name || member.email}` : "Invitar miembro"}</DialogTitle>
          <DialogDescription>
            {member ? "Los cambios aplican en la siguiente acción que haga esta persona." : "Crea la cuenta con una contraseña temporal y compártela por un canal seguro."}
          </DialogDescription>
        </DialogHeader>
        <form
          className="grid gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            setErr(null);
            if (!member) {
              const v = validateMember({ email, name, password });
              setErrors(v);
              if (Object.keys(v).length) return;
            }
            save.mutate();
          }}
          noValidate
        >
          {!member && (
            <div className="grid gap-3 md:grid-cols-3">
              <FormField id="m-email" label="Correo" required error={errors.email}>
                <Input type="email" value={email} onChange={(e) => setEmail(e.target.value)} autoComplete="off" />
              </FormField>
              <FormField id="m-name" label="Nombre">
                <Input value={name} onChange={(e) => setName(e.target.value)} autoComplete="off" />
              </FormField>
              <FormField id="m-pass" label="Contraseña temporal" required error={errors.password} description="Mínimo 12 caracteres.">
                <Input type="text" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" />
              </FormField>
            </div>
          )}
          <PermissionMatrix value={perms} onChange={setPerms} />
          {err && <Notice tone="bad">{err}</Notice>}
          <DialogFooter className="sm:justify-between">
            {member ? (
              <Button type="button" variant={member.status === "active" ? "destructive" : "outline"} disabled={toggle.isPending} onClick={() => toggle.mutate()}>
                {member.status === "active" ? "Desactivar miembro" : "Reactivar miembro"}
              </Button>
            ) : (
              <span />
            )}
            <div className="flex gap-2">
              <Button type="button" variant="outline" onClick={onClose}>
                Cancelar
              </Button>
              <Button type="submit" disabled={save.isPending}>
                {save.isPending ? "Guardando…" : member ? "Guardar acceso" : "Crear miembro"}
              </Button>
            </div>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

const ROLES = [
  { id: "storefront", label: "Tienda (pública, para el storefront)" },
  { id: "viewer", label: "Lector" },
  { id: "editor", label: "Editor" },
  { id: "publisher", label: "Publicador del diseño" },
  { id: "admin", label: "Admin" },
];

function ApiKeyDialog({ onClose }: { onClose: () => void }) {
  const [name, setName] = useState("");
  const [role, setRole] = useState("viewer");
  const [key, setKey] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const create = useMutation({
    mutationFn: () => api.createApiKey(name.trim() || role, role),
    onSuccess: (r) => setKey(r.key),
    onError: (e) => setErr(errorMessage(e)),
  });
  return (
    <Dialog open onOpenChange={(v) => !v && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Crear API key</DialogTitle>
          <DialogDescription>Para integraciones (ERP, conectores) o para el storefront. Cada llave tiene un rol fijo.</DialogDescription>
        </DialogHeader>
        {key ? (
          <Card>
            <CardHeader>
              <CardTitle>Llave creada</CardTitle>
              <CardDescription>Rol: {ROLES.find((r) => r.id === role)?.label}</CardDescription>
            </CardHeader>
            <CardContent>
              <Secret label="API key" value={key} />
            </CardContent>
          </Card>
        ) : (
          <form
            className="grid gap-4"
            onSubmit={(e) => {
              e.preventDefault();
              create.mutate();
            }}
          >
            <FormField id="k-name" label="Nombre" description="Para reconocerla después, por ejemplo “ERP SAP”.">
              <Input value={name} onChange={(e) => setName(e.target.value)} />
            </FormField>
            <div className="grid gap-1.5">
              <span className="text-sm font-medium" id="k-role-label">
                Rol
              </span>
              <Select value={role} onValueChange={setRole}>
                <SelectTrigger aria-labelledby="k-role-label">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {ROLES.map((r) => (
                    <SelectItem key={r.id} value={r.id}>
                      {r.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            {err && <Notice tone="bad">{err}</Notice>}
            <DialogFooter>
              <Button type="submit" disabled={create.isPending}>
                Crear llave
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}
