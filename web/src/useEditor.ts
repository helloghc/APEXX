import { useCallback, useEffect, useRef, useState } from "react";
import { api, ApiError } from "./api";
import { applyPatch } from "./jsonpatch";
import type { Edit } from "./patch";
import type { Report, Template } from "./types";

export interface EditorState {
  template: Template | null;
  version: number;
  report: Report | null;
  saving: boolean;
  conflict: boolean;
  error: string | null;
  canUndo: boolean;
  canRedo: boolean;
  previewNonce: number;
  announcement: string;
}

/**
 * useEditor owns the draft. Every change is a JSON Patch sent with ifVersion,
 * applied optimistically and reverted to the server state if it fails.
 * Patches are serialized so versions never race.
 */
export function useEditor(site: string, handle: string) {
  const [state, setState] = useState<EditorState>({
    template: null,
    version: 0,
    report: null,
    saving: false,
    conflict: false,
    error: null,
    canUndo: false,
    canRedo: false,
    previewNonce: 0,
    announcement: "",
  });
  const server = useRef<{ template: Template | null; version: number }>({ template: null, version: 0 });
  const queue = useRef<Promise<void>>(Promise.resolve());
  const undo = useRef<Edit[]>([]);
  const redo = useRef<Edit[]>([]);

  const load = useCallback(async () => {
    try {
      const d = await api.draft(site, handle);
      server.current = { template: d.template, version: d.version };
      undo.current = [];
      redo.current = [];
      setState((s) => ({
        ...s,
        template: d.template,
        version: d.version,
        conflict: false,
        error: null,
        canUndo: false,
        canRedo: false,
        previewNonce: s.previewNonce + 1,
      }));
      // A no-op test patch returns the validation report for the current draft.
      const r = await api.patch(site, handle, [{ op: "test", path: "/handle", value: handle }], d.version);
      server.current.version = r.version;
      setState((s) => ({ ...s, report: r.report, version: r.version }));
    } catch (e) {
      setState((s) => ({ ...s, error: message(e) }));
    }
  }, [site, handle]);

  useEffect(() => {
    void load();
  }, [load]);

  const send = useCallback(
    (edit: Edit, kind: "do" | "undo" | "redo") => {
      setState((s) =>
        s.template ? { ...s, template: applyPatch(s.template, edit.ops), saving: true, error: null } : s,
      );
      queue.current = queue.current.then(async () => {
        try {
          const r = await api.patch(site, handle, edit.ops, server.current.version);
          server.current = { template: r.template, version: r.version };
          const inverse: Edit = { label: edit.label, ops: edit.inverse, inverse: edit.ops };
          if (kind === "do") {
            undo.current.push(edit);
            redo.current = [];
          } else if (kind === "undo") redo.current.push(inverse);
          else undo.current.push(inverse);
          setState((s) => ({
            ...s,
            template: r.template,
            version: r.version,
            report: r.report,
            saving: false,
            canUndo: undo.current.length > 0,
            canRedo: redo.current.length > 0,
            previewNonce: s.previewNonce + 1,
            announcement: `${edit.label}: guardado`,
          }));
        } catch (e) {
          const conflict = e instanceof ApiError && e.problem.status === 409;
          setState((s) => ({
            ...s,
            template: server.current.template,
            saving: false,
            conflict,
            error: conflict ? null : message(e),
            announcement: `${edit.label}: no se guardó`,
          }));
        }
      });
      return queue.current;
    },
    [site, handle],
  );

  const apply = useCallback((edit: Edit | null) => (edit ? send(edit, "do") : Promise.resolve()), [send]);

  const doUndo = useCallback(() => {
    const e = undo.current.pop();
    if (!e) return Promise.resolve();
    return send({ label: "Deshacer", ops: e.inverse, inverse: e.ops }, "undo");
  }, [send]);

  const doRedo = useCallback(() => {
    const e = redo.current.pop();
    if (!e) return Promise.resolve();
    return send({ label: "Rehacer", ops: e.inverse, inverse: e.ops }, "redo");
  }, [send]);

  const refreshPreview = useCallback(() => setState((s) => ({ ...s, previewNonce: s.previewNonce + 1 })), []);
  const clearError = useCallback(() => setState((s) => ({ ...s, error: null })), []);

  return { state, apply, undo: doUndo, redo: doRedo, reload: load, refreshPreview, clearError };
}

export function message(e: unknown): string {
  if (e instanceof ApiError) {
    const p = e.problem;
    if (p.status === 401 || p.status === 403) return "No tienes acceso. Revisa tu clave de API en Acceso.";
    const fields = p.errors?.map((f) => f.message).join("; ");
    return [p.detail || p.title || p.code, fields].filter(Boolean).join(" · ");
  }
  if (e instanceof TypeError) return "No hay conexión con el servidor. Revisa tu red e intenta de nuevo.";
  return e instanceof Error ? e.message : String(e);
}
