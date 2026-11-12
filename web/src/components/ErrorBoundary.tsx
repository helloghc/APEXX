import { Component, type ErrorInfo, type ReactNode } from "react";
import { AlertTriangle } from "lucide-react";
import { Button } from "@/components/ui/button";

// One broken screen must not take the whole panel down: the error stays in
// this box, the menu keeps working, and moving to another screen (a new
// resetKey) clears it. The browser console keeps the details for support.
export class ErrorBoundary extends Component<{ resetKey: string; children: ReactNode }, { error: Error | null }> {
  state = { error: null as Error | null };

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error("Pantalla con error:", error, info.componentStack);
  }

  componentDidUpdate(prev: { resetKey: string }) {
    if (prev.resetKey !== this.props.resetKey && this.state.error) this.setState({ error: null });
  }

  render() {
    if (!this.state.error) return this.props.children;
    return (
      <div className="mx-auto max-w-xl p-6" role="alert">
        <div className="flex items-start gap-3 rounded-lg border border-destructive/40 bg-destructive/5 p-4">
          <AlertTriangle className="mt-0.5 size-5 shrink-0 text-destructive" aria-hidden />
          <div className="grid gap-2">
            <h1 className="text-base font-semibold">Esta pantalla tuvo un error</h1>
            <p className="text-sm text-muted-foreground">Tus datos están a salvo. Puedes reintentar o ir a otra sección desde el menú. Si se repite, avísale a soporte.</p>
            <div className="flex gap-2">
              <Button size="sm" onClick={() => this.setState({ error: null })}>Reintentar</Button>
              <Button size="sm" variant="outline" onClick={() => window.location.reload()}>Recargar el panel</Button>
            </div>
          </div>
        </div>
      </div>
    );
  }
}
