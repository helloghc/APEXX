import { defineConfig, devices } from "@playwright/test";

// The E2E run starts a real Go server (with the built admin embedded under /admin/)
// against PostgreSQL, creating a fresh store for every run. Run `npm run build` first
// so the embedded UI is current. See e2e/start-server.sh.
export default defineConfig({
  testDir: "e2e",
  timeout: 120_000,
  retries: 0,
  workers: 1,
  reporter: [["list"]],
  use: { baseURL: "http://127.0.0.1:8094/admin/", trace: "off", ...devices["Desktop Chrome"] },
  webServer: {
    command: "bash e2e/start-server.sh",
    url: "http://127.0.0.1:8094/readyz",
    timeout: 180_000,
    reuseExistingServer: false,
    stdout: "ignore",
    stderr: "pipe",
  },
});
