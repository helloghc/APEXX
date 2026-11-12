import { useEffect, useRef, useState } from "react";
import { coerce, fieldsFor, localError, type FieldSpec, type SectionIssues } from "../schema";
import type { Issue, Json, SectionDefinition, SectionInstance } from "../types";
import ui from "../ui.module.css";
import s from "./SettingsForm.module.css";

interface Props {
  section: SectionInstance | null;
  def: SectionDefinition | undefined;
  issues?: SectionIssues;
  onChange: (key: string, value: Json | undefined) => void;
  onBlockChange: (block: number, key: string, value: string) => void;
  onAddBlock: (type: string) => void;
  onRemoveBlock: (block: number) => void;
  onAudience?: (a: { mode: "only" | "hide"; groups: string[] } | null) => void;
}

export function SettingsForm({ section, def, issues, onChange, onBlockChange, onAddBlock, onRemoveBlock, onAudience }: Props) {
  if (!section) {
    return (
      <aside className={s.panel} aria-label="Ajustes">
        <p className={s.empty}>Elige una sección de la lista para editar sus textos, imágenes y opciones.</p>
      </aside>
    );
  }
  if (section.globalRef || !def) {
    return (
      <aside className={s.panel} aria-label="Ajustes">
        <h2>{section.globalRef ? "Sección global" : section.type}</h2>
        <p className={s.empty}>
          {section.globalRef
            ? "Esta sección se comparte entre páginas y se edita en su propio documento."
            : "Este tipo de sección no está en el catálogo. Quítala o reemplázala."}
        </p>
      </aside>
    );
  }
  const fields = fieldsFor(def.settingsSchema);
  const blocks = section.blocks ?? [];
  const allowed = def.blocks.allowed ?? [];
  return (
    <aside className={s.panel} aria-label={`Ajustes de ${def.name}`}>
      <header>
        <h2>{def.name}</h2>
        <p className={s.desc}>{def.description}</p>
      </header>
      {issues?.section.map((i) => <IssueLine key={i.code + i.path} issue={i} />)}
      <form className={s.form} onSubmit={(e) => e.preventDefault()}>
        {fields.map((f) => (
          <Field
            key={`${section.id}:${f.key}`}
            id={`f-${section.id}-${f.key}`}
            field={f}
            value={section.settings?.[f.key]}
            serverIssues={issues?.fields[f.key]}
            onCommit={(v) => onChange(f.key, v)}
          />
        ))}
      </form>
      {allowed.length > 0 && (
        <section className={s.blocks} aria-label="Bloques">
          <div className={s.blocksHead}>
            <h3>
              Bloques <span className={s.count}>{blocks.length} de {def.blocks.min}–{def.blocks.max}</span>
            </h3>
            <button className={ui.btn} disabled={blocks.length >= def.blocks.max} onClick={() => onAddBlock(allowed[0] as string)}>
              + Bloque
            </button>
          </div>
          {blocks.map((b, i) => {
            const keys = Array.from(new Set(["title", "text", ...Object.keys(b.settings ?? {})]));
            return (
              <fieldset key={b.id} className={s.block}>
                <legend>
                  {b.type} {i + 1}
                </legend>
                {keys.map((k) => (
                  <BlockInput
                    key={k}
                    id={`b-${b.id}-${k}`}
                    label={k === "title" ? "Título" : k === "text" ? "Texto" : k}
                    value={String(b.settings?.[k] ?? "")}
                    onCommit={(v) => onBlockChange(i, k, v)}
                  />
                ))}
                <button
                  className={`${ui.btn} ${ui.ghost} ${ui.danger}`}
                  disabled={blocks.length <= def.blocks.min}
                  onClick={() => onRemoveBlock(i)}
                >
                  Quitar bloque
                </button>
              </fieldset>
            );
          })}
        </section>
      )}
      {onAudience && <AudienceField section={section} onChange={onAudience} />}
      <footer className={s.budget}>
        Costo de la sección: {def.budget.htmlBytes.toLocaleString("es-MX")} B de HTML · {def.budget.jsBytes.toLocaleString("es-MX")} B de JS
      </footer>
    </aside>
  );
}

