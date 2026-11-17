import { readFileSync } from 'node:fs';
import { brotliCompressSync, gzipSync } from 'node:zlib';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import { defineConfig } from 'vitest/config';
import { licenses } from './scripts/licenses.ts';

// The production policy, from the file the Go test compares with the server's header.
const csp = readFileSync(new URL('./csp.txt', import.meta.url), 'utf8').trim();
const lic = licenses();

// precompress writes a .br and a .gz sibling of every script and stylesheet, so the Go server sends them as they are
// (internal/control/ui/spa.go). Brotli and gzip are node's own; no dependency.
const precompress = {
  name: 'precompress',
  apply: 'build' as const,
  generateBundle(
    this: { emitFile(file: { type: 'asset'; fileName: string; source: Uint8Array }): unknown },
    _: unknown,
    bundle: Record<string, { type: string; fileName: string; code?: string; source?: string | Uint8Array }>,
  ) {
    for (const item of Object.values(bundle)) {
      if (!/\.(js|css)$/.test(item.fileName)) continue;
      const raw = Buffer.from(item.type === 'chunk' ? item.code! : item.source!);
      this.emitFile({ type: 'asset', fileName: `${item.fileName}.br`, source: brotliCompressSync(raw) });
      this.emitFile({ type: 'asset', fileName: `${item.fileName}.gz`, source: gzipSync(raw, { level: 9 }) });
    }
  },
};

export default defineConfig({
  plugins: [svelte(), lic.app, precompress],
  build: {
    // The Go binary embeds this folder (internal/control/ui/spa.go). scripts/clean.mjs empties it first and keeps
    // its .gitignore, which emptyOutDir would delete.
    outDir: '../internal/control/ui/spa',
    emptyOutDir: false,
    // The polyfill is an inline script, which the CSP blocks. Every supported browser has modulepreload.
    modulePreload: { polyfill: false },
    // Inlined assets would be data: URLs; every asset stays a file served from 'self'.
    assetsInlineLimit: 0,
    // The layout worker is about 424 KB gzip, ELK all of it; scripts/size.mjs holds its budget.
    chunkSizeWarningLimit: 2000,
  },
  // The Home map's layout worker, an ES module like the app: its licences are collected by the same plugin.
  worker: { format: 'es', plugins: () => [lic.worker] },
  preview: { headers: { 'Content-Security-Policy': csp } },
  test: { include: ['src/**/*.test.ts'] },
});
