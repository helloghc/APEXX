import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { fileURLToPath } from "node:url";

// APEXX_API points the dev/preview proxy at the Go server.
const target = process.env.APEXX_API ?? "http://127.0.0.1:8080";

export default defineConfig({
  base: "./",
  plugins: [react(), tailwindcss()],
  resolve: { alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) } },
  server: { proxy: { "/v1": target } },
  preview: { proxy: { "/v1": target } },
  build: { outDir: "dist", emptyOutDir: true, sourcemap: false, target: "es2022" },
  test: { environment: "node", include: ["src/**/*.test.ts"] },
});
