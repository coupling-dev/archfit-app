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
  | ({ kind: 'repo' | 'graph' | 'data-sharing' | 'policy' } & Repo)
  | ({ kind: 'pull' | 'pull-graph'; number: number } & Repo)
  | { kind: 'unknown' };

const repoPath = /^\/r\/([^/]+)\/([^/]+)(?:(?:\/pull\/([1-9][0-9]{0,9}))?(\/graph)?|\/(data-sharing|policy))\/?$/;

export function parseRoute(pathname: string): Route {
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
  if (m[5]) return { kind: m[5] as 'data-sharing' | 'policy', ...repo };
  const graph = m[4] !== undefined;
  if (m[3]) return { kind: graph ? 'pull-graph' : 'pull', number: Number(m[3]), ...repo };
  return { kind: graph ? 'graph' : 'repo', ...repo };
}

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
