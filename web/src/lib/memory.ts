// The App's memory of the last answer to a few reads, so a page the user comes back to shows at once while the current
// answer is read again (lib/early). It lives in this document only: nothing is written to the browser's storage, so a
// reload, a sign-out and a closed tab all forget it.
//
// Only a read of the repository list, a repository's Home, its policy and a pull request page is remembered. Never an
// owner action, a request, a passkey list, the session or any write: those must be read from the server every time.
const remembered = /^\/api\/repos(?:\/[^/?#]+\/[^/?#]+\/(?:home|policy|pulls\/[1-9][0-9]*))?$/;
const limit = 40;

// An answer is kept as text and handed out parsed, so a page that changes what it got cannot change the memory.
const answers = new Map<string, string>();
let login: string | null = null;
// epoch counts the times everything was dropped. A read started in an earlier epoch (a read in flight, or one a link's
// pointer started) answered before the user lost access, so it is neither remembered nor shown again.
let epoch = 0;

export const currentEpoch = () => epoch;

const keyOf = (path: string) => `${login}\n${path}`;

// signedIn names the user the App is showing; another user (or none) starts with an empty memory.
export function signedIn(next: string | null): void {
  if (next !== login) forget();
  login = next;
}

// forget drops every answer: the session ended or a read was refused, so nothing seen before may be shown again.
export function forget(): void {
  epoch++;
  answers.clear();
}

export function drop(path: string): void {
  answers.delete(keyOf(path));
}

export function remember(path: string, body: unknown, since = epoch): void {
  if (!login || since !== epoch || !remembered.test(path)) return;
  const key = keyOf(path);
  answers.delete(key); // re-insert last, so the oldest answer is the one that goes
  answers.set(key, JSON.stringify(body));
  const oldest = answers.keys().next();
  if (answers.size > limit && !oldest.done) answers.delete(oldest.value);
}

// recall is the text of the last answer to path for the signed-in user, or undefined.
export function recall(path: string): string | undefined {
  return login && remembered.test(path) ? answers.get(keyOf(path)) : undefined;
}
