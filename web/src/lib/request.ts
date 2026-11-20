import type { MapModel } from '../components/map/model';
// The words and rules of the request page (design 10.4, 12.3). The server decides who may answer and checks every
// answer again; these helpers only say what the page shows and keep a button off until its field is filled.

import type { AnswerKind, Preview, PullEffect, RequestEarlier, RequestModule, RequestView } from './api';

// answerKinds are the answers a request offers, in order. A surface request (a use past the declared parts of the
// module it reaches) is never approved as such: it offers declaring the package public or a model part (a model part
// needs policy version 1), allowing it until a date, or rejecting it. Without a named package it offers no
// declaration.
export function answerKinds(r: Pick<RequestView, 'surface' | 'policy_version'> | null): { kind: AnswerKind; title: string }[] {
  if (!r?.surface) {
    return [
      { kind: 'approve', title: 'Approve and merge' },
      { kind: 'approve-until', title: 'Approve until a date' },
      { kind: 'reject', title: 'Reject' },
    ];
  }
  const out: { kind: AnswerKind; title: string }[] = [];
  if (r.surface.package) {
    out.push({ kind: 'declare-public', title: 'Declare as public' });
    if (r.policy_version === 1) out.push({ kind: 'declare-model', title: 'Declare as model' });
  }
  out.push({ kind: 'approve-until', title: 'Allow until a date' }, { kind: 'reject', title: 'Reject' });
  return out;
}

// loosens is true for an answer that loosens the policy: it needs the owner's passkey (design 9.2). A reject does not.
export function loosens(kind: AnswerKind): boolean {
  return kind !== 'reject';
}

const isoDate = /^(\d{4})-(\d{2})-(\d{2})$/;

function isDate(s: string): boolean {
  const m = isoDate.exec(s);
  if (!m) return false;
  const d = new Date(Date.UTC(Number(m[1]), Number(m[2]) - 1, Number(m[3])));
  return d.toISOString().slice(0, 10) === s;
}

// answerProblem is what an answer still lacks, '' when it can be sent. The server checks the date's range.
export function answerProblem(kind: AnswerKind, until: string, instead: string): string {
  if (kind === 'approve-until' && !isDate(until)) return 'Choose the date the exception ends.';
  if (kind === 'reject' && !instead.trim()) return 'Write what to do instead.';
  return '';
}

// Confirmation describes the policy PR, never a promise that the affected code PR passes or merges.
export function decisionState(kind: AnswerKind, preview: Preview | undefined, later: boolean) {
  const ready = preview?.answer === kind;
  const changesPolicy = ready && !!preview.diff;
  const mergeLater = changesPolicy && later;
  const verbs: Partial<Record<AnswerKind, string>> = {
    reject: 'Reject',
    'approve-until': 'Approve until date',
    'declare-public': 'Declare as public',
    'declare-model': 'Declare as model',
  };
  const verb = verbs[kind] ?? 'Approve';
  return {
    ready,
    changesPolicy,
    mergeLater,
    confirm: !changesPolicy
      ? `${verb} with${kind === 'reject' ? 'out a passkey' : ' passkey'}`
      : mergeLater ? `${verb} and keep the policy pull request open` : `${verb} and merge policy PR`,
    effect: !changesPolicy
      ? 'This records your answer without changing .archfit.yaml.'
      : mergeLater
        ? 'This answer records the policy change in a pull request and keeps it open. The change takes effect only when that pull request merges.'
        : 'This answer records the policy change in a pull request and attempts to merge it. If GitHub refuses, the policy pull request stays open.',
  };
}

// Balance is a coupling number as the record prints it. Engine 0.9.0 names the inputs it was computed with; a
// balance of engine v4.0.0 has none (until task 6.5).
export interface Balance {
  value: number;
  max?: number;
  min: number;
  strength?: string;
  volatility?: string;
}

// balanceLine is the balance as the record prints it: "9/10 (min 7)", or with the 0.9.0 inputs
// "2/10 (min 7): intrusive, assessment-repair is high", whose subject is the target module.
export function balanceLine(b: Balance, subject?: string): string {
  const text = `${b.value}/${b.max ?? 10} (min ${b.min})`;
  return b.strength && b.volatility && subject ? `${text}: ${b.strength}, ${subject} is ${b.volatility}` : text;
}

