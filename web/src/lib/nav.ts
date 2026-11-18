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

// linkURL is the in-app address of the link an event's target sits in, or null when it is not a link the App moves
// inside itself (a link that opens elsewhere or downloads, or one inAppURL leaves to the browser).
export function linkURL(target: EventTarget | null, current: URL): URL | null {
  const link = (target as Element | null)?.closest?.('a[href]');
  if (!(link instanceof HTMLAnchorElement) || (link.target && link.target !== '_self') || link.hasAttribute('download')) return null;
  return inAppURL(link.getAttribute('href') ?? '', current);
}
