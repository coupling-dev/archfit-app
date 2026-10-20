import { getJSON, repoAPI } from './api';
import type { Route } from './route';

// A page's first reads start beside /api/session instead of after it: the server authorizes each one on its own, and
// the page takes the answer once it mounts. A read no page takes (signed out, another failure) is dropped quietly.
const early = new Map<string, Promise<unknown>>();

// firstPaths are the reads a route's page makes first, with no input but the path.
export function firstPaths(route: Route): string[] {
  switch (route.kind) {
    case 'home':
      return ['/api/repos'];
    case 'repo':
      return [repoAPI(route.owner, route.name, 'overview'), repoAPI(route.owner, route.name, 'findings')];
    case 'graph':
      return [repoAPI(route.owner, route.name, 'graph')];
    case 'pull':
      return [repoAPI(route.owner, route.name, `pulls/${route.number}`)];
    case 'pull-graph':
      return [repoAPI(route.owner, route.name, `pulls/${route.number}/graph`)];
    case 'data-sharing':
      return [repoAPI(route.owner, route.name, 'data-sharing')];
  }
  return [];
}

export function prefetch(route: Route, get: (path: string) => Promise<unknown> = getJSON): void {
  for (const path of firstPaths(route)) {
    const read = get(path);
    read.catch(() => {}); // the page that takes it handles the failure; one that never does must not log it
    early.set(path, read);
  }
}

// firstJSON is the early read of path, once; any later read of it goes to the server again.
export function firstJSON<T>(path: string): Promise<T> {
  const read = early.get(path);
  if (!read) return getJSON<T>(path);
  early.delete(path);
  return read as Promise<T>;
}
