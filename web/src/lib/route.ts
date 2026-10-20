// The pages of the web app. The server serves the same shell for each and has already authorized the repository of
// a repository page; the policy and consent pages are still server-rendered links.

export interface Repo {
  owner: string;
  name: string;
  fullName: string;
}

export type Route =
  | { kind: 'home' }
  | ({ kind: 'repo' | 'graph' } & Repo)
  | ({ kind: 'pull' | 'pull-graph'; number: number } & Repo)
  | { kind: 'unknown' };

const repoPath = /^\/r\/([^/]+)\/([^/]+)(?:\/pull\/([1-9][0-9]{0,9}))?(\/graph)?\/?$/;

export function parseRoute(pathname: string): Route {
  if (pathname === '/') return { kind: 'home' };
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
  const graph = m[4] !== undefined;
  if (m[3]) return { kind: graph ? 'pull-graph' : 'pull', number: Number(m[3]), ...repo };
  return { kind: graph ? 'graph' : 'repo', ...repo };
}

// repoPage is the App path of a repository page, for links; the server authorizes it again on every request.
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
