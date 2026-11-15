import type { OwnerKind } from './owner';

// The pages of the web app. The server serves the same shell for each, whatever the repository; every API read of a
// repository page is authorized on its own.

export interface Repo {
  owner: string;
  name: string;
  fullName: string;
}

export type Route =
  | { kind: 'home' }
  | { kind: 'admin' }
  | ({ kind: 'repo' | 'policy' | 'passkeys' } & Repo)
  | ({ kind: 'pull' | 'request'; number: number } & Repo)
  | ({ kind: 'owner'; action: OwnerKind; pull: number } & Repo)
  | { kind: 'unknown' };

const repoPath =
  /^\/r\/([^/]+)\/([^/]+)(?:\/pull\/([1-9][0-9]{0,9})|\/(passkeys|policy)|\/requests\/([1-9][0-9]{0,9}))?\/?$/;

export function parseRoute(pathname: string, search = ''): Route {
  const owner = /^\/r\/([^/]+)\/([^/]+)\/owner\/(first|workflow|confirm|revert)\/?$/.exec(pathname);
  if (owner) {
    const repo = parseRoute(`/r/${owner[1]}/${owner[2]}`);
    if (repo.kind !== 'repo') return { kind: 'unknown' };
    const values = new URLSearchParams(search).getAll('pull');
    if (values.length > 1 || (values.length && !/^[1-9][0-9]{0,9}$/.test(values[0]))) return { kind: 'unknown' };
    const pull = values.length ? Number(values[0]) : 0;
    const action = owner[3];
    if (action !== 'first' && action !== 'workflow' && action !== 'confirm' && action !== 'revert') return { kind: 'unknown' };
    if ((action === 'workflow' && !pull) || ((action === 'confirm' || action === 'revert') && pull)) return { kind: 'unknown' };
    return { kind: 'owner', action, pull, owner: repo.owner, name: repo.name, fullName: repo.fullName };
  }
  if (pathname === '/') return { kind: 'home' };
  if (pathname === '/admin') return { kind: 'admin' };
  const m = repoPath.exec(pathname);
  if (!m) return { kind: 'unknown' };
  let repo: Repo;
  try {
    const owner = decodeURIComponent(m[1]);
    const name = decodeURIComponent(m[2]);
    // GitHub has no repository or owner named "." or "..", and either would climb out of an API path.
    if ([owner, name].some((p) => p === '.' || p === '..')) return { kind: 'unknown' };
    repo = { owner, name, fullName: `${owner}/${name}` };
  } catch {
    return { kind: 'unknown' };
  }
  if (m[4]) return { kind: m[4] as 'passkeys' | 'policy', ...repo };
  if (m[5]) return { kind: 'request', number: Number(m[5]), ...repo };
  if (m[3]) return { kind: 'pull', number: Number(m[3]), ...repo };
  return { kind: 'repo', ...repo };
}

// guideURL is the user guide on GitHub: the App's pages link to it so a customer can read how it works.
export const guideURL = 'https://github.com/coupling-dev/archfit-app/blob/main/docs/user-guide.md';

// repoPage is the App path of a repository page, for links; the server authorizes its reads on every request.
export function repoPage(fullName: string, suffix = ''): string {
  const [owner, name] = fullName.split('/');
  return `/r/${encodeURIComponent(owner)}/${encodeURIComponent(name)}${suffix}`;
}

// signInHref returns to the page the user is on after the GitHub sign-in.
export function signInHref(here: string): string {
  return '/auth/login?return=' + encodeURIComponent(here);
}

// safeHref lets a link from an API answer through only when it points into the App or to github.com, so a value that
// came from GitHub or a report can never become a javascript: or other-origin link. Anything else is no link.
export function safeHref(url: string | undefined | null): string | undefined {
  if (!url) return undefined;
  if (url.startsWith('/') && !url.startsWith('//') && !url.startsWith('/\\')) return url;
  if (url.startsWith('https://github.com/')) return url;
  return undefined;
}

// errorPlaceholder is the app-error meta tag's content in index.html; the server replaces it on every response.
const errorPlaceholder = '__APP_ERROR__';

// serverError is the sentence of a server error page: the server answered the page with an error status and put the
// sentence in the app-error meta tag. Empty on a page, and for the placeholder of a build served without the App.
export function serverError(content: string | null | undefined): string {
  return !content || content === errorPlaceholder ? '' : content;
}
