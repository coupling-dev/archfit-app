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
  state: string; // stored evidence, not a readiness verdict
  setup_stage?: string;
  enforcement: string;
  enforcement_reason?: string;
  next_step: { text: string; href: string };
}

export interface Repos {
  repos: RepoRow[];
  truncated: boolean;
}

// One owner step that waits on a repository, from /api/owner-actions. since is absent when no time is recorded.
export interface OwnerActionRow {
  repo: string;
  kind: string;
  label: string;
  text: string;
  pull_request?: number;
  since?: string;
  href: string;
}

export interface OwnerActions {
  actions: OwnerActionRow[];
  unread: number; // repositories whose owner steps could not be read
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
export type AnswerKind = 'approve' | 'approve-until' | 'reject' | 'declare-public' | 'declare-model';

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
  balance?: { value: number; max: number; min: number; strength?: string; volatility?: string };
  policy: boolean; // false: the default branch has no valid policy
  layers: string[];
  modules: RequestModule[];
  answer?: { kind: AnswerKind; until?: string; instead?: string };
  dependencies: { kind: 'deny' | 'exception'; from: string; to: string; rule: string; expires?: string }[];
  allowed: boolean; // the policy already allows from -> to
  earlier: RequestEarlier[];
  // A surface request (engine 0.9.0): the declared parts of `to` and the package past them a declaration adds.
  surface?: { public: string[]; model: string[]; package?: string };
  waiting?: number[]; // pull requests waiting on this request
  asked_by?: string; // the login that filed the request; absent for an older record
  asked_at?: string; // when, RFC 3339
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

import { refused, visit, waiting } from './gate';
import { get } from 'svelte/store';
import { forget } from './memory';
import { signInHref } from './route';

// ApiError is a failed call: the sentence to show, and for a repository of an account no operator approved that account.
export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
    readonly waiting = '',
    // from is the visit a read was started in (lib/gate); 0 for a write.
    readonly from = 0,
  ) {
    super(message);
  }
}

const csrfHeader = 'X-CSRF-Token';
const unreadable = 'The App did not answer. Try again in a minute.';

export async function getJSON<T>(path: string): Promise<T> {
  const from = get(visit);
  try {
    return await answer<T>(await send(path, { headers: { Accept: 'application/json' } }));
  } catch (e) {
    if (e instanceof ApiError) throw new ApiError(e.status, e.message, e.waiting, from);
    throw e;
  }
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
  // A refused read (the session ended, or the account may not see it) leaves nothing seen before to show again.
  if (res.status === 401 || res.status === 403) forget();
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
  const current = e.from === get(visit);
  if (e.waiting) {
    if (current) waiting.set(e.waiting);
    return '';
  }
  if (e.status !== 404) return explain(e, here, go);
  if (current) refused.set(e.message);
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
    model: string[] | null; // version 1 only
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
  edit: {
    available: boolean; reason: string; file?: string; digest?: string; recovery?: boolean;
    // The open policy pull request: its change against the default branch, and what Reload removes from it.
    pull_request?: number; target?: string; diff?: string; lost?: string; voided?: boolean; foreign?: boolean;
    merge_now?: boolean; // the App merges the pull request by itself when GitHub allows
  };
}
