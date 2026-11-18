import { get as read } from 'svelte/store';
import { ApiError, getJSON, repoAPI } from './api';
import { visit } from './gate';
import { currentEpoch, drop, recall, remember } from './memory';
import { ownerViewPath } from './owner';
import type { Route } from './route';

// A page's first reads start beside /api/session instead of after it: the server authorizes each one on its own, and
// the page takes the answer once it mounts. A read no page takes (signed out, another failure) is dropped quietly.
const early = new Map<string, Promise<unknown>>();
// The paths whose early read is the last answer from memory, which is not a new answer to remember.
const recalled = new Set<string>();

// firstPaths are the reads a route's page makes first, with no input but the path.
export function firstPaths(route: Route): string[] {
  switch (route.kind) {
    case 'home':
      return ['/api/repos'];
    case 'repo':
      return [repoAPI(route.owner, route.name, 'home')];
    case 'policy':
      return [repoAPI(route.owner, route.name, 'policy')];
    case 'pull':
      return [repoAPI(route.owner, route.name, `pulls/${route.number}`)];
    case 'request':
      return [repoAPI(route.owner, route.name, `requests/${route.number}`)];
    case 'passkeys':
      return [repoAPI(route.owner, route.name, 'passkeys')];
    case 'owner':
      return [repoAPI(route.owner, route.name, ownerViewPath(route))];
    case 'admin':
      return ['/api/repos', '/api/admin/installations'];
  }
  return [];
}

// listed are the pages that show the last answer at once when the user comes back, and whose read starts when the
// pointer rests on a link to them. Every other page reads from the server each time it opens: an owner action, a
// request and a passkey list show what the user is about to answer.
const listed = new Set<Route['kind']>(['home', 'repo', 'policy', 'pull']);

// A read started for a link the user may follow. It is taken once by the visit it was for, and only while it is fresh:
// a pointer that rests on a link and then goes elsewhere must not leave an old answer to be shown as the current one.
const hintTTL = 10_000;
const hinted = new Map<string, { read: Promise<unknown>; at: number; epoch: number }>();

function start(path: string, get: (path: string) => Promise<unknown>): Promise<unknown> {
  const hint = hinted.get(path);
  hinted.delete(path);
  const answer = hint && Date.now() - hint.at < hintTTL && hint.epoch === currentEpoch() ? hint.read : get(path);
  answer.catch(() => {}); // the page that takes it handles the failure; one that never does must not log it
  return answer;
}

// prefetch starts the reads of route. For a visit inside the App (again), a page in `listed` takes the last answer from
// memory at once, and the result says when the current answers are in: true when any differs from what the page was
// given, or could not be read, so the page must be shown again; false when the page already shows the current answers.
// The result is null when nothing came from memory.
export function prefetch(
  route: Route,
  get: (path: string) => Promise<unknown> = getJSON,
  again = false,
): Promise<boolean> | null {
  const current: Promise<boolean>[] = [];
  const since = currentEpoch();
  for (const path of firstPaths(route)) {
    const answer = start(path, get);
    const last = again && listed.has(route.kind) ? recall(path) : undefined;
    recalled.delete(path);
    if (last === undefined) {
      early.set(path, answer);
      continue;
    }
    early.set(path, Promise.resolve(JSON.parse(last)));
    recalled.add(path);
    current.push(
      answer.then(
        (body) => {
          remember(path, body, since);
          if (JSON.stringify(body) === last) return false;
          early.set(path, answer);
          recalled.delete(path);
          return true;
        },
        () => {
          // Fail closed: a view that could not be refreshed is not shown again; the page shows why.
          drop(path);
          early.set(path, answer);
          recalled.delete(path);
          return true;
        },
      ),
    );
  }
  return current.length ? Promise.all(current).then((changed) => changed.some(Boolean)) : null;
}

// firstJSON is the early read of path, once; any later read of it goes to the server again.
export function firstJSON<T>(path: string): Promise<T> {
  const from = read(visit);
  const since = currentEpoch();
  const answer = early.get(path) ?? getJSON<T>(path);
  const fromMemory = recalled.delete(path);
  early.delete(path);
  return answer.then(
    (body) => {
      if (!fromMemory) remember(path, body, since);
      return body as T;
    },
    (e: unknown) => {
      // The read may have started in an earlier visit (the answer in memory was shown first): it answers for this one.
      throw e instanceof ApiError ? new ApiError(e.status, e.message, e.waiting, from) : e;
    },
  );
}

// A pointer resting on a link, or a link with the keyboard focus, starts that page's read, so the click finds it under
// way. Nothing starts where the browser asks for less data.
const quiet = () => {
  const link = (navigator as Navigator & { connection?: { saveData?: boolean } }).connection;
  return !!link?.saveData;
};

export function hint(route: Route, get: (path: string) => Promise<unknown> = getJSON): void {
  if (!listed.has(route.kind) || quiet()) return;
  const now = Date.now();
  for (const [path, h] of hinted) if (now - h.at >= hintTTL) hinted.delete(path);
  for (const path of firstPaths(route)) {
    if (hinted.has(path)) continue;
    const answer = get(path);
    answer.catch(() => {});
    hinted.set(path, { read: answer, at: now, epoch: currentEpoch() });
  }
}
