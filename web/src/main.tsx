import { startPanelLanguage } from "./i18n";
import { startFieldErrors } from "./fieldErrors";
import { applyTheme, currentTheme } from "./adminThemes";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { lazy, StrictMode, Suspense } from "react";
import { createRoot } from "react-dom/client";
import { Root } from "./Root";
import "./tokens.css";
import "./index.css";

// The superadmin loads only when asked for: the stores' panel never carries it.
const Superadmin = lazy(() => import("./Superadmin").then((m) => ({ default: m.Superadmin })));
const superadmin = () => window.location.hash.startsWith("#superadmin");
const startedAsSuperadmin = superadmin();
window.addEventListener("hashchange", () => {
  if (superadmin() !== startedAsSuperadmin) window.location.reload();
});

const client = new QueryClient({ defaultOptions: { queries: { staleTime: 30_000, retry: 1 } } });

applyTheme(currentTheme());

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={client}>
      {/* The platform's operators: their own sign-in, never a store's. */}
      {startedAsSuperadmin ? (
        <Suspense fallback={null}>
          <Superadmin />
        </Suspense>
      ) : (
        <Root />
      )}
    </QueryClientProvider>
  </StrictMode>,
);

void startPanelLanguage();
startFieldErrors();
