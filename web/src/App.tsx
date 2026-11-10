import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useState } from "react";
import { api } from "./api";
import { Access, History, SectionPicker } from "./components/Dialogs";
import { Preview, type Device } from "./components/Preview";
import { SectionList } from "./components/SectionList";
import { SettingsForm } from "./components/SettingsForm";
import { TopBar } from "./components/TopBar";
import {
  addBlock,
  addSection,
  duplicateSection,
  mainSections,
  moveSection,
  newId,
  removeBlock,
  removeSection,
  setBlockSetting,
  setSetting,
} from "./patch";
import { groupIssues } from "./schema";
import type { Release } from "./types";
import { message, useEditor } from "./useEditor";
import s from "./App.module.css";
import ui from "./ui.module.css";

const params = new URLSearchParams(window.location.search);
const SITE = params.get("site") ?? "demo";
const HANDLE = params.get("page") ?? "index";

interface Toast {
  text: string;
  undo?: boolean;
  tone: "ok" | "bad";
}

export function App() {
  const qc = useQueryClient();
  const ed = useEditor(SITE, HANDLE);
  const { template, report } = ed.state;
  const defsQ = useQuery({ queryKey: ["sections"], queryFn: api.sections });
  const releasesQ = useQuery({ queryKey: ["releases", SITE], queryFn: () => api.releases(SITE) });
  const defs = useMemo(() => new Map((defsQ.data ?? []).map((d) => [d.type, d])), [defsQ.data]);

  const [selected, setSelected] = useState<string | null>(null);
  const [device, setDevice] = useState<Device>("desktop");
  const [picker, setPicker] = useState(false);
  const [history, setHistory] = useState(false);
  const [access, setAccess] = useState(false);
  const [publishing, setPublishing] = useState(false);
  const [toast, setToast] = useState<Toast | null>(null);

  const sections = template ? mainSections(template) : [];
  const selIndex = sections.findIndex((x) => x.id === selected);
  const selSection = selIndex >= 0 ? (sections[selIndex] ?? null) : null;
  const issues = useMemo(() => groupIssues(report?.issues), [report]);

  useEffect(() => {
    if (!selected && sections[0]) setSelected(sections[0].id);
  }, [sections, selected]);

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      const t = e.target as HTMLElement;
      if (t.closest("input, textarea, select")) return;
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "z") {
        e.preventDefault();
        void (e.shiftKey ? ed.redo() : ed.undo());
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [ed]);

  useEffect(() => {
    if (!toast) return;
    const t = setTimeout(() => setToast(null), toast.undo ? 12000 : 6000);
    return () => clearTimeout(t);
  }, [toast]);

  async function publish() {
    setPublishing(true);
    try {
      const r = await api.publish(SITE, "Publicado desde el editor");
      setToast({ text: `Publicado en ${(r.release.durationMicros / 1000).toFixed(2)} ms`, undo: true, tone: "ok" });
      void qc.invalidateQueries({ queryKey: ["releases", SITE] });
    } catch (e) {
      setToast({ text: `No se publicó: ${message(e)}`, tone: "bad" });
    } finally {
      setPublishing(false);
    }
  }

  async function rollback() {
    setPublishing(true);
    try {
      const r = await api.rollback(SITE);
      setToast({ text: `Se restauró la publicación ${r.release.revisionId}`, tone: "ok" });
      void qc.invalidateQueries({ queryKey: ["releases", SITE] });
    } catch (e) {
      setToast({ text: `No se pudo deshacer: ${message(e)}`, tone: "bad" });
    } finally {
      setPublishing(false);
    }
  }

  if (!template) {
    return (
      <main className={s.loading}>
        {ed.state.error ? (
          <div className={s.loadError} role="alert">
            <p>No se pudo abrir la página: {ed.state.error}</p>
            <div className={s.row}>
              <button className={ui.btn} onClick={() => void ed.reload()}>Reintentar</button>
              <button className={ui.btn} onClick={() => setAccess(true)}>Acceso</button>
            </div>
          </div>
        ) : (
          <p>Cargando editor…</p>
        )}
        <Access open={access} onClose={() => setAccess(false)} onSaved={() => void ed.reload()} />
      </main>
    );
  }

  return (
    <div className={s.app}>
      <TopBar
        siteName={SITE}
        pageName={template.name || template.handle}
        report={report}
        saving={ed.state.saving}
        canUndo={ed.state.canUndo}
        canRedo={ed.state.canRedo}
        publishing={publishing}
        device={device}
        onDevice={setDevice}
        onUndo={() => void ed.undo()}
        onRedo={() => void ed.redo()}
        onPublish={() => void publish()}
        onHistory={() => setHistory(true)}
        onAccess={() => setAccess(true)}
      />
      {ed.state.conflict && (
        <div className={`${ui.banner} ${ui.warn}`} role="alert">
          Otra persona cambió esta página. Recarga para ver la última versión; tus cambios sin guardar se perderán.
          <button className={ui.btn} onClick={() => void ed.reload()}>Recargar</button>
        </div>
      )}
      {ed.state.error && (
        <div className={`${ui.banner} ${ui.bad}`} role="alert">
          {ed.state.error}
          <button className={ui.btn} onClick={ed.clearError}>Cerrar</button>
        </div>
      )}
      {issues.page.length > 0 && (
        <div className={`${ui.banner} ${issues.page.some((i) => i.blocking) ? ui.bad : ui.warn}`}>
          {issues.page.map((i) => i.message).join(" · ")}
        </div>
      )}
      <div className={s.main}>
        <SectionList
          sections={sections}
          defs={defs}
          issues={issues.bySection}
          selected={selected}
          onSelect={setSelected}
          onMove={(from, to) => void ed.apply(moveSection(template, from, to))}
          onDuplicate={(i) => {
            const id = newId("sec");
            void ed.apply(duplicateSection(template, i, id));
            setSelected(id);
          }}
          onRemove={(i) => {
            const next = sections[i + 1] ?? sections[i - 1];
            void ed.apply(removeSection(template, i));
            setSelected(next?.id ?? null);
          }}
          onAdd={() => setPicker(true)}
        />
        <Preview site={SITE} handle={HANDLE} nonce={ed.state.previewNonce} device={device} selectedId={selected} />
        <SettingsForm
          section={selSection}
          def={selSection?.type ? defs.get(selSection.type) : undefined}
          issues={issues.bySection.get(selIndex)}
          onChange={(k, v) => void ed.apply(setSetting(template, selIndex, k, v))}
          onBlockChange={(b, k, v) => void ed.apply(setBlockSetting(template, selIndex, b, k, v))}
          onAddBlock={(type) => void ed.apply(addBlock(template, selIndex, type))}
          onRemoveBlock={(b) => void ed.apply(removeBlock(template, selIndex, b))}
        />
      </div>

      <SectionPicker
        open={picker}
        defs={defsQ.data ?? []}
        kind={template.kind}
        onClose={() => setPicker(false)}
        onPick={(d) => {
          const id = newId("sec");
          void ed.apply(addSection(template, d, selIndex >= 0 ? selIndex + 1 : sections.length, id));
          setSelected(id);
          setPicker(false);
        }}
      />
      <History
        open={history}
        items={(releasesQ.data ?? []) as Release[]}
        busy={publishing}
        onClose={() => setHistory(false)}
        onRollback={() => void rollback()}
      />
      <Access
        open={access}
        onClose={() => setAccess(false)}
        onSaved={() => {
          void ed.reload();
          void qc.invalidateQueries();
        }}
      />

      <div className={s.toastHost} aria-live="polite">
        {toast && (
          <div className={`${s.toast} ${toast.tone === "bad" ? s.toastBad : ""}`} role="status" data-testid="toast">
            <span>{toast.text}</span>
            {toast.undo && (
              <button className={ui.btn} onClick={() => void rollback()} disabled={publishing}>
                Deshacer publicación
              </button>
            )}
            <button className={ui.iconBtn} onClick={() => setToast(null)} aria-label="Cerrar aviso">✕</button>
          </div>
        )}
      </div>
      <p className="sr-only" aria-live="polite">{ed.state.announcement}</p>
    </div>
  );
}
