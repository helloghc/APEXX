import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useState } from "react";
import { api, type Me } from "./api";
import { History, SectionPicker } from "./components/Dialogs";
import { toast } from "./components/ui/sonner";
import { can } from "./perms";
import { Preview, type Device } from "./components/Preview";
import { SectionList } from "./components/SectionList";
import { SettingsForm } from "./components/SettingsForm";
import { StylePanel } from "./components/StylePanel";
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
  setAudience,
  locate,
  regionSections,
  type Region,
} from "./patch";
import { groupIssues } from "./schema";
import type { Release } from "./types";
import { message, useEditor } from "./useEditor";
import s from "./App.module.css";
import ui from "./ui.module.css";

/** Editor is the visual builder for one page of one site, shown inside the admin shell. */
export function Editor({ site: SITE, handle: HANDLE, me }: { site: string; handle: string; me: Me }) {
  const qc = useQueryClient();
  const ed = useEditor(SITE, HANDLE);
  const { template, report } = ed.state;
  const defsQ = useQuery({ queryKey: ["sections"], queryFn: api.sections });
  const releasesQ = useQuery({ queryKey: ["releases", SITE], queryFn: () => api.releases(SITE) });
  const defs = useMemo(() => new Map((defsQ.data ?? []).map((d) => [d.type, d])), [defsQ.data]);
  const membersQ = useQuery({ queryKey: ["members"], queryFn: api.members, enabled: can(me, "team", "read") });
  const names = useMemo(() => {
    const m = new Map<string, string>();
    for (const x of membersQ.data?.items ?? []) m.set(x.id, x.name || x.email);
    if (me.kind === "member") m.set(me.id, me.name || me.email || "Tú");
    return m;
  }, [membersQ.data, me]);
  const canPublish = can(me, "builder", "admin");

  const [selected, setSelected] = useState<string | null>(null);
  // On a phone the three panes are tabs: the list, the preview and the form.
  const [pane, setPane] = useState<"sections" | "preview" | "settings">("sections");
  const [device, setDevice] = useState<Device>(() => (typeof window !== "undefined" && window.matchMedia?.("(max-width: 719px)").matches ? "mobile" : "desktop"));
  const [picker, setPicker] = useState(false);
  const [history, setHistory] = useState(false);
  const [style, setStyle] = useState(false);
  const [publishing, setPublishing] = useState(false);

  const sections = template ? mainSections(template) : [];
  // The selected section can live in the header or footer region too.
  const sel = template ? locate(template, selected) : null;
  const selRegion: Region = sel?.region ?? "main";
  const selIndex = sel?.index ?? -1;
  const selSection = sel?.section ?? null;
  const mainIndex = selRegion === "main" ? selIndex : -1;
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

  async function publish() {
    setPublishing(true);
    // "Deshacer publicación" only makes sense when there is a previous release to go back to.
    const hadPrevious = (releasesQ.data?.length ?? 0) > 0;
    try {
      const r = await api.publish(SITE, "Publicado desde el editor");
      toast.success(`Publicado en ${(r.release.durationMicros / 1000).toFixed(2)} ms`, {
        duration: 12000,
        action: hadPrevious ? { label: "Deshacer publicación", onClick: () => void rollback() } : undefined,
      });
      void qc.invalidateQueries({ queryKey: ["releases", SITE] });
    } catch (e) {
      toast.error(`No se publicó: ${message(e)}`);
    } finally {
      setPublishing(false);
    }
  }

  async function rollback() {
    setPublishing(true);
    try {
      const r = await api.rollback(SITE);
      toast.success(`Se restauró la publicación ${r.release.revisionId}`);
      void qc.invalidateQueries({ queryKey: ["releases", SITE] });
    } catch (e) {
      toast.error(`No se pudo deshacer: ${message(e)}`);
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
            </div>
          </div>
        ) : (
          <p>Cargando editor…</p>
        )}
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
        onPublish={() => (canPublish ? void publish() : toast.error("Tu acceso al diseño no permite publicar. Pide nivel Admin en Diseño de la tienda."))}
        onHistory={() => setHistory(true)}
        onStyle={can(me, "builder", "edit") ? () => setStyle(true) : undefined}
        liveUrl={`/s/${encodeURIComponent(me.tenant)}/${encodeURIComponent(SITE)}/${encodeURIComponent(HANDLE)}`}
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
      <div className={s.tabs} role="tablist" aria-label="Diseñador">
        {([["sections", "Secciones"], ["preview", "Vista previa"], ["settings", "Editar"]] as const).map(([k, label]) => (
          <button key={k} type="button" role="tab" aria-selected={pane === k} className={s.tab} onClick={() => setPane(k)}>{label}</button>
        ))}
      </div>
      <div className={s.main} data-pane={pane}>
        <SectionList
          header={regionSections(template, "header")}
          footer={regionSections(template, "footer")}
          sections={sections}
          defs={defs}
          issues={issues.bySection}
          selected={selected}
          onSelect={(id) => { setSelected(id); if (window.matchMedia("(max-width: 719px)").matches) setPane("settings"); }}
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
          issues={selRegion === "main" ? issues.bySection.get(selIndex) : undefined}
          onChange={(k, v) => void ed.apply(setSetting(template, selIndex, k, v, selRegion))}
          onBlockChange={(b, k, v) => void ed.apply(setBlockSetting(template, selIndex, b, k, v, selRegion))}
          onAddBlock={(type) => void ed.apply(addBlock(template, selIndex, type, undefined, selRegion))}
          onRemoveBlock={(b) => void ed.apply(removeBlock(template, selIndex, b, selRegion))}
          onAudience={(a) => void ed.apply(setAudience(template, selIndex, a, selRegion))}
        />
      </div>

      <SectionPicker
        open={picker}
        defs={defsQ.data ?? []}
        kind={template.kind}
        onClose={() => setPicker(false)}
        onPick={(d) => {
          const id = newId("sec");
          void ed.apply(addSection(template, d, mainIndex >= 0 ? mainIndex + 1 : sections.length, id));
          setSelected(id);
          setPicker(false);
        }}
      />
      <StylePanel
        open={style}
        onClose={() => setStyle(false)}
        site={SITE}
        template={template}
        onEdit={(e) => void ed.apply(e)}
        onApplied={(prev, name) => {
          setStyle(false);
          setSelected(null);
          void ed.reload();
          toast.success(`Tema ${name} aplicado al borrador`, {
            duration: 12000,
            action: prev
              ? {
                  label: "Volver a como estaba",
                  onClick: () =>
                    void api
                      .restoreRevision(SITE, prev)
                      .then(() => ed.reload())
                      .catch((e) => toast.error(`No se pudo restaurar: ${message(e)}`)),
                }
              : undefined,
          });
        }}
      />
      <History
        open={history}
        items={(releasesQ.data ?? []) as Release[]}
        names={names}
        busy={publishing || !canPublish}
        onClose={() => setHistory(false)}
        onRollback={() => void rollback()}
      />
      <p className="sr-only" aria-live="polite">{ed.state.announcement}</p>
    </div>
  );
}
