// The App's JSON API (internal/control/ui/api.go). The session cookie authenticates; writes add the session's CSRF
// token as a header. The client never names a user or an installation: the server reads both from the session.

export interface Session {
  login: string;
  csrf: string;
  operator?: boolean; // the user approves installations on /admin
  waiting?: number; // operators only: installations waiting for approval; -1 when it could not be read
  build?: AppBuild;
}

export interface RepoRow {
  full_name: string;
  waiting?: boolean; // no operator approved its account yet
  state: string;
}

export interface Repos {
  repos: RepoRow[];
  truncated: boolean;
}

export interface AppBuild {
  version: string;
  source_revision: string;
  built_at: string;
  runtime_revision: string;
  modified: boolean;
}

export interface Notice {
  notice?: string;
  location?: string;
}

// Installation is one account that installed the App, as the operators' page lists it.
export interface Installation {
  id: number;
  account: string;
  type: string; // "Organization" or "User"
  installed_at: string;
  repos: { name: string; private: boolean }[];
  repos_error: boolean;
  auto: boolean; // approved by the App's configuration: no revoke
  approved_by?: string;
  approved_at?: string;
}

export interface AdminBody {
  waiting: Installation[];
  approved: Installation[];
  unreadable: number;
}

// The request page (internal/control/ui/requests.go, answers.go): one request, the preview of an answer, the outcome.
export type AnswerKind = 'approve' | 'approve-until' | 'reject';

export interface RequestModule {
  name: string;
  layer?: string;
  volatility?: string;
  subdomain?: string;
  depends_on: string[];
}

export interface RequestEarlier {
  kind: 'deny' | 'exception';
  rule: string;
  from: string;
  to?: string;
  why?: string;
  instead?: string;
  expires?: string;
}

export interface RequestView {
  issue_href?: string;
  number: number;
  state: 'open' | 'answered' | 'closed';
  reason: string;
  rule: string;
  path?: string;
  from?: string;
  to?: string;
  expires?: string;
  record: string;
  verified: boolean; // false: the numbers are as reported by the CLI
  balance?: { value: number; max: number; min: number };
  policy: boolean; // false: the default branch has no policy of version 3
  layers: string[];
  modules: RequestModule[];
  answer?: { kind: AnswerKind; until?: string; instead?: string };
  dependencies: { kind: 'deny' | 'exception'; from: string; to: string; rule: string; expires?: string }[];
  allowed: boolean; // the policy already allows from -> to
  earlier: RequestEarlier[];
}

export interface PullEffect {
  number: number;
  status: string;
  waits: boolean;
  clears: number;
  left: number;
}

export interface Preview {
  answer: AnswerKind;
  until?: string;
  diff: string; // '' when the answer writes no policy line
  policy_digest: string;
  loosens: boolean;
  pulls: PullEffect[];
}

export interface Answered {
  pull_request?: number; // the policy pull request; absent when no policy line changes
  state?: 'merged' | 'later' | 'refused' | 'void';
  merge?: string;
  notice: string;
}

// The passkey page (internal/control/ui/passkey.go). A passkey's id is opaque; the user sees its dates.
export interface Passkey {
  id: string;
  created: string;
  last_used?: string;
}

export interface Passkeys {
  passkeys: Passkey[];
}

export interface Registered {
  passkey: Passkey;
  notice: string;
}

import { refused, waiting } from './gate';
import { signInHref } from './route';

// ApiError is a failed call: the sentence to show, and for a repository of an account no operator approved that account.
export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
    readonly waiting = '',
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
    const { error: message, waiting: account } = (body ?? {}) as { error?: unknown; waiting?: unknown };
    throw new ApiError(
      res.status,
      typeof message === 'string' && message ? message : unreadable,
      typeof account === 'string' ? account : '',
    );
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

// explainLoad is explain for the read that fills a page. Its 404 (a repository that does not exist or the user cannot
// see, a pull request that is not one) replaces the whole page, as the server's error page did, and so does the 403 of
// an account waiting for approval, with the Waiting page. Never for a write: a write's 404 stays a sentence on the
// page, so a form keeps what the user typed.
export function explainLoad(e: ApiError, here: string, go?: (url: string) => void): string {
  if (e.waiting) {
    waiting.set(e.waiting);
    return '';
  }
  if (e.status !== 404) return explain(e, here, go);
  refused.set(e.message);
  return '';
}

// repoAPI is the API path of a repository the page shows; each part is escaped, so a name cannot add a segment.
export function repoAPI(owner: string, name: string, suffix: string): string {
  return `/api/repos/${encodeURIComponent(owner)}/${encodeURIComponent(name)}/${suffix}`;
}

// Policy is the default-branch policy read by the policy page, not the open proposal.
export interface Policy {
  owners: string[] | null;
  gate: string;
  layers: string[] | null;
  modules: {
    name: string;
    paths: string[] | null;
    public: string[] | null;
    volatility: string;
    subdomain: string;
    layer: string;
    depends_on: string[];
  }[];
  builtin: {
    graph: string;
    coupling: string;
    duplication: string;
    cycle: string;
    layers: string;
    unowned: string;
    min_balance: number | null;
  };
  rules: {
    id: string;
    deny?: { from: string[]; to: string[] };
    only?: { use: string; in: string[] };
    note?: string;
    not_checked: boolean;
    why: string;
    instead?: string;
  }[];
  exceptions: {
    rule: string;
    from: string;
    to?: string;
    expires?: string;
    days_left?: number;
    why: string;
    request?: number;
    request_href?: string;
  }[];
  yaml: string;
  digest: string;
  edit: { available: boolean; reason: string; file?: string; digest?: string; recovery?: boolean };
}
