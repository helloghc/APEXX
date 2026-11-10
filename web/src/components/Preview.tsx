import { useEffect, useRef, useState } from "react";
import { api } from "../api";
import { message } from "../useEditor";
import s from "./Preview.module.css";

export type Device = "mobile" | "tablet" | "desktop";
const widths: Record<Device, string> = { mobile: "390px", tablet: "820px", desktop: "100%" };

interface Props {
  site: string;
  handle: string;
  nonce: number;
  device: Device;
  selectedId: string | null;
}

/** Preview renders the draft in a sandboxed iframe (no scripts) and keeps the scroll position across refreshes. */
export function Preview({ site, handle, nonce, device, selectedId }: Props) {
  const frame = useRef<HTMLIFrameElement>(null);
  const scroll = useRef(0);
  const [html, setHtml] = useState<string>("");
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    try {
      scroll.current = frame.current?.contentWindow?.scrollY ?? scroll.current;
    } catch {
      /* cross-origin guard */
    }
    api
      .preview(site, handle)
      .then((h) => !cancelled && (setHtml(h), setError(null)))
      .catch((e) => !cancelled && setError(message(e)));
    return () => {
      cancelled = true;
    };
  }, [site, handle, nonce]);

  function onLoad() {
    const w = frame.current?.contentWindow;
    if (!w) return;
    w.scrollTo(0, scroll.current);
    highlight();
  }

  function highlight() {
    const doc = frame.current?.contentDocument;
    if (!doc) return;
    doc.querySelectorAll("[data-apexx-selected]").forEach((el) => {
      el.removeAttribute("data-apexx-selected");
      (el as HTMLElement).style.outline = "";
    });
    if (!selectedId) return;
    const el = doc.getElementById(selectedId);
    if (el) {
      el.setAttribute("data-apexx-selected", "");
      el.style.outline = "2px solid #2447d3";
      el.style.outlineOffset = "-2px";
    }
  }

  useEffect(highlight, [selectedId]);

  return (
    <section className={s.wrap} aria-label="Vista previa">
      {error && <p className={s.error} role="alert">No se pudo cargar la vista previa: {error}</p>}
      <div className={s.stage}>
        <iframe
          ref={frame}
          title="Vista previa de la página"
          className={s.frame}
          style={{ width: widths[device] }}
          sandbox="allow-same-origin"
          srcDoc={html}
          onLoad={onLoad}
          data-testid="preview"
        />
      </div>
    </section>
  );
}
