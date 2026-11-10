import type { Report } from "../types";
import type { Device } from "./Preview";
import ui from "../ui.module.css";
import s from "./TopBar.module.css";

interface Props {
  siteName: string;
  pageName: string;
  report: Report | null;
  saving: boolean;
  canUndo: boolean;
  canRedo: boolean;
  publishing: boolean;
  device: Device;
  onDevice: (d: Device) => void;
  onUndo: () => void;
  onRedo: () => void;
  onPublish: () => void;
  onHistory: () => void;
  onAccess: () => void;
}

const kb = (n: number) => `${(n / 1000).toLocaleString("es-MX", { maximumFractionDigits: 1 })} KB`;

export function blockingReasons(report: Report | null): string[] {
  return (report?.issues ?? []).filter((i) => i.blocking).map((i) => i.message);
}

export function TopBar(p: Props) {
  const r = p.report;
  const reasons = blockingReasons(r);
  const warnings = (r?.issues ?? []).length - reasons.length;
  const htmlRatio = r && r.limit.htmlBytes ? r.htmlBytes / r.limit.htmlBytes : 0;
  const jsRatio = r && r.limit.jsBytes ? r.budget.jsBytes / r.limit.jsBytes : 0;
  const tone = (ratio: number) => (ratio > 1 ? ui.bad : ratio >= 0.8 ? ui.warn : ui.ok);
  const status = reasons.length ? "bad" : warnings ? "warn" : "ok";

  return (
    <header className={s.bar}>
      <div className={s.title}>
        <strong>{p.siteName}</strong>
        <span className={s.page}>{p.pageName}</span>
        <span className={s.saving} aria-live="polite">
          {p.saving ? "Guardando…" : "Guardado"}
        </span>
      </div>

      <div className={s.health} role="group" aria-label="Estado de la página" data-testid="health">
        <span className={`${ui.pill} ${ui[status]}`}>
          {status === "ok" ? "Lista para publicar" : status === "warn" ? `${warnings} aviso(s)` : `${reasons.length} bloqueo(s)`}
        </span>
        {r && (
          <>
            <span className={`${ui.pill} ${tone(htmlRatio)}`} title="HTML comprimido frente al límite de la plantilla">
              HTML {kb(r.htmlBytes)} / {kb(r.limit.htmlBytes)}
            </span>
            <span className={`${ui.pill} ${tone(jsRatio)}`} title="JavaScript de las secciones frente al límite">
              JS {kb(r.budget.jsBytes)} / {kb(r.limit.jsBytes)}
            </span>
          </>
        )}
      </div>

      <div className={s.actions}>
        <div className={s.devices} role="radiogroup" aria-label="Ancho de la vista previa">
          {(
            [
              ["mobile", "Móvil"],
              ["tablet", "Tablet"],
              ["desktop", "Escritorio"],
            ] as const
          ).map(([d, label]) => (
            <button
              key={d}
              role="radio"
              aria-checked={p.device === d}
              className={`${s.device} ${p.device === d ? s.on : ""}`}
              onClick={() => p.onDevice(d)}
            >
              {label}
            </button>
          ))}
        </div>
        <button className={ui.btn} onClick={p.onUndo} disabled={!p.canUndo} aria-keyshortcuts="Control+Z" title="Deshacer (Ctrl+Z)">
          ↶ Deshacer
        </button>
        <button className={ui.btn} onClick={p.onRedo} disabled={!p.canRedo} aria-keyshortcuts="Control+Shift+Z" title="Rehacer (Ctrl+Shift+Z)">
          ↷ Rehacer
        </button>
        <button className={ui.btn} onClick={p.onHistory}>
          Historial
        </button>
        <button className={`${ui.btn} ${ui.ghost}`} onClick={p.onAccess}>
          Acceso
        </button>
        <button
          className={`${ui.btn} ${ui.primary}`}
          onClick={p.onPublish}
          disabled={reasons.length > 0 || p.publishing || p.saving}
          aria-describedby={reasons.length ? "publish-reasons" : undefined}
        >
          {p.publishing ? "Publicando…" : "Publicar"}
        </button>
      </div>
      {reasons.length > 0 && (
        <ul id="publish-reasons" className={s.reasons}>
          {reasons.map((m, i) => (
            <li key={i}>{m}</li>
          ))}
        </ul>
      )}
    </header>
  );
}
