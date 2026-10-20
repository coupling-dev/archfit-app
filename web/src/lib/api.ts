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
  next_how: string;
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

export interface Finding {
  rule: string;
  rule_id: string;
  meta: string;
  violation: boolean;
  from: string;
  to: string;
  origin: '' | 'added' | 'existing';
  why: string;
  fix: string;
  alternatives: string[] | null;
  locations: string[];
  strength: string;
  distance: string;
  volatility: string;
  move: string; // the engine's balancing move for the seam, worded as the PR comment words it; '' for none
  metric: string; // a metric ratchet, which has no edge: "metric <name>: before → after (threshold)"
  owner_decision: boolean; // the engine leaves the move to the repository's owner
  also: string[] | null; // on a violation: the advisories on the same dependency, "rule · severity", as the comment says
}

export interface Cycle {
  modules: string[];
  rule: string;
  rule_id: string;
  meta: string;
  violation: boolean;
  why: string;
  fixes: string[] | null;
  alternatives: string[] | null;
  directions: Finding[];
}

export interface Dimension {
  key: string;
  name: string;
  status: string;
  observed: number;
  measurable: number;
  basis: string;
  confidence: string;
  gate: string;
}

// PullHead is the header the pull request page and its graph share.
export interface PullHead {
  number: number;
  title: string;
  author: string;
  state: string;
  head_ref: string;
  base_ref: string;
  head_sha: string;
  url: string;
  checks_url: string;
  result: string;
  tone: Tone;
  headline: string;
  required: boolean;
  unavailable: string;
  run_id: number;
  no_baseline: boolean;
  compared: boolean;
  decision?: Decision; // absent without a report
}

// Decision is the engine's decision on the report, as it states it.
export interface Decision {
  verdict: string; // the engine's verdict in words: "blocked", "needs attention", "healthy"
  detail: string; // the rest of the comment's line: hard gates, active blockers, required rules
  hard_gates: string; // pass, fail or unmeasured
  blockers: number; // decision.active_blockers
  unevaluated: { rule_id: string; reason: string }[];
  origin_reasons: string[];
}

// Findings is a report's findings in the PR comment's groups, its evidence and its agent tasks.
export interface Findings {
  violation_count: number;
  cycles: Cycle[] | null;
  other: Finding[] | null;
  advisory_count: number;
  advisories: Finding[] | null;
  accepted_count: number;
  accepted: Finding[] | null;
  advisories_elsewhere: boolean; // an advisory joined a violation's row, so the rest are on other dependencies
  fixed_count: number;
  fixed: Finding[] | null; // known issues the report no longer finds
  omitted: number;
  dimensions: Dimension[] | null;
  unknowns: { dimension: string; fact: string; reason: string }[] | null;
  tasks: string;
  task_count: number;
}

export type Pull = PullHead & Findings;

// RepoFindings is the latest report of the default branch, for the overview.
export type RepoFindings = Findings & {
  unavailable: string;
  branch: string;
  no_baseline: boolean;
  attention_reason: string; // why the engine's verdict is needs_attention; '' otherwise
};

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

// A problem of a policy text. line and column are 1-based; a problem with no single place has neither.
export interface Diagnostic {
  line?: number;
  column?: number;
  message: string;
}

export interface Check {
  name: string;
  state: string;
  tone: Tone;
  reason: string;
}

export type DraftState = 'none' | 'draft' | 'awaiting_review' | 'published' | 'closed';

export interface PolicyPage {
  branch: string;
  head_sha: string; // the open draft's head, '' without one; every write sends it back
  content: string;
  draft: DraftState;
  draft_state: string; // the draft's state in words, '' for none
  draft_url: string;
  checks: Check[];
  diagnostics: Diagnostic[];
  can_write: boolean;
  can_ready: boolean;
  managed: { offer: boolean; off?: string; data_sharing?: string };
  run?: { state: 'pending' | 'failed' | 'completed' | 'unknown'; url: string };
  since?: string;
  notice?: string;
}

export interface Checked {
  diagnostics: Diagnostic[];
  notice: string;
}

import { signInHref } from './route';

// ApiError is a failed call: the sentence to show and, for a refused policy text, its problems.
export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
    readonly diagnostics: Diagnostic[] = [],
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
    const { error: message, diagnostics } = (body ?? {}) as { error?: unknown; diagnostics?: unknown };
    throw new ApiError(
      res.status,
      typeof message === 'string' && message ? message : unreadable,
      Array.isArray(diagnostics) ? (diagnostics as Diagnostic[]) : [],
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

// repoAPI is the API path of a repository the page shows; each part is escaped, so a name cannot add a segment.
export function repoAPI(owner: string, name: string, suffix: string): string {
  return `/api/repos/${encodeURIComponent(owner)}/${encodeURIComponent(name)}/${suffix}`;
}
