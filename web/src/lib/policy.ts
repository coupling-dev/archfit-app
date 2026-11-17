import type { MapLine, MapModel } from '../components/map/model';
import type { ApiError, Policy } from './api';

type Exception = Policy['exceptions'][number];

// policyMissing is the server's sentence for a readable repository with no .archfit.yaml on its default branch.
export const policyMissing = 'No policy on the default branch yet.';

export function isPolicyMissing(error: ApiError): boolean {
  return error.status === 409 && error.message === policyMissing;
}

export interface PolicyRule {
  id: string;
  mode: string;
  summary: string;
  why?: string;
  instead?: string;
}

// The built-in rules carry their fixed ids, as on the pull request and request pages, and the design's sentences.
export function policyRules(policy: Policy): PolicyRule[] {
  const builtin = [
    ['graph', 'A dependency between two modules must be an allowed dependency.'],
    [
      'coupling',
      policy.builtin.min_balance == null
        ? 'New knowledge across a module boundary needs a minimum balance, the coupling number out of 10.'
        : `New knowledge across a module boundary needs a balance of at least ${policy.builtin.min_balance}. Balance is the coupling number, out of 10.`,
    ],
    ['duplication', 'Knowledge that another module already owns is not copied.'],
    ['cycle', 'No new cycle between modules.'],
    ['layers', 'Dependencies follow the layer order.'],
    ['unowned', 'New production code belongs to a module.'],
  ] as const;
  return [
    ...builtin.map(([id, summary]) => ({ id, summary, mode: policy.builtin[id] })),
    ...policy.rules.map((rule) => ({
      id: rule.id,
      mode: rule.not_checked ? 'note' : rule.deny || rule.only ? 'deny' : '',
      summary: rule.deny
        ? `Deny: ${rule.deny.from.join(', ')} → ${rule.deny.to.join(', ')}`
        : rule.only
          ? `Only use ${rule.only.use} in ${rule.only.in.join(', ')}`
          : (rule.note ?? ''),
      why: rule.why,
      instead: rule.instead,
    })),
  ];
}

export function ruleMode(mode: string): string {
  switch (mode) {
    case 'ask':
      return 'Ask owner';
    case 'deny':
      return 'Deny';
    case 'off':
      return 'Off';
    case 'note':
      return 'Not checked';
    default:
      return mode || 'unknown';
  }
}

export function exceptionExpiry(exception: Exception) {
  const days = exception.days_left;
  if (days == null || !Number.isFinite(days)) {
    return { label: exception.expires ? 'Days remaining not provided' : 'No end date', tone: '', remaining: null };
  }
  return {
    label:
      days < 0
        ? `Expired ${-days} ${days === -1 ? 'day' : 'days'} ago`
        : days === 0
          ? 'Expires today'
          : `Expires in ${days} ${days === 1 ? 'day' : 'days'}`,
    tone: days < 0 ? 'danger' : days <= 30 ? 'attention' : '',
    remaining: Math.max(0, Math.min(30, days)),
  };
}

// groupExceptions orders exceptions by rule, the largest group first; ties keep the order of the file.
export function groupExceptions(exceptions: Exception[]): { rule: string; items: Exception[] }[] {
  const byRule = new Map<string, Exception[]>();
  for (const e of exceptions) byRule.set(e.rule, [...(byRule.get(e.rule) ?? []), e]);
  return [...byRule]
    .map(([rule, items]) => ({ rule, items }))
    .sort((a, b) => b.items.length - a.items.length);
}

export function policyCounts(policy: Policy) {
  const rules = policyRules(policy);
  return {
    rules: rules.length,
    modules: policy.modules.length,
    askDeny: rules.filter((rule) => rule.mode === 'ask' || rule.mode === 'deny').length,
    exceptions: policy.exceptions.length,
    expiring: policy.exceptions.filter((e) => e.days_left != null && e.days_left >= 0 && e.days_left <= 30).length,
    expired: policy.exceptions.filter((e) => e.days_left != null && e.days_left < 0).length,
  };
}

export function filterPolicy(policy: Policy, query: string) {
  const needle = query.trim().toLowerCase();
  const matches = (values: (string | number | undefined | null)[]) => values.join(' ').toLowerCase().includes(needle);
  return {
    modules: policy.modules.filter((m) =>
      matches([m.name, m.layer, m.volatility, m.subdomain, ...(m.paths ?? []), ...(m.public ?? []), ...m.depends_on]),
    ),
    rules: policyRules(policy).filter((r) =>
      matches([r.id, r.mode, ruleMode(r.mode), r.summary, r.why, r.instead]),
    ),
    exceptions: policy.exceptions.filter((e) =>
      matches([
        e.rule,
        e.from,
        e.to,
        e.why,
        e.expires,
        e.request,
        e.request ? `request #${e.request}` : '',
        exceptionExpiry(e).label,
      ]),
    ),
  };
}

// The policy's declared dependencies as the Home map's model: one allowed line per dependency whose target is in the
// module list. Layers are outer-first there; the policy lists them inner-first. Missing targets are counted, not drawn.
export function policyMap(modules: Policy['modules'], layers: string[] | null): { model: MapModel; missing: number } {
  const names = new Set(modules.map((m) => m.name));
  const lines = modules.flatMap((m) =>
    m.depends_on.filter((to) => names.has(to)).map((to): MapLine => ({ kind: 'allowed', from: m.name, to })),
  );
  return {
    model: {
      layers: [...(layers ?? [])].reverse(),
      modules: modules.map((m) => ({ name: m.name, layer: m.layer, volatility: m.volatility })),
      lines,
    },
    missing: modules.reduce((n, m) => n + m.depends_on.filter((to) => !names.has(to)).length, 0),
  };
}
