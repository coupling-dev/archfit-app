// A Vite plugin that writes assets/licenses-<hash>.txt: the licence of every package whose code is in the built
// bundle, as their licences ask (MIT keeps the notice with the copy; elkjs's EPL-2.0 names where its source is).
import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';

import type { Plugin, Rollup } from 'vite';

const packageOf = /[/\\]node_modules[/\\]((?:@[^/\\]+[/\\])?[^/\\]+)[/\\]/;

function notice(name: string, dir: string): string | null {
  const pkg = JSON.parse(readFileSync(join(dir, 'package.json'), 'utf8'));
  const file = readdirSync(dir).find((f) => /^(licen[cs]e|copying)(\.|$)/i.test(f));
  if (!file) return null;
  const source = typeof pkg.repository === 'string' ? pkg.repository : pkg.repository?.url;
  const head = [`${name}@${pkg.version} · ${pkg.license}`];
  if (source) head.push(`Source: ${source}`);
  return `${head.join('\n')}\n\n${readFileSync(join(dir, file), 'utf8').trim()}`;
}

// licenses returns two plugins that share what they found: worker, for the build of each worker, which Vite bundles
// before the app's own bundle is written, and app, which adds the app's packages and writes the file.
export function licenses(): { app: Plugin; worker: Plugin } {
  const dirs = new Map<string, string>();
  const collect = (bundle: Rollup.OutputBundle) => {
    for (const out of Object.values(bundle)) {
      for (const id of Object.keys('modules' in out ? out.modules : {})) {
        const m = packageOf.exec(id);
        if (m) dirs.set(m[1].replace('\\', '/'), id.slice(0, m.index + m[0].length - 1));
      }
    }
  };
  const worker: Plugin = {
    name: 'archfit-licenses-worker',
    apply: 'build',
    generateBundle(_, bundle) {
      collect(bundle);
    },
  };
  const app: Plugin = {
    name: 'archfit-licenses',
    apply: 'build',
    generateBundle(_, bundle) {
      collect(bundle);
      const notices = [...dirs]
        .sort(([a], [b]) => a.localeCompare(b))
        .map(([name, dir]) => {
          const text = notice(name, dir);
          if (!text) this.error(`${name} is in the bundle but ships no licence file`);
          return text;
        });
      this.emitFile({
        type: 'asset',
        name: 'licenses.txt', // hashed like every asset, which the server caches as immutable
        source: `Third-party software in the Archfit web app.\n\n${notices.join(`\n\n${'-'.repeat(80)}\n\n`)}\n`,
      });
    },
  };
  return { app, worker };
}
