// The pages of the web app. The server serves the same shell for each and has already authorized the repository of
// a repository page; every other page of the App is still a server-rendered link.

export type Route =
  { kind: 'home' } | { kind: 'repo'; owner: string; name: string; fullName: string } | { kind: 'unknown' };

const repoPath = /^\/r\/([^/]+)\/([^/]+)\/?$/;

export function parseRoute(pathname: string): Route {
  if (pathname === '/') return { kind: 'home' };
  const m = repoPath.exec(pathname);
  if (!m) return { kind: 'unknown' };
  try {
    const owner = decodeURIComponent(m[1]);
    const name = decodeURIComponent(m[2]);
    // GitHub has no repository or owner named "." or "..", and either would climb out of an API path.
    if ([owner, name].some((p) => p === '.' || p === '..')) return { kind: 'unknown' };
    return { kind: 'repo', owner, name, fullName: `${owner}/${name}` };
  } catch {
    return { kind: 'unknown' };
  }
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
