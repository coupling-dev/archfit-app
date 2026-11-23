import { repoPage, type Repo } from './route';
import { lineKinds, type MapModel, type MapLine } from '../components/map/model';

export interface HomeNeed {
  kind: string;
  text: string;
  href: string;
  request?: number;
  record?: string;
  since?: string; // RFC 3339: when an owner step last changed
}
export interface OwnerReview {
  label: string;
  href?: string;
}
export const approvalStatuses = ['needed', 'approved_pending_merge', 'active', 'stale', 'unknown'] as const;
export interface ApprovalSummary {
  status: (typeof approvalStatuses)[number];
  kind: 'first' | 'workflow' | 'confirm';
  pull_request?: number;
  current_head?: string;
  approved_head?: string;
  merged?: boolean;
  href?: string;
}
export interface WeeklySummary { known: boolean; checked?: number; quiet?: number; asked?: number; caveat?: string }
// The policy's gate value as the App reports it: advisory when absent, unknown when unreadable.
export const setupModes = ['advisory', 'required', 'unknown'] as const;
export type SetupMode = (typeof setupModes)[number];
export interface HomeSetup {
  weekly?: WeeklySummary;
  mode: SetupMode;
  approval: ApprovalSummary;
  passkey_known: boolean;
  owner_action?: OwnerReview;
  state: string;
  policy: boolean;
  workflow: boolean;
  passkey: boolean;
  open_pull: number;
  missing: string[];
  first_pull?: number;
  setup_pull?: number;
  rules: { code: string; state: string; text: string }[];
}

// The advisory state with an active first approval is the one where the page offers to
// make the check required; the ruleset instructions stay below it.
export function setupCanRequireGate(setup: HomeSetup): boolean {
  return setup.mode === 'advisory' && setup.approval.status === 'active';
}

export function setupStepState(setup: HomeSetup) {
  const unreadableRules = setup.rules.some(
    (rule) => (rule.code === 'rulesets' || rule.code === 'branch_rules') && rule.state !== 'ok',
  );
  const enforcementOn = ['pull_requests', 'gate_check'].every((code) => {
    const findings = setup.rules.filter((rule) => rule.code === code);
    return findings.length > 0 && findings.every((rule) => rule.state === 'ok');
  });
  const done = [
    setup.policy || setup.first_pull !== undefined,
    setupPullStep(setup).kind !== 'create',
    setup.approval.status === 'active' || setup.approval.status === 'approved_pending_merge',
    enforcementOn && !unreadableRules,
  ];
  return {
    done,
    completed: done.filter(Boolean).length,
    next: !done[0]
      ? 'Next: add the policy'
      : !done[1]
        ? 'Next: create the pull request'
        : !done[2]
          ? 'Next: review owner approval'
          : !done[3]
            ? 'Next: turn on the required check'
            : setup.approval.status === 'approved_pending_merge'
              ? 'Waiting for activation on the default branch.'
              : 'Setup complete · archfit/gate required',
    approvalText: approvalText(setup.approval),
  };
}

// The step chip has three words: done, to do, or after step N when the step waits for an earlier one.
// Step 3 cannot be approved before its pull request exists; step 4 is independent of approval.
export function setupStepChip(done: boolean[], index: number): string {
  if (done[index]) return 'done';
  if (index === 2 && !done[1]) return 'after step 2';
  return 'to do';
}

export function approvalText(approval: ApprovalSummary): string {
  if (approval.status === 'approved_pending_merge')
    return approval.merged
      ? 'Approval recorded; checking activation'
      : `Approved for pull request #${approval.pull_request}. Waiting for merge.`;
  if (approval.status === 'stale') return `Pull request #${approval.pull_request} changed after approval. The approval covers the old head; a new approval is needed.`;
  if (approval.status === 'unknown') return 'Could not confirm approval';
  if (approval.status === 'active') return 'The approved policy and workflow are active on the default branch.';
  return 'Review the proposed files before approving.';
}

export function ownerReviewLabel(setup: HomeSetup): string {
  return setup.passkey ? 'Review and approve' : 'Review proposed files';
}

