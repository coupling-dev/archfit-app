// The App's JSON API (internal/control/ui/api.go). The session cookie authenticates; writes add the session's CSRF
// token as a header. The client never names a user or an installation: the server reads both from the session.

export type Tone = 'success' | 'attention' | 'danger' | 'accent' | 'neutral';

export interface Session {
  login: string;
  csrf: string;
}

export interface RepoRow {
  full_name: string;
  state: string;
  tone: Tone;
  reason: string;
}

export interface Repos {
  repos: RepoRow[];
  truncated: boolean;
}

export interface PullRow {
  number: number;
  title: string;
  state: string;
  tone: Tone;
  reason: string;
  href: string;
}

export interface Step {
  label: string;
  done: boolean;
  current: boolean;
}

export interface Overview {
  default_branch: string;
  main_state: string;
  main_tone: Tone;
  main_reason: string;
  pulls: PullRow[] | null;
  pulls_more: number;
  pulls_error: boolean;
  steps: Step[];
  next: string;
  stage: string;
  diagnostic: string;
  draft_url: string;
  draft_state: string;
  banner: string;
  admin: boolean;
  managed_ai?: ShareState; // for an administrator only
  can_start: boolean;
  can_discover: boolean;
  pulls_url: string;
  checks_url: string;
}

export interface Notice {
  notice?: string;
  location?: string;
}

// unknown is "could not be read", never "not allowed".
export type ShareState = 'allowed' | 'not_allowed' | 'unknown';

export interface Precondition {
  label: string;
  state: 'met' | 'not_met' | 'unknown';
}

export interface SharedFeature {
  kind: string;
  title: string;
  leaves: string;
  note?: string;
  allowed: boolean;
  by?: string; // the login as it was when the consent was recorded
  at?: string; // RFC 3339
  checks: Precondition[];
}

export interface DataSharing {
  owner: string;
  features: SharedFeature[];
  notice?: string;
}

import { signInHref } from './route';

export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message);
  }
}

const csrfHeader = 'X-CSRF-Token';
const unreadable = 'The App did not answer. Try again in a minute.';

export async function getJSON<T>(path: string): Promise<T> {
  return answer<T>(await send(path, { headers: { Accept: 'application/json' } }));
}

export async function postJSON<T>(path: string, csrf: string, body: unknown): Promise<T> {
  return answer<T>(
    await send(path, {
      method: 'POST',
      headers: {
        Accept: 'application/json',
        'Content-Type': 'application/json',
        [csrfHeader]: csrf,
      },
      body: JSON.stringify(body),
    }),
  );
}

async function send(path: string, init: RequestInit): Promise<Response> {
  try {
    return await fetch(path, { ...init, credentials: 'same-origin' });
  } catch {
    throw new ApiError(0, unreadable);
  }
}

async function answer<T>(res: Response): Promise<T> {
  let body: unknown;
  try {
    body = await res.json();
  } catch {
    throw new ApiError(res.status, unreadable);
  }
  if (!res.ok) {
    const message = (body as { error?: unknown })?.error;
    throw new ApiError(res.status, typeof message === 'string' && message ? message : unreadable);
  }
  return body as T;
}

// explain is the sentence a page shows for a failed call. A 401 (the session ended, or GitHub rejected the user's token
// and the server ended it) goes to sign-in instead, returning here.
export function explain(e: ApiError, here: string, go: (url: string) => void = (url) => location.assign(url)): string {
  if (e.status !== 401) return e.message;
  go(signInHref(here));
  return '';
}

// repoAPI is the API path of a repository the page shows; each part is escaped, so a name cannot add a segment.
export function repoAPI(owner: string, name: string, suffix: string): string {
  return `/api/repos/${encodeURIComponent(owner)}/${encodeURIComponent(name)}/${suffix}`;
}