function IssueLine({ issue }: { issue: Issue }) {
  return (
    <p className={issue.blocking ? ui.error : ui.help} role={issue.blocking ? "alert" : undefined}>
      {issue.message}
    </p>
  );
}

/** useDebouncedCommit keeps typing local and saves after a pause or on blur. */
function useDebouncedCommit<T>(value: T, commit: (v: T) => void, delay = 700) {
  const [local, setLocal] = useState(value);
  const dirty = useRef(false);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  useEffect(() => {
    if (!dirty.current) setLocal(value);
  }, [value]);
  useEffect(() => () => clearTimeout(timer.current), []);
  const flush = (v: T) => {
    clearTimeout(timer.current);
    if (!dirty.current) return;
    dirty.current = false;
    commit(v);
  };
  const update = (v: T, immediate = false) => {
    setLocal(v);
    dirty.current = true;
    clearTimeout(timer.current);
    if (immediate) flush(v);
    else timer.current = setTimeout(() => flush(v), delay);
  };
  return { local, update, flush: () => flush(local) };
}

function Field({
  id,
  field,
  value,
  serverIssues,
  onCommit,
}: {
  id: string;
  field: FieldSpec;
  value: Json | undefined;
  serverIssues?: Issue[];
  onCommit: (v: Json | undefined) => void;
}) {
  const initial = value === undefined || value === null ? (field.kind === "checkbox" ? false : "") : value;
  const { local, update, flush } = useDebouncedCommit<string | boolean>(
    typeof initial === "boolean" ? initial : String(initial),
    (raw) => onCommit(coerce(field, raw)),
  );
  const parsed = coerce(field, local);
  const err = localError(field, parsed) ?? serverIssues?.find((i) => i.blocking)?.message ?? null;
  const hint = !err ? serverIssues?.find((i) => !i.blocking)?.message : undefined;
  const describedBy = err ? `${id}-err` : undefined;
  const common = {
    id,
    "aria-invalid": err ? ("true" as const) : ("false" as const),
    "aria-describedby": describedBy,
    "aria-required": field.required,
  };

  let control;
  switch (field.kind) {
    case "checkbox":
      control = <input {...common} type="checkbox" checked={Boolean(local)} onChange={(e) => update(e.target.checked, true)} />;
      break;
    case "select":
      control = (
        <select {...common} className={ui.input} value={String(local)} onChange={(e) => update(e.target.value, true)}>
          {!field.required && <option value="">—</option>}
          {field.options.map((o) => (
            <option key={String(o)} value={String(o)}>
              {OPTION_LABELS[String(o)] ?? String(o)}
            </option>
          ))}
        </select>
      );
      break;
    case "slider":
      control = (
        <div className={s.slider}>
          <input
            {...common}
            type="range"
            min={field.min}
            max={field.max}
            step={field.integer ? 1 : 0.05}
            value={local === "" ? String(field.defaultValue ?? field.min ?? 0) : String(local)}
            onChange={(e) => update(e.target.value)}
            onBlur={flush}
          />
          <output htmlFor={id}>{local === "" ? String(field.defaultValue ?? "") : String(local)}</output>
        </div>
      );
      break;
    case "number":
      control = (
        <input
          {...common}
          className={ui.input}
          type="number"
          min={field.min}
          max={field.max}
          step={field.integer ? 1 : "any"}
          value={String(local)}
          placeholder={field.defaultValue !== undefined ? String(field.defaultValue) : undefined}
          onChange={(e) => update(e.target.value)}
          onBlur={flush}
        />
      );
      break;
    case "textarea":
      control = (
        <textarea {...common} className={ui.input} rows={4} value={String(local)} onChange={(e) => update(e.target.value)} onBlur={flush} />
      );
      break;
    case "text":
      control = (
        <input {...common} className={ui.input} type="text" value={String(local)} onChange={(e) => update(e.target.value)} onBlur={flush} />
      );
      break;
    default:
      control = <p className={ui.help}>Este ajuste se edita con un control que llegará en la próxima versión.</p>;
  }

  return (
    <div className={ui.field}>
      <label className={ui.label} htmlFor={id}>
        {field.label} {field.required && <span className={ui.req} aria-hidden="true">*</span>}
      </label>
      {control}
      {field.maxLength !== undefined && typeof local === "string" && (field.kind === "text" || field.kind === "textarea") && (
        <span className={ui.help}>
          {[...local].length}/{field.maxLength}
        </span>
      )}
      {err && (
        <span id={`${id}-err`} className={ui.error}>
          {err}
        </span>
      )}
      {hint && <span className={ui.help}>{hint}</span>}
    </div>
  );
}

