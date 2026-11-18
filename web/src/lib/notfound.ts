import type { Route } from './route';

// missingSentence says what was not found on a repository the user can see. Only such a repository may be named: for
// one the user cannot see the page says nothing about it (App.svelte).
export function missingSentence(route: Route): string {
  if (!('fullName' in route)) return '';
  switch (route.kind) {
    case 'pull':
      return `Pull request #${route.number} was not found in ${route.fullName}.`;
    case 'request':
      return `Request #${route.number} was not found in ${route.fullName}.`;
    default:
      return `This page was not found in ${route.fullName}.`;
  }
}