export function approvalNeedsReview(setup: HomeSetup): boolean {
  return setup.approval.status === 'needed' || setup.approval.status === 'stale';
}

export function setupPullStep(setup: HomeSetup) {
  if (setup.open_pull > 0) return { kind: 'review' as const, number: setup.open_pull };
  if (setup.first_pull !== undefined) return { kind: 'first-policy' as const, number: setup.first_pull };
  if (setup.policy && setup.workflow) return { kind: 'current' as const };
  return { kind: 'create' as const };
}

export function setupRuleIsAdvisory(code: string): boolean {
  return ['administrator_bypass', 'app_bypass', 'policy_push', 'policy_push_bypass', 'approvals'].includes(code);
}

// Rule words: on, off, optional, verify in GitHub, does not apply. Optional rows never read as action needed.
export function setupRuleStatus(state: string, code = ''): string {
  if (setupRuleIsAdvisory(code)) {
    if (state === 'unknown') return 'verify in GitHub';
    if (state === 'warning') return 'optional';
  }
  if (state === 'ok') return 'on';
  if (state === 'unknown') return 'verify in GitHub';
  if (state === 'not_applicable') return 'does not apply';
  return 'off';
}

// The repository state words of the Archfit 4 design, as the App sends them. An unknown word is refused.
export const homeStates = ['clean', 'drift', 'expired', 'outside_flow', 'not_set_up', 'unknown'] as const;
export type HomeState = (typeof homeStates)[number];

const stateLabels: Record<HomeState, string> = {
  clean: 'clean',
  drift: 'drift',
  expired: 'expired',
  outside_flow: 'changed outside the flow',
  not_set_up: 'not set up',
  unknown: 'unknown',
};

export function homeStateLabel(state: HomeState): string {
  return stateLabels[state];
}

export interface Home {
  weekly?: WeeklySummary;
  state: HomeState;
  status: string;
  needs_you: HomeNeed[];
  map?: MapModel;
  setup?: HomeSetup;
}

function object(v: unknown): Record<string, unknown> {
  if (!v || typeof v !== 'object' || Array.isArray(v)) throw new Error('The Home answer could not be read.');
  return v as Record<string, unknown>;
}
function string(v: unknown): string {
  if (typeof v !== 'string') throw new Error('The Home answer could not be read.');
  return v;
}
function array(v: unknown): unknown[] {
  if (!Array.isArray(v)) throw new Error('The Home answer could not be read.');
  return v;
}
function boolean(v: unknown): boolean {
  if (typeof v !== 'boolean') throw new Error('The Home answer could not be read.');
  return v;
}
function nonnegativeNumber(v: unknown): number {
  if (typeof v !== 'number' || !Number.isSafeInteger(v) || v < 0) throw new Error('The Home answer could not be read.');
  return v;
}
function integer(v: unknown): number {
  if (typeof v !== 'number' || !Number.isSafeInteger(v)) throw new Error('The Home answer could not be read.');
  return v;
}
function number(v: unknown): number {
  if (typeof v !== 'number' || !Number.isSafeInteger(v) || v < 1) throw new Error('The Home answer could not be read.');
  return v;
}

export function readOwnerReview(value: unknown): OwnerReview {
  const v = object(value);
  return { label: string(v.label), ...(v.href === undefined ? {} : { href: string(v.href) }) };
}

export function readApproval(value: unknown): ApprovalSummary {
  const a = object(value);
  const status = approvalStatuses.find((s) => s === a.status);
  const kind = (['first', 'workflow', 'confirm'] as const).find((k) => k === a.kind);
  if (!status || !kind) throw new Error('The Home answer could not be read.');
  const pull = a.pull_request === undefined ? undefined : number(a.pull_request);
  const current = a.current_head === undefined ? undefined : string(a.current_head);
  const approved = a.approved_head === undefined ? undefined : string(a.approved_head);
  if (
    (status === 'approved_pending_merge' && (!pull || !current || current !== approved)) ||
    (status === 'stale' && (!pull || !approved))
  )
    throw new Error('The Home answer could not be read.');
  return {
    status,
    kind,
    ...(pull === undefined ? {} : { pull_request: pull }),
    ...(current === undefined ? {} : { current_head: current }),
    ...(approved === undefined ? {} : { approved_head: approved }),
    ...(a.merged === undefined ? {} : { merged: boolean(a.merged) }),
    ...(a.href === undefined ? {} : { href: string(a.href) }),
  };
}

