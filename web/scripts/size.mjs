// Fails the build when a load is over budget (docs/design/ui-redesign.md, Bundle budget). First load is every script
// and stylesheet index.html references; each lazy chunk has a budget of its own: the graph pages' (Graph-*) and the
// policy page's (Policy-*), which holds CodeMirror.
import { readdirSync, readFileSync } from 'node:fs';
import { gzipSync } from 'node:zlib';

const out = new URL('../../internal/control/ui/spa/', import.meta.url);
const KB = 1024;
const size = (path) => gzipSync(readFileSync(new URL(path, out))).length;

const index = readFileSync(new URL('index.html', out), 'utf8');
const first = [...index.matchAll(/(?:src|href)="\/(assets\/[^"]+\.(?:js|css))"/g)].map((m) => m[1]);
const chunk = (prefix) =>
  readdirSync(new URL('assets/', out))
    .filter((f) => f.startsWith(`${prefix}-`) && /\.(js|css)$/.test(f))
    .map((f) => `assets/${f}`);

const lazy = [
  {
    name: 'graph chunk',
    prefix: 'Graph',
    files: chunk('Graph'),
    budget: { js: 540 * KB, css: 20 * KB },
  },
  {
    name: 'editor chunk',
    prefix: 'Policy',
    files: chunk('Policy'),
    budget: { js: 200 * KB, css: 20 * KB },
  },
];
const loads = [{ name: 'first load', files: first, budget: { js: 120 * KB, css: 20 * KB } }, ...lazy];
let over = false;
for (const load of loads) {
  for (const kind of ['js', 'css']) {
    const used = load.files.filter((f) => f.endsWith(`.${kind}`)).reduce((n, f) => n + size(f), 0);
    const line = `${load.name} ${kind}: ${(used / KB).toFixed(1)} KB gzip of ${load.budget[kind] / KB} KB`;
    if (used > load.budget[kind]) {
      over = true;
      console.error(`over budget, ${line}`);
    } else {
      console.log(line);
    }
  }
}
if (!first.some((f) => f.endsWith('.js'))) {
  console.error('index.html references no script; is the build broken?');
  over = true;
}
for (const load of lazy) {
  if (!load.files.some((f) => f.endsWith('.js'))) {
    console.error(`no ${load.prefix}-*.js chunk; is the page still lazy?`);
    over = true;
  }
  if (first.some((f) => load.files.includes(f))) {
    console.error(`the first load includes the ${load.name}`);
    over = true;
  }
}
process.exit(over ? 1 : 0);
