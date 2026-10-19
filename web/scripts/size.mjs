// Fails the build when the first load is over budget (docs/design/ui-redesign.md, Bundle budget).
// First load is every script and stylesheet index.html references; lazy chunks have budgets of their own later.
import { readFileSync } from 'node:fs';
import { gzipSync } from 'node:zlib';

const out = new URL('../../internal/control/ui/spa/', import.meta.url);
const budget = { js: 120 * 1024, css: 20 * 1024 };

const index = readFileSync(new URL('index.html', out), 'utf8');
const used = { js: 0, css: 0 };
for (const [, path] of index.matchAll(/(?:src|href)="\/(assets\/[^"]+\.(?:js|css))"/g)) {
  const kind = path.endsWith('.css') ? 'css' : 'js';
  used[kind] += gzipSync(readFileSync(new URL(path, out))).length;
}
let over = false;
for (const kind of ['js', 'css']) {
  const line = `${kind}: ${(used[kind] / 1024).toFixed(1)} KB gzip of ${budget[kind] / 1024} KB`;
  if (used[kind] > budget[kind]) {
    over = true;
    console.error(`over budget, ${line}`);
  } else {
    console.log(line);
  }
}
if (used.js === 0) {
  console.error('index.html references no script; is the build broken?');
  over = true;
}
process.exit(over ? 1 : 0);
