// Fails the build when a load is over budget (docs/design/ui-redesign.md, Bundle budget). First load is every script
// and stylesheet index.html references; the graph pages' lazy chunk (Graph-*) has a budget of its own.
import { readdirSync, readFileSync } from 'node:fs';
import { gzipSync } from 'node:zlib';

const out = new URL('../../internal/control/ui/spa/', import.meta.url);
const KB = 1024;
const size = (path) => gzipSync(readFileSync(new URL(path, out))).length;

const index = readFileSync(new URL('index.html', out), 'utf8');
const first = [...index.matchAll(/(?:src|href)="\/(assets\/[^"]+\.(?:js|css))"/g)].map((m) => m[1]);
const graph = readdirSync(new URL('assets/', out))
  .filter((f) => /^Graph-.*\.(js|css)$/.test(f))
  .map((f) => `assets/${f}`);

const loads = [
  { name: 'first load', files: first, budget: { js: 120 * KB, css: 20 * KB } },
  { name: 'graph chunk', files: graph, budget: { js: 540 * KB, css: 20 * KB } },
];
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
if (!graph.some((f) => f.endsWith('.js'))) {
  console.error('no Graph-*.js chunk; is the graph page still lazy?');
  over = true;
}
if (first.some((f) => graph.includes(f))) {
  console.error('the first load includes the graph chunk');
  over = true;
}
process.exit(over ? 1 : 0);
