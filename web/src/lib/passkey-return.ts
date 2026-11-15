import { parseRoute, repoPage } from './route';

// Rebuild an allowlisted route instead of navigating to user-supplied URL text.
// Only the initiating request, owner action or policy editor in this repository qualifies.
export function passkeyReturn(fullName: string, value: string | null): string | undefined {
  if (!value || !value.startsWith('/') || value.startsWith('//') || /[\\\\\s#]/.test(value)) return;
  const [path, query = ''] = value.split('?');
  const route = parseRoute(path, query);
  if (!('fullName' in route) || route.fullName !== fullName) return;
  if (route.kind === 'request') return repoPage(fullName, '/requests/' + route.number);
  if (route.kind === 'owner') {
    return repoPage(fullName, '/owner/' + route.action + (route.pull ? '?pull=' + route.pull : ''));
  }
  if (route.kind === 'policy' && new URLSearchParams(query).get('edit') === '1') {
    return repoPage(fullName, '/policy?edit=1');
  }
}
