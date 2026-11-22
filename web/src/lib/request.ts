import type { MapModel } from '../components/map/model';
// The words and rules of the request page (design 10.4, 12.3). The server decides who may answer and checks every
// answer again; these helpers only say what the page shows and keep a button off until its field is filled.

import type { AnswerKind, Preview, PullEffect, RequestEarlier, RequestModule, RequestView } from './api';

// answerKinds are the answers a request offers, in order (story spec 8.18, section 5; owner decision 2026-11-22). A
// surface request (a use past the declared parts of the module it reaches) is never approved as such: it offers making
// the package public, allowing it until a date, rejecting it, and a smaller fourth answer, declaring it a shared model.
// Without a named package it offers no declaration.
export function answerKinds(r: Pick<RequestView, 'surface'> | null): { kind: AnswerKind; title: string; minor?: boolean }[] {
  const until = { kind: 'approve-until' as const, title: 'Allow until a date' };
  const reject = { kind: 'reject' as const, title: 'Reject, and say what to do instead' };
  if (!r?.surface) return [{ kind: 'approve', title: 'Allow' }, until, reject];
  if (!r.surface.package) return [until, reject];
  return [
    { kind: 'declare-public', title: 'Make it public' },
    until,
    reject,
    { kind: 'declare-model', title: 'Declare it as shared model', minor: true },
  ];
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

// decisionState says whether the chosen answer can be sent: its preview is loaded. The answer always asks the App to
// merge the policy pull request when its checks pass (8.20); the page never offers to keep it open.
export function decisionState(kind: AnswerKind, preview: Preview | undefined) {
  const ready = preview?.answer === kind;
  return { ready, changesPolicy: ready && !!preview.diff, confirm: loosens(kind) ? 'Answer with passkey' : 'Answer' };
}

// Balance is a coupling number as the record prints it. It names the inputs it was computed with; a balance stored from
// an engine v4.0.0 result has none.
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

// surfaceLine is the declared surface of a module as the record prints it: the public globs, then the model globs,
// each with its role, or "none declared".
export function surfaceLine(s: { public: string[]; model: string[] }): string {
  const parts = [...s.public.map((g) => `${g} (public)`), ...s.model.map((g) => `${g} (model)`)];
  return parts.join(', ') || 'none declared';
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
    const lines: Record<AnswerKind, string> = {
      approve: 'An owner approved this request.',
      'approve-until': 'An owner approved this request.',
      reject: 'An owner rejected this request.',
      // A use past the declared parts is never approved: the owner declared the package instead.
      'declare-public': 'An owner declared the package public.',
      'declare-model': 'An owner declared the package a model part.',
    };
    const line = r.answer ? lines[r.answer.kind] : 'An owner answered this request.';
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

// question is the page title: the request as the owner's question (story spec 8.18, section 2.6).
export function question(r: Pick<RequestView, 'from' | 'to' | 'path' | 'surface'>): string {
  if (r.surface?.package && r.to) return `Should ${r.surface.package} be public in ${r.to}?`;
  if (r.from && r.to) return `May ${r.from} depend on ${r.to}?`;
  if (r.from) return `May ${r.from} keep this change?`;
  return r.path ? `May ${r.path} stay as it is?` : 'May this change stay as it is?';
}

export type Step = 'done' | 'failed' | 'current' | 'later' | 'skipped';

// storySteps is the story row of a request page: change, check, ask, answer, policy, re-check. A request exists, so the
// check failed and the ask happened. An approval moves the story to the policy step; a rejection ends it at the answer.
// merged is true when this page saw the App merge the policy pull request of its own answer.
export function storySteps(r: Pick<RequestView, 'state' | 'answer'>, merged = false): Step[] {
  if (r.state === 'open') return ['done', 'failed', 'current', 'later', 'later', 'later'];
  if (r.answer?.kind === 'reject') return ['done', 'failed', 'done', 'failed', 'skipped', 'skipped'];
  if (r.state === 'answered') return merged ? ['done', 'failed', 'done', 'done', 'done', 'current'] : ['done', 'failed', 'done', 'done', 'current', 'later'];
  return ['done', 'failed', 'done', 'skipped', 'skipped', 'skipped'];
}

// addedLines counts the lines an answer adds to .archfit.yaml, from its unified diff.
export function addedLines(diff: string): number {
  return diff.split('\n').filter((l) => l.startsWith('+') && !l.startsWith('+++')).length;
}

const lines = (n: number) => `${n} ${n === 1 ? 'line' : 'lines'}`;

// effectLines is what an answer does, shown before the owner chooses it: the policy change, then the open pull
// requests. Without a preview yet it says only what the answer kind does.
export function effectLines(
  kind: AnswerKind,
  r: Pick<RequestView, 'from' | 'to' | 'surface'>,
  p: Preview | undefined,
  until: string,
): string[] {
  const out: string[] = [];
  const n = p ? addedLines(p.diff) : 1;
  const pkg = r.surface?.package ?? 'the package';
  if (kind === 'approve') out.push(`Adds ${lines(n)} to the policy: ${r.from} may depend on ${r.to}.`);
  else if (kind === 'approve-until')
    out.push(`Adds an exception for this one place that ends on ${until || 'the date you choose'}. After it, the check fails again and asks for a new answer.`);
  else if (kind === 'declare-public') out.push(`Adds ${lines(n)}: ${pkg} joins the public part of ${r.to}. Every module may then use it.`);
  else if (kind === 'declare-model')
    out.push(`Adds ${lines(n)}: other modules may use ${pkg} as a shared model, a lower balance than a public part.`);
  else if (!p) out.push('The author sees what you say to do instead.');
  else if (p.diff) out.push(`Adds a rule: ${r.from} may not depend on ${r.to}, with what you say to do instead. The author sees your text.`);
  else out.push('Changes nothing in the policy. The author sees what you say to do instead.');
  if (!p || kind === 'reject') return out;
  const passing = p.pulls.filter((e) => e.left === 0).map((e) => `#${e.number}`);
  const failing = p.pulls.filter((e) => e.left > 0);
  if (passing.length)
    out.push(`Pull request ${passing.join(', ')} ${passing.length === 1 ? 'passes' : 'pass'} when the policy is updated.`);
  for (const e of failing) out.push(`Pull request #${e.number} still has ${e.left} ${e.left === 1 ? 'error' : 'errors'}.`);
  if (!p.pulls.length) out.push('No open pull request changes.');
  return out;
}

// effectLead splits an effect line into its lead, shown strong, and the rest: "Adds 1 line" | " to the policy: …".
export function effectLead(line: string): [string, string] {
  const m = /^(Adds (?:\d+ lines?|an exception|a rule)|Changes nothing)/.exec(line);
  return m ? [m[1], line.slice(m[1].length)] : ['', line];
}

// defaultUntil is the date an "allow until a date" answer offers first: 90 days from today, in UTC.
export function defaultUntil(today: Date): string {
  return new Date(today.getTime() + 90 * 86_400_000).toISOString().slice(0, 10);
}

// changedWhileOpen is the sentence the page shows when a reload of the request finds it changed, '' when nothing did.
export function changedWhileOpen(before: RequestView, after: RequestView, policyMoved: boolean): string {
  if (before.state === 'open' && after.state !== 'open')
    return after.state === 'answered'
      ? `Request #${after.number} was answered while you looked. The page shows the answer.`
      : `Request #${after.number} was closed while you looked.`;
  if (policyMoved) return 'The policy on the default branch changed while you looked. The effects below are the new ones.';
  return '';
}
