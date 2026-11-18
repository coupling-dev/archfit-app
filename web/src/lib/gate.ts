import { writable } from 'svelte/store';

// refused is the sentence of a repository API that answered 404. The server serves the same shell for every
// repository, so the app shows this as the server's error page did: instead of the route. A repository the user can
// see keeps its tabs and names what is missing (App.svelte); one that does not exist and one the user cannot see
// answer the same 404, so they look the same here, with no tabs.
export const refused = writable('');

// waiting is the account of a repository API that answered "waiting for approval": the app then shows the Waiting page
// instead of the route, the way it shows refused.
export const waiting = writable('');

// shown is the location the App is showing. A read still in flight from a page the user has left answers for its own
// location, and must not replace the page now shown.
export const shown = writable('');