// balanceText is the balance of a request, whose subject is the module it reaches, and says when the CLI reported it.
export function balanceText(r: Pick<RequestView, 'balance' | 'verified' | 'to'>): string {
  if (!r.balance) return '';
  const b = balanceLine(r.balance, r.to);
  return r.verified ? b : `${b}, as reported`;
}

// dependency is what the request asks about: two modules, one module, or the path of unowned code.
export function dependency(r: Pick<RequestView, 'from' | 'to' | 'path'>): string {
  if (r.from && r.to) return `${r.from} -> ${r.to}`;
  return r.from || r.path || '';
}

export interface Status {
  word: string;
  tone: 'attention' | 'neutral';
  line: string;
}

// statusOf is the page's first line: the state as a dot and a word, then one sentence.
export function statusOf(r: RequestView): Status {
  const name = `Request #${r.number}`;
  if (r.state === 'answered') {
    const line = r.answer?.kind === 'reject' ? 'An owner rejected this request.' : r.answer ? 'An owner approved this request.' : 'An owner answered this request.';
    return { word: 'answered', tone: 'neutral', line };
  }
  if (r.state === 'closed') return { word: 'closed', tone: 'neutral', line: `${name} is closed.` };
  let line = 'Waiting for an owner to answer.';
  if (!r.policy) line = `${name} cannot be answered here: the default branch has no valid policy.`;
  else if (r.allowed) line += ' The policy already allows this dependency.';
  else if (!r.verified) line += ' Its numbers are as reported: no pull request confirmed them yet.';
  return { word: 'waiting', tone: 'attention', line };
}

// earlierLine is one earlier answer about the same modules, as the policy holds it.
export function earlierLine(e: RequestEarlier): string {
  const what = dependency(e);
  if (e.kind === 'deny') return `Rule ${e.rule} denies ${what}.${e.instead ? ` Instead: ${e.instead}` : ''}`;
  return `Exception to ${e.rule} for ${what}${e.expires ? ` until ${e.expires}` : ''}.`;
}

// previewPath is the API path of the preview of one answer, with only the fields that answer takes.
export function previewPath(number: number, kind: AnswerKind, until: string, instead: string): string {
  const q = new URLSearchParams({ answer: kind });
  if (kind === 'approve-until' && until) q.set('until', until);
  if (kind === 'reject' && instead) q.set('instead', instead);
  return `requests/${number}/preview?${q}`;
}

// pullLine is what an answer does to one open pull request: whether it waits on this request, the errors it clears.
export function pullLine(e: PullEffect): string {
  const waits = e.waits ? ', waits on this request' : '';
  const errors = (n: number) => `${n} ${n === 1 ? 'error' : 'errors'}`;
  return `#${e.number} (${e.status})${waits}: clears ${errors(e.clears)}, ${errors(e.left)} left`;
}

// moduleLine is what the policy declares about one module: its layer, volatility and subdomain.
export function moduleLine(m: RequestModule): string {
  const parts = [m.layer && `Layer ${m.layer}`, `Volatility ${m.volatility || 'not set'}`, m.subdomain && `subdomain ${m.subdomain}`];
  return parts.filter(Boolean).join(' · ');
}

// requestMap uses only the policy's two declared modules and their known dependencies.
export function requestMap(r: RequestView): MapModel {
  const modules = r.modules.map((m) => ({ name: m.name, layer: m.layer || '', volatility: m.volatility || 'not set' }));
  const names = new Set(modules.map((m) => m.name));
  const lines: MapModel['lines'] = [];
  for (const m of r.modules) {
    for (const to of m.depends_on) if (names.has(to)) lines.push({ kind: 'allowed', from: m.name, to });
  }
  for (const line of r.dependencies) {
    if (names.has(line.from) && names.has(line.to)) lines.push(line);
  }
  if (r.state === 'open' && r.from && r.to && names.has(r.from) && names.has(r.to))
    lines.push({ kind: 'requested', from: r.from, to: r.to, rule: r.rule, request: r.number });
  return { layers: r.layers, modules, lines };
}
