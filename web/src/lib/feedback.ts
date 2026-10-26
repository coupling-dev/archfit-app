import type { GLink } from './graph';

export const trustSentence =
  'Reports are customer-attested. Upload authentication does not independently verify report content.';

// Query parameters select only a pair already present in the admitted graph; they supply no graph facts.
export function selectedPair(links: GLink[], search: string): GLink | undefined {
  const query = new URLSearchParams(search);
  return links.find((link) => link.from === query.get('from') && link.to === query.get('to'));
}

export function pairHref(graphPage: string, from: string, to: string): string {
  return graphPage + '?' + new URLSearchParams({ from, to });
}
