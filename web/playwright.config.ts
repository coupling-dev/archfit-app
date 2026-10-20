import { defineConfig, devices } from '@playwright/test';

const port = 4173;

// The smoke test runs the production build under the production CSP (vite preview reads web/csp.txt) with the API
// mocked in the browser, so it needs no Go server and no GitHub.
export default defineConfig({
  testDir: 'e2e',
  forbidOnly: !!process.env.CI,
  reporter: process.env.CI ? 'github' : 'list',
  use: { baseURL: `http://127.0.0.1:${port}` },
  webServer: {
    command: `vite preview --host 127.0.0.1 --port ${port} --strictPort`,
    url: `http://127.0.0.1:${port}`,
    reuseExistingServer: false,
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
});
