// The App's JSON API (internal/control/ui/api.go). The session cookie authenticates; writes add the session's CSRF
// token as a header. The client never names a user or an installation: the server reads both from the session.

export type Tone = 'success' | 'attention' | 'danger' | 'accent' | 'neutral';

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
  rerun?: string; // the pull request waits for an update after the target branch moved: the next step, with the modules
}

// DecisionRow is a decision request of an unattended agent. Every text is data written by an agent or GitHub.
export interface DecisionRow {
  pull: number;
  title?: string;
  status: 'pending' | 'approved' | 'declined' | 'stale';
  finding: string; // short ID
  rule: string;
  edge: string; // "from → to"
  requester: string; // whoever commented; never an approval
  head: string; // short
  created: string; // RFC 3339
  href: string; // the pull request page, where an owner decides
}

// PolicyChangeRow is an open pull request that changes the protected policy.
export interface PolicyChangeRow {
  number: number;
  title: string;
  headline: string; // the policy change card's classification
  approval: 'granted' | 'missing' | 'not_needed' | 'unknown';
  by?: string;
  href: string;
}

// Inbox is the decisions page: what waits for an owner. Read only.
export interface Inbox {
  requests: DecisionRow[];
  requests_more: number;
  requests_error: boolean;
  policy_changes: PolicyChangeRow[];
  policy_more: number;
  policy_error: boolean;
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
  main_sha: string; // the default-branch commit that check is on, short
  main_at: string; // when it started, RFC 3339; '' without a check
  pulls: PullRow[] | null;
  pulls_more: number;
  pulls_error: boolean;
  pulls_to_update: number; // listed pull requests that wait for an update after the target moved
  decisions_pending: number; // decision requests that wait for an owner, in all pull requests
  decisions: DecisionRow[]; // the first few
  decisions_error: boolean;
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
  workflow?: WorkflowFile; // the file the owner adds by hand; absent when none is due
}

// WorkflowFile is the Archfit workflow the owner adds: the App has no permission to write .github/workflows.
export interface WorkflowFile {
  path: string;
  branch: string;
  content: string;
  add_url: string; // GitHub's new-file page on branch, filled in
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
  locations_total: number; // the engine's full count; locations holds at most 50 of them
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

export interface AppBuild {
  version: string;
  source_revision: string;
  built_at: string;
  runtime_revision: string;
  modified: boolean;
}

// Display only. A diagnostic run identity is never report admission.
export interface CheckContext {
  run_id: number;
  run_attempt: number;
  head_sha: string;
  check_started_at: string;
  report_digest: string;
  reason: string;
  next: string;
  detail: string;
  also: string[];
  producer: string;
  policy_digest: string;
  baseline_digest: string;
  labels_digest: string;
  profile: string;
  schema?: string;
  approved?: { engine_version: string; image_digest: string; platform: string; action_ref: string };
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
  check?: CheckContext; // App check diagnostics, independent of report availability
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

// Approval is the owner approval of a pull request head that changes a protected file; absent when it changes none.
export interface Approval {
  head_sha: string; // the full head it is for; Approve sends it back
  paths: string[];
  digests: Record<string, string> | null; // each path at the head, '' when deleted; null for a fork; Approve sends it back
  short_digests: Record<string, string> | null;
  state: 'granted' | 'missing';
  by?: string;
  can_approve: boolean;
  reason?: string; // why the signed-in user cannot approve, while missing
  author_is_agent: boolean;
}

// ScreenLine is one line of the comment's first screen, as plain text.
export interface ScreenLine {
  kind: 'headline' | 'heading' | 'text' | 'item';
  text: string;
}

export type Pull = PullHead & Findings & { approval?: Approval; first_screen?: ScreenLine[] };

// Approved answers the Approve button: the approval read again.
export interface Approved {
  approval: Approval | null;
  notice: string;
}

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
  offered: boolean; // false: the App does not run it, so it can only be revoked
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

export interface Checked {
  diagnostics: Diagnostic[];
  notice: string;
}

import { refused, waiting } from './gate';
import { signInHref } from './route';

// ApiError is a failed call: the sentence to show, for a refused policy text its problems, and for a repository of an
// account no operator approved that account.
export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
    readonly diagnostics: Diagnostic[] = [],
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
    const {
      error: message,
      diagnostics,
      waiting: account,
    } = (body ?? {}) as { error?: unknown; diagnostics?: unknown; waiting?: unknown };
    throw new ApiError(
      res.status,
      typeof message === 'string' && message ? message : unreadable,
      Array.isArray(diagnostics) ? (diagnostics as Diagnostic[]) : [],
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
// page, so a form or the editor keeps what the user typed.
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
