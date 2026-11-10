import { defineConfig, devices } from "@playwright/test";

// The E2E run starts the real Go server and serves the built editor with `vite preview`,
// which proxies /v1 to it. Ports are fixed so the test can also read the published page directly.
const API = "http://127.0.0.1:8098";

export default defineConfig({
  testDir: "e2e",
  timeout: 60_000,
  retries: 0,
  reporter: [["list"]],
  use: { baseURL: "http://127.0.0.1:4174", trace: "off", ...devices["Desktop Chrome"] },
  webServer: [
    {
      command: "go run ./cmd/apexx -addr 127.0.0.1:8098 -seed 200",
      cwd: "..",
      url: `${API}/healthz`,
      timeout: 120_000,
      reuseExistingServer: false,
    },
    {
      command: "npx vite preview --port 4174 --strictPort --host 127.0.0.1",
      url: "http://127.0.0.1:4174",
      env: { APEXX_API: API },
      timeout: 60_000,
      reuseExistingServer: false,
    },
  ],
});
