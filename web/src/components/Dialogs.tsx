import { useEffect, useRef, useState, type ReactNode } from "react";
import type { Release, SectionDefinition } from "../types";
import ui from "../ui.module.css";
import s from "./Dialogs.module.css";

function Dialog({ open, onClose, title, children }: { open: boolean; onClose: () => void; title: string; children: ReactNode }) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const d = ref.current;
    if (!d) return;
    if (open && !d.open) d.showModal?.();
    if (!open && d.open) d.close();
  }, [open]);
  return (
    <dialog ref={ref} className={ui.dialog} onClose={onClose} aria-label={title}>
      {open && (
        <div className={ui.dialogBody}>
          <div className={ui.dialogHead}>
            <h2>{title}</h2>
            <button className={ui.iconBtn} onClick={onClose} aria-label="Cerrar">
              ✕
            </button>
          </div>
          {children}
        </div>
      )}
    </dialog>
  );
}

export function SectionPicker({
  open,
  defs,
  kind,
  onPick,
  onClose,
}: {
  open: boolean;
  defs: SectionDefinition[];
  kind: string;
  onPick: (d: SectionDefinition) => void;
  onClose: () => void;
}) {
  const [q, setQ] = useState("");
  const visible = defs.filter(
    (d) => (q === "" || (d.name + d.description).toLowerCase().includes(q.toLowerCase())),
  );
  const groups = new Map<string, SectionDefinition[]>();
  for (const d of visible) groups.set(d.category, [...(groups.get(d.category) ?? []), d]);
  return (
    <Dialog open={open} onClose={onClose} title="Agregar sección">
      <input className={ui.input} placeholder="Buscar sección" aria-label="Buscar sección" value={q} onChange={(e) => setQ(e.target.value)} autoFocus />
      <div className={s.groups}>
        {[...groups.entries()].map(([cat, items]) => (
          <section key={cat}>
            <h3 className={s.cat}>{cat}</h3>
            <ul className={s.picks}>
              {items.map((d) => {
                const allowed = !d.enabledOn?.length || d.enabledOn.includes(kind);
                return (
                  <li key={d.type}>
                    <button className={s.pick} onClick={() => onPick(d)} disabled={!allowed}>
                      <strong>{d.name}</strong>
                      <span>{allowed ? d.description : "No disponible en este tipo de página"}</span>
                      <span className={s.cost}>
                        {(d.budget.htmlBytes / 1000).toFixed(1)} KB HTML · {(d.budget.jsBytes / 1000).toFixed(1)} KB JS
                        {d.hydration !== "none" ? " · interactiva" : ""}
                      </span>
                    </button>
                  </li>
                );
              })}
            </ul>
          </section>
        ))}
        {visible.length === 0 && <p className={ui.help}>No hay secciones con ese nombre.</p>}
      </div>
    </Dialog>
  );
}

export function History({
  open,
  items,
  onClose,
  onRollback,
  busy,
  names,
}: {
  open: boolean;
  names?: Map<string, string>;
  items: Release[];
  onClose: () => void;
  onRollback: () => void;
  busy: boolean;
}) {
  return (
    <Dialog open={open} onClose={onClose} title="Historial de publicaciones">
      {items.length === 0 ? (
        <p className={ui.help}>Todavía no hay publicaciones.</p>
      ) : (
        <ol className={s.history}>
          {items.map((r, i) => (
            <li key={r.revisionId + r.publishedAt}>
              <div>
                <strong>{i === 0 ? "En vivo · " : ""}{r.rolledBackFrom ? "Deshacer" : "Publicación"}</strong>{" "}
                <code>{r.revisionId}</code>
              </div>
              <span className={ui.help}>
                {new Date(r.publishedAt).toLocaleString("es-MX")} · {actorName(r.publishedBy, names)} · {(r.durationMicros / 1000).toFixed(2)} ms
              </span>
            </li>
          ))}
        </ol>
      )}
      <button className={ui.btn} onClick={onRollback} disabled={busy || items.length < 2}>
        Volver a la publicación anterior
      </button>
    </Dialog>
  );
}

/** actorName turns "member:mem_x" into the member's name when known. */
export function actorName(actor: string, names?: Map<string, string>): string {
  const [kind, id] = actor.split(":", 2);
  if (kind === "member" && id) return names?.get(id) ?? "Miembro del equipo";
  if (kind === "api_key") return "Integración (API key)";
  return actor;
}
