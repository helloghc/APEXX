import { useState } from "react";
import { errorMessage, request } from "@/api";
import { toast } from "@/components/ui/sonner";
import { href } from "@/router";

/** SandboxBar is always on screen inside the sandbox: nobody mistakes it for the real store. */
export function SandboxBar() {
  const [busy, setBusy] = useState(false);
  async function exit() {
    setBusy(true);
    try {
      await request("POST", "/v1/sandbox/exit");
      window.location.hash = "#inicio";
      window.location.reload();
    } catch (e) {
      toast.error(errorMessage(e));
      setBusy(false);
    }
  }
  return (
    <div role="note" className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1 bg-[#c2410c] px-4 py-2 text-sm text-white" data-sandbox-bar>
      <span>
        <strong>Sandbox.</strong> Lo que hagas aquí no llega a tu tienda real hasta que lo publiques.
      </span>
      <span className="flex flex-wrap items-center gap-3">
        <a className="underline underline-offset-2 hover:no-underline" href={href("configuracion", "sandbox")}>
          Publicar a producción
        </a>
        <button type="button" className="rounded border border-white/70 px-2 py-0.5 hover:bg-white/10 disabled:opacity-60" disabled={busy} onClick={() => void exit()}>
          {busy ? "Volviendo…" : "Volver a producción"}
        </button>
      </span>
    </div>
  );
}