export function readHome(value: unknown): Home {
  const v = object(value);
  // A missing state is shown as unknown, never as clean; a word the App does not define is refused.
  const state = v.state === undefined ? 'unknown' : homeStates.find((s) => s === v.state);
  if (!state) throw new Error('The Home answer could not be read.');
  const home: Home = {
    state,
    status: string(v.status),
    needs_you: array(v.needs_you ?? []).map((n) => {
      const need = object(n);
      return {
        kind: string(need.kind),
        text: string(need.text),
        href: string(need.href),
        ...(need.request === undefined ? {} : { request: number(need.request) }),
        ...(need.record === undefined ? {} : { record: string(need.record) }),
        ...(need.since === undefined ? {} : { since: string(need.since) }),
      };
    }),
  };
  if (v.weekly !== undefined) {
    const w = object(v.weekly);
    home.weekly = boolean(w.known) ? { known: true, checked: nonnegativeNumber(w.checked), quiet: nonnegativeNumber(w.quiet), asked: nonnegativeNumber(w.asked), caveat: string(w.caveat) } : { known: false };
  }
  if (v.map !== undefined) {
    const m = object(v.map);
    home.map = {
      layers: array(m.layers).map(string),
      modules: array(m.modules).map((entry) => {
        const n = object(entry);
        return { name: string(n.name), layer: string(n.layer), volatility: string(n.volatility) };
      }),
      lines: array(m.lines).map((entry): MapLine => {
        const l = object(entry);
        const kind = lineKinds.find((k) => k === l.kind);
        if (!kind) throw new Error('The Home answer could not be read.');
        return {
          kind,
          from: string(l.from),
          to: string(l.to),
          ...(l.rule === undefined ? {} : { rule: string(l.rule) }),
          ...(l.request === undefined ? {} : { request: number(l.request) }),
          ...(l.expires === undefined ? {} : { expires: string(l.expires) }),
          ...(l.days_left === undefined ? {} : { days_left: integer(l.days_left) }),
        };
      }),
    };
  }
  if (v.setup !== undefined) {
    const s = object(v.setup);
    const weekly = s.weekly === undefined ? undefined : object(s.weekly);
    const weeklyKnown = weekly === undefined ? false : boolean(weekly.known);
    home.setup = {
      ...(weekly === undefined ? {} : { weekly: weeklyKnown ? { known: true, checked: nonnegativeNumber(weekly.checked), quiet: nonnegativeNumber(weekly.quiet), asked: nonnegativeNumber(weekly.asked), caveat: string(weekly.caveat) } : { known: false } }),
      mode: setupModes.find((m) => m === s.mode) ?? 'unknown',
      approval: readApproval(s.approval),
      passkey_known: boolean(s.passkey_known),
      ...(s.owner_action === undefined ? {} : { owner_action: readOwnerReview(s.owner_action) }),
      state: string(s.state),
      policy: boolean(s.policy),
      workflow: boolean(s.workflow),
      passkey: boolean(s.passkey),
      open_pull: nonnegativeNumber(s.open_pull),
      missing: array(s.missing).map(string),
      ...(s.setup_pull === undefined ? {} : { setup_pull: number(s.setup_pull) }),
      ...(s.first_pull === undefined ? {} : { first_pull: number(s.first_pull) }),
      rules: array(s.rules).map((entry) => {
        const r = object(entry);
        return { code: string(r.code), state: string(r.state), text: string(r.text) };
      }),
    };
  }
  return home;
}