function BlockInput({ id, label, value, onCommit }: { id: string; label: string; value: string; onCommit: (v: string) => void }) {
  const { local, update, flush } = useDebouncedCommit(value, onCommit);
  return (
    <div className={ui.field}>
      <label className={ui.label} htmlFor={id}>
        {label}
      </label>
      <input id={id} className={ui.input} value={local} onChange={(e) => update(e.target.value)} onBlur={flush} />
    </div>
  );
}

/** AudienceField: who sees the section (everyone, only some customer groups, or all but them). */
function AudienceField({ section, onChange }: { section: SectionInstance; onChange: (a: { mode: "only" | "hide"; groups: string[] } | null) => void }) {
  const [groups, setGroups] = useState<{ id: string; name: string }[] | null>(null);
  useEffect(() => {
    let live = true;
    fetch("/v1/customer-groups", { credentials: "same-origin" }).then((r) => (r.ok ? r.json() : { items: [] })).then((j) => { if (live) setGroups(j.items ?? []); }).catch(() => live && setGroups([]));
    return () => { live = false; };
  }, []);
  const a = section.visibility?.audience;
  const mode = a ? a.mode : "all";
  const chosen = a?.groups ?? [];
  const set = (m: string, g: string[]) => onChange(m === "all" || g.length === 0 ? (m === "all" ? null : { mode: m as "only" | "hide", groups: g }) : { mode: m as "only" | "hide", groups: g });
  return (
    <fieldset className={s.field} data-audience-field>
      <legend>Quién la ve</legend>
      <select aria-label="Quién ve la sección" value={mode} onChange={(e) => set(e.target.value, e.target.value === "all" ? [] : chosen)}>
        <option value="all">Todos</option>
        <option value="only">Solo clientes de estos grupos</option>
        <option value="hide">Todos menos estos grupos</option>
      </select>
      {mode !== "all" && (groups === null ? <p>Cargando grupos…</p> : groups.length === 0 ? <p>Crea grupos de clientes en Clientes › Grupos.</p> : groups.map((g) => (
        <label key={g.id} style={{ display: "flex", gap: 6, alignItems: "center" }}><input type="checkbox" checked={chosen.includes(g.id)} onChange={(e) => set(mode, e.target.checked ? [...chosen, g.id] : chosen.filter((x) => x !== g.id))} /> {g.name}</label>
      )))}
      {mode !== "all" && chosen.length === 0 && <p>Elige al menos un grupo.</p>}
    </fieldset>
  );
}

/** Readable names for the choices sections offer (the stored value stays as is). */
const OPTION_LABELS: Record<string, string> = {
  left: "Izquierda", center: "Centro", right: "Derecha", full: "Ancho completo", split: "Dividido", text: "Solo texto",
  small: "Chico", medium: "Mediano", large: "Grande", tall: "Vertical", square: "Cuadrado", pill: "Redondeado",
};
