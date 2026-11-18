import { parseRoute } from './route';

// inAppURL is where a link goes inside the App, or null when the browser should follow it as before: another origin,
// a path the App has no page for (the server answers those), or a fragment on the page already shown.
export function inAppURL(href: string, current: URL): URL | null {
  let url: URL;
  try {
    url = new URL(href, current);
  } catch {
    return null;
  }
  if (url.origin !== current.origin) return null;
  if (parseRoute(url.pathname, url.search).kind === 'unknown') return null;
  if (url.hash && url.pathname === current.pathname && url.search === current.search) return null;
  return url;
}
