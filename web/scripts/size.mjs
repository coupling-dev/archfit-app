// Fails the build when a load is over budget (docs/archive/design/ui-redesign.md, Bundle budget). First load is every
// script and stylesheet index.html references; the Home map's layout worker has its own budget
// (layout.worker-*), which holds ELK. Each is held in gzip and in brotli: the server sends the .br sibling the build
// wrote, so its size is the wire size, and a missing sibling means the file would go out uncompressed.
import { existsSync, readdirSync, readFileSync } from 'node:fs';
import { gzipSync } from 'node:zlib';

const out = new URL('../../internal/control/ui/spa/', import.meta.url);
const KB = 1024;
const size = (path) => gzipSync(readFileSync(new URL(path, out))).length;
const wire = (path) => readFileSync(new URL(`${path}.br`, out)).length;

const index = readFileSync(new URL('index.html', out), 'utf8');
const first = [...index.matchAll(/(?:src|href)="\/(assets\/[^"]+\.(?:js|css))"/g)].map((m) => m[1]);
const chunk = (prefix) =>
  readdirSync(new URL('assets/', out))
    .filter((f) => f.startsWith(`${prefix}-`) && /\.(js|css)$/.test(f))
    .map((f) => `assets/${f}`);

const lazy = [
  {
    name: 'layout worker',
    prefix: 'layout.worker',
    files: chunk('layout.worker'),
    budget: { js: 480 * KB, css: 0 },
    br: { js: 380 * KB, css: 0 },
  },
];
// The signed-in pages are chunks of their own (src/lib/pages.ts); this budget fails when one is imported statically again.
const loads = [{ name: 'first load', files: first, budget: { js: 45 * KB, css: 10 * KB }, br: { js: 40 * KB, css: 9 * KB } }, ...lazy];
let over = false;
for (const load of loads) {
  for (const [encoding, measure, budget] of [['gzip', size, load.budget], ['brotli', wire, load.br]]) {
    for (const kind of ['js', 'css']) {
      const files = load.files.filter((f) => f.endsWith(`.${kind}`));
      const missing = encoding === 'brotli' ? files.filter((f) => !existsSync(new URL(`${f}.br`, out))) : [];
      if (missing.length) {
        over = true;
        console.error(`no brotli file for ${missing.join(', ')}; the server would send it uncompressed`);
        continue;
      }
      const used = files.reduce((n, f) => n + measure(f), 0);
      const line = `${load.name} ${kind}: ${(used / KB).toFixed(1)} KB ${encoding} of ${budget[kind] / KB} KB`;
      if (used > budget[kind]) {
        over = true;
        console.error(`over budget, ${line}`);
      } else {
        console.log(line);
      }
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