// Rebuild only known route shapes for this repository; safeHref alone would allow another repository's route.
export function homeHref(repo: Repo, href: string): string | undefined {
  const sameRepo = (owner: string, name: string) => {
    try {
      return (
        decodeURIComponent(owner).toLowerCase() === repo.owner.toLowerCase() &&
        decodeURIComponent(name).toLowerCase() === repo.name.toLowerCase()
      );
    } catch {
      return false;
    }
  };
  const local = /^\/r\/([^/]+)\/([^/]+)(\/.*)$/.exec(href);
  if (local && sameRepo(local[1], local[2])) {
    const suffix = local[3];
    if (
      /^\/(?:policy|owner\/(?:first|confirm|revert))$/.test(suffix) ||
      /^\/requests\/[1-9][0-9]{0,9}$/.test(suffix) ||
      /^\/owner\/(?:first|workflow)\?pull=[1-9][0-9]{0,9}$/.test(suffix)
    )
      return repoPage(repo.fullName, suffix);
  }
  const issue = /^https:\/\/github\.com\/([^/]+)\/([^/]+)\/(issues|pull)\/([1-9][0-9]{0,9})$/.exec(href);
  if (issue && sameRepo(issue[1], issue[2]))
    return (
      'https://github.com/' +
      encodeURIComponent(repo.owner) +
      '/' +
      encodeURIComponent(repo.name) +
      '/' +
      issue[3] +
      '/' +
      issue[4]
    );
  return undefined;
}

export interface HomeAction {
  label: string;
  href: string;
  external: boolean;
  needsPasskey?: boolean; // the review is the owner's approval, which asks for a passkey
}

const needLabels: Record<string, string> = {
  confirm: 'Confirm the policy change',
  revert: 'Revert the policy change',
  request: 'Open the request',
  exception: 'Open the exception',
  drift: 'Open the drift issue',
  workflow: 'Review workflow change',
};

// The icon marks the kind of work: a problem to fix, or something that waits for an answer.
export function needIcon(kind: string): 'alert' | 'clock' {
  return kind === 'request' || kind === 'workflow' || kind === 'exception' ? 'clock' : 'alert';
}

export function isExternalHref(href: string): boolean {
  return href.startsWith('https://');
}

// The one primary button: the first owner item with a link that leaves nowhere unreachable, else the active setup
// step's own action, else none. A button never points at an anchor on the same page.
export function homeAction(home: Home, repo: Repo): HomeAction | undefined {
  for (const need of home.needs_you) {
    const href = homeHref(repo, need.href);
    if (!href) continue;
    // Drift and exception items point at a GitHub issue. Before that issue exists they fall back to the policy page,
    // which View policy already offers, so a button named for the issue would name something that is not there.
    if ((need.kind === 'drift' || need.kind === 'exception') && !isExternalHref(href)) continue;
    // A workflow item for an eligible owner already reads "Review workflow change · pull request #n"; keep that verb.
    const label = need.kind === 'workflow' && need.text.includes(' · ') ? need.text.split(' · ')[0] : needLabels[need.kind];
    if (label) return { label, href, external: isExternalHref(href) };
  }
  const setup = home.setup;
  if (!setup) return undefined;
  const github = `https://github.com/${encodeURIComponent(repo.owner)}/${encodeURIComponent(repo.name)}`;
  if (setup.setup_pull) return { label: `Open pull request #${setup.setup_pull}`, href: `${github}/pull/${setup.setup_pull}`, external: true };
  const review = approvalNeedsReview(setup) && setup.owner_action?.href ? homeHref(repo, setup.owner_action.href) : undefined;
  if (review) return { label: ownerReviewLabel(setup), href: review, external: false, needsPasskey: true };
  const active = setupStepState(setup).done.indexOf(false);
  if (active === 1) return { label: 'Create a pull request on GitHub', href: `${github}/compare`, external: true };
  // In advisory state the next action is the check's own step, not the ruleset: adding the
  // ruleset first would require a check that still does not block a merge.
  if (active === 3 && !setupCanRequireGate(setup)) return { label: 'Open ruleset settings', href: `${github}/settings/rules`, external: true };
  return undefined;
}
