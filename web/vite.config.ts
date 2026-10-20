import { readFileSync } from 'node:fs';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import { defineConfig } from 'vitest/config';
import { licenses } from './scripts/licenses.ts';

// The production policy, from the file the Go test compares with the server's header.
const csp = readFileSync(new URL('./csp.txt', import.meta.url), 'utf8').trim();

export default defineConfig({
  plugins: [svelte(), licenses()],
  build: {
    // The Go binary embeds this folder (internal/control/ui/spa.go). scripts/clean.mjs empties it first and keeps
    // its .gitignore, which emptyOutDir would delete.
    outDir: '../internal/control/ui/spa',
    emptyOutDir: false,
    // The polyfill is an inline script, which the CSP blocks. Every supported browser has modulepreload.
    modulePreload: { polyfill: false },
    // Inlined assets would be data: URLs; every asset stays a file served from 'self'.
    assetsInlineLimit: 0,
    // The graph pages' chunk is about 500 KB gzip, ELK most of it; scripts/size.mjs holds its budget.
    chunkSizeWarningLimit: 2000,
  },
  preview: { headers: { 'Content-Security-Policy': csp } },
  test: { include: ['src/**/*.test.ts'] },
});
