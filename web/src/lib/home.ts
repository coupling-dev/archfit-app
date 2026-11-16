import { repoPage, type Repo } from './route';
import { lineKinds, type MapModel, type MapLine } from '../components/map/model';

export interface HomeNeed {
  kind: string;
  text: string;
  href: string;
  record?: string;
}
export interface OwnerReview {
  label: string;
  href?: string;
}
export interface HomeSetup {
  owner_action?: OwnerReview;
  state: string;
  policy: boolean;
  workflow: boolean;
  passkey: boolean;
  open_pull: number;
  missing: string[];
  first_pull?: number;
  rules: { code: string; state: string; text: string }[];
}

export function setupStepState(setup: HomeSetup) {
  const done = [setup.policy || setup.first_pull !== undefined, setupPullStep(setup).kind !== 'create',
    setup.state === 'approved', setup.rules.length > 0 && setup.rules.every((rule) => rule.state === 'ok' || rule.state === 'not_applicable')];
  return {
    done,
    completed: done.filter(Boolean).length,
    next: !done[0] ? 'Next: add the policy' : !done[1] ? 'Next: create the pull request' :
      !done[2] ? 'Next: approve the workflow with your passkey' : !done[3] ? 'Next: turn on the required check' : 'Setup is complete.',
    approval: setup.owner_action?.href ? 'Review needed' : setup.state === 'first_approval' ? 'Action needed' : setup.state === 'approved' ? 'Done' : 'Waiting',
    gate: setup.rules.some((rule) => rule.state === 'unknown') ? 'Check with an admin' : done[3] ? 'On' : 'Action needed',
  };
}

export function setupPullStep(setup: HomeSetup) {
  if (setup.open_pull > 0) return { kind: 'review' as const, status: 'Ready for review', number: setup.open_pull };
  if (setup.first_pull !== undefined) return { kind: 'first-policy' as const, status: 'Ready for review', number: setup.first_pull };
  if (setup.policy && setup.workflow) return { kind: 'current' as const, status: 'Done' };
  return { kind: 'create' as const, status: 'Action needed' };
}

export function setupChipClass(status: string): 'good' | 'attention' | 'neutral' {
  if (status === 'Done' || status === 'On' || status === 'Ready for review') return 'good';
  if (status === 'Waiting' || status === 'Not available') return 'neutral';
  return 'attention';
}

export function setupRuleStatus(state: string): string {
  if (state === 'ok') return 'On';
  if (state === 'unknown') return 'Check';
  if (state === 'not_applicable') return 'Not available';
  return 'Action needed';
}

export interface Home {
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
function number(v: unknown): number {
  if (typeof v !== 'number' || !Number.isSafeInteger(v) || v < 1) throw new Error('The Home answer could not be read.');
  return v;
}

export function readOwnerReview(value: unknown): OwnerReview {
  const v = object(value);
  return { label: string(v.label), ...(v.href === undefined ? {} : { href: string(v.href) }) };
}

export function readHome(value: unknown): Home {
  const v = object(value);
  const home: Home = {
    status: string(v.status),
    needs_you: array(v.needs_you ?? []).map((n) => {
      const need = object(n);
      return {
        kind: string(need.kind),
        text: string(need.text),
        href: string(need.href),
        ...(need.record === undefined ? {} : { record: string(need.record) }),
      };
    }),
  };
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
        };
      }),
    };
  }
  if (v.setup !== undefined) {
    const s = object(v.setup);
    home.setup = {
      ...(s.owner_action === undefined ? {} : { owner_action: readOwnerReview(s.owner_action) }),
      state: string(s.state),
      policy: boolean(s.policy),
      workflow: boolean(s.workflow),
      passkey: boolean(s.passkey),
      open_pull: nonnegativeNumber(s.open_pull),
      missing: array(s.missing).map(string),
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
      return decodeURIComponent(owner).toLowerCase() === repo.owner.toLowerCase() &&
        decodeURIComponent(name).toLowerCase() === repo.name.toLowerCase();
    } catch { return false; }
  };
  const local = /^\/r\/([^/]+)\/([^/]+)(\/.*)$/.exec(href);
  if (local && sameRepo(local[1], local[2])) {
    const suffix = local[3];
    if (/^\/(?:policy|owner\/(?:first|confirm|revert))$/.test(suffix) ||
        /^\/requests\/[1-9][0-9]{0,9}$/.test(suffix) ||
        /^\/owner\/(?:first|workflow)\?pull=[1-9][0-9]{0,9}$/.test(suffix))
      return repoPage(repo.fullName, suffix);
  }
  const issue = /^https:\/\/github\.com\/([^/]+)\/([^/]+)\/issues\/([1-9][0-9]{0,9})$/.exec(href);
  if (issue && sameRepo(issue[1], issue[2]))
    return 'https://github.com/' + encodeURIComponent(repo.owner) + '/' + encodeURIComponent(repo.name) + '/issues/' + issue[3];
  return undefined;
}
