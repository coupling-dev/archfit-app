import { readFileSync } from 'node:fs';

// The "Not used" words of design 5, matched as the engine matches them (internal/wire/wording): whole words in any case,
// a phrase across any white space, after the two gate names are removed.
const notUsed = JSON.parse(readFileSync(new URL('./not-used.json', import.meta.url), 'utf8')) as {
  words: string[];
  gateNames: string;
};
// not-used.json is a copy of the App's vendored engine file; a re-vendor that changes the list fails every spec here
// until the copy follows.
const vendored = readFileSync(new URL('../../internal/wire/wording/wording.go.txt', import.meta.url), 'utf8');
const vendoredWords = [...(/var notUsedWords = \[\]string\{([\s\S]*?)\n\}/.exec(vendored)?.[1] ?? '').matchAll(/"([^"]*)"/g)].map(
  (m) => m[1],
);
const vendoredGate = /regexp\.MustCompile\(`([^`]*)`\)/.exec(vendored)?.[1];
if (JSON.stringify(vendoredWords) !== JSON.stringify(notUsed.words) || vendoredGate !== notUsed.gateNames)
  throw new Error('web/e2e/not-used.json differs from internal/wire/wording/wording.go.txt: copy the list again');
const gateNames = new RegExp(notUsed.gateNames.replace(/^\(\?i\)/, ''), 'gi');
const patterns = notUsed.words.map((w) => {
  const phrase = w
    .split(/\s+/)
    .map((part) => part.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'))
    .join('[\\t\\n\\f\\r ]+');
  return { w, re: new RegExp(`(^|[^\\p{L}\\p{N}_])${phrase}($|[^\\p{L}\\p{N}_])`, 'iu') };
});

export function notUsedIn(text: string): string[] {
  const t = text.replace(gateNames, ' ');
  return patterns.filter((p) => p.re.test(t)).map((p) => p.w);
}
