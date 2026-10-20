import { execFileSync } from "node:child_process";
import { defineConfig, devices } from "@playwright/test";

// E2E_PORT names the preview port. Unset, the run takes a free port, so `make all` in two worktrees at once does not
// fail on a taken port. It is written back to the environment because each test worker loads this file again and must
// reach the same server: workers inherit the environment of the run.
process.env.E2E_PORT ||= execFileSync(
  process.execPath,
  [
    "-e",
    "const s=require('net').createServer().listen(0,'127.0.0.1',()=>{console.log(s.address().port);s.close()})",
  ],
  { encoding: "utf8" },
).trim();
const port = Number(process.env.E2E_PORT);

// The smoke test runs the production build under the production CSP (vite preview reads web/csp.txt) with the API
// mocked in the browser, so it needs no Go server and no GitHub.
export default defineConfig({
  testDir: "e2e",
  forbidOnly: !!process.env.CI,
  reporter: process.env.CI ? "github" : "list",
  use: { baseURL: `http://127.0.0.1:${port}` },
  webServer: {
    command: `vite preview --host 127.0.0.1 --port ${port} --strictPort`,
    url: `http://127.0.0.1:${port}`,
    reuseExistingServer: false,
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});
