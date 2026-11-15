import type { Repo } from './route';

export const ownerKinds = ['first', 'workflow', 'confirm', 'revert'] as const;
export type OwnerKind = (typeof ownerKinds)[number];
export type OwnerRoute = Repo & { action: OwnerKind; pull: number };

export interface OwnerFile {
  role: string;
  ref: string;
  path: string;
  digest: string;
  text: string;
  missing: boolean;
}
export interface OwnerView {
  kind: OwnerKind;
  pull_request: number;
  status: string;
  note: string;
  main_action: string;
  action_digest: string;
  digest: string;
  head: string;
  base: string;
  tip: string;
  approved: string;
  files: OwnerFile[];
  diffs: { title: string; path: string; text: string }[];
  proposals: {
    id: string;
    pull_request: number;
    head: string;
    digest: string;
    approvals: { id: string; request: number; digest: string }[];
  }[];
}
export interface OwnerOutcome {
  approval: string;
  pull_request?: number;
  state?: 'merged' | 'later' | 'refused';
  merge?: string;
  issue?: number;
  check?: 'rerun' | 'pending' | 'blocked';
}

const unreadable = 'The owner action could not be read. Reload the page.';
function object(v: unknown): Record<string, unknown> {
  if (!v || typeof v !== 'object' || Array.isArray(v)) throw new Error(unreadable);
  return v as Record<string, unknown>;
}
function string(v: unknown): string {
  if (typeof v !== 'string') throw new Error(unreadable);
  return v;
}
function array(v: unknown): unknown[] {
  if (!Array.isArray(v)) throw new Error(unreadable);
  return v;
}
function number(v: unknown): number {
  if (typeof v !== 'number' || !Number.isSafeInteger(v) || v < 0) throw new Error(unreadable);
  return v;
}
function digest(v: unknown): string {
  const s = string(v);
  if (!/^sha256:[0-9a-f]{64}$/.test(s)) throw new Error(unreadable);
  return s;
}

export function readOwnerView(value: unknown, route: OwnerRoute): OwnerView {
  const v = object(value);
  const kind = ownerKinds.find((k) => k === v.kind);
  if (!kind || kind !== route.action || number(v.pull_request) !== route.pull) throw new Error(unreadable);
  return {
    kind,
    pull_request: route.pull,
    status: string(v.status),
    note: string(v.note),
    main_action: string(v.main_action),
    action_digest: digest(v.action_digest),
    digest: digest(v.digest),
    head: string(v.head),
    base: string(v.base),
    tip: string(v.tip),
    approved: string(v.approved),
    files: array(v.files).map((entry) => {
      const f = object(entry);
      if (typeof f.missing !== 'boolean') throw new Error(unreadable);
      return {
        role: string(f.role),
        ref: string(f.ref),
        path: string(f.path),
        digest: digest(f.digest),
        text: string(f.text),
        missing: f.missing,
      };
    }),
    diffs: array(v.diffs).map((entry) => {
      const d = object(entry);
      return { title: string(d.title), path: string(d.path), text: string(d.text) };
    }),
    proposals: array(v.proposals).map((entry) => {
      const p = object(entry);
      return {
        id: string(p.id),
        pull_request: number(p.pull_request),
        head: string(p.head),
        digest: digest(p.digest),
        approvals: array(p.approvals).map((entry) => {
          const a = object(entry);
          return { id: string(a.id), request: number(a.request), digest: digest(a.digest) };
        }),
      };
    }),
  };
}

export function ownerViewPath(route: OwnerRoute): string {
  return `owner/${route.action}/view${route.pull ? '?pull=' + route.pull : ''}`;
}

export function outcomeText(kind: OwnerKind, out: OwnerOutcome): string {
  if (kind === 'revert') {
    if (out.state === 'merged') return 'Policy reverted.';
    return `The revert is recorded in #${out.pull_request}. It has not merged. Open the pull request to see what to do.`;
  }
  const done =
    kind === 'first'
      ? 'First policy approved.'
      : kind === 'workflow'
        ? 'Workflow change approved.'
        : 'Policy confirmed.';
  if (out.check === 'rerun') return done + ' The App asked for the check to run again.';
  if (out.check === 'pending') return done + ' The check waits for its run.';
  if (out.check === 'blocked')
    return done + ' The check could not run again. Open the pull request checks to see what to do.';
  return done;
}
