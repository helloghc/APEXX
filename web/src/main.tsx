import { startPanelLanguage } from "./i18n";
import { applyTheme, currentTheme } from "./adminThemes";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { Root } from "./Root";
import "./tokens.css";
import "./index.css";

const client = new QueryClient({ defaultOptions: { queries: { staleTime: 30_000, retry: 1 } } });

applyTheme(currentTheme());

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={client}>
      <Root />
    </QueryClientProvider>
  </StrictMode>,
);

void startPanelLanguage();
