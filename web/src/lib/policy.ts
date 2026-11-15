import type { Policy } from './api';

type PolicyModule = Policy['modules'][number];
type Exception = Policy['exceptions'][number];

export interface PolicyRule {
  id: string;
  title: string;
  mode: string;
  summary: string;
  why?: string;
  instead?: string;
}

export function policyRules(policy: Policy): PolicyRule[] {
  const builtin = [
    ['graph', 'Module dependencies', 'Only declared module dependencies.'],
    ['coupling', 'Coupling', `Minimum balance: ${policy.builtin.min_balance ?? 'Not provided'}.`],
    ['duplication', 'Duplication', 'Check for copied code.'],
    ['cycle', 'Module cycles', 'No dependency cycles between modules.'],
    ['layers', 'Layer direction', 'Respect the declared layer order.'],
    ['unowned', 'Module coverage', 'Code must belong to a module.'],
  ] as const;
  return [
    ...builtin.map(([id, title, summary]) => ({ id, title, summary, mode: policy.builtin[id] })),
    ...policy.rules.map((rule) => ({
      id: rule.id,
      title: rule.id,
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
      return mode || 'Not provided';
  }
}

export function exceptionExpiry(exception: Exception) {
  const days = exception.days_left;
  if (days == null || !Number.isFinite(days)) {
    return { label: exception.expires ? 'Days remaining not provided' : 'Not provided', tone: '', remaining: null };
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

export function policyCounts(policy: Policy) {
  return {
    modules: policy.modules.length,
    blocking: policyRules(policy).filter((rule) => rule.mode === 'ask' || rule.mode === 'deny').length,
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
      matches([r.id, r.title, r.mode, ruleMode(r.mode), r.summary, r.why, r.instead]),
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

export function policyGraph(modules: PolicyModule[], layers: string[] | null) {
  // Fixed three-column rows keep labels readable without a layout engine. The table remains authoritative.
  const order = [...new Set([...[...(layers ?? [])].reverse(), ...modules.map((m) => m.layer)])].filter((name) => modules.some((m) => m.layer === name));
  const nodes: { module: PolicyModule; index: number; x: number; y: number; layer: number }[] = [];
  let row = 0;
  for (const [layer, name] of order.entries()) {
    const group = modules.flatMap((module, index) => (module.layer === name ? [{ module, index }] : []));
    const remaining = new Map(group.map((entry) => [entry.module.name, entry]));
    while (remaining.size) {
      const incoming = new Set([...remaining.values()].flatMap(({ module }) => module.depends_on));
      const ready = [...remaining.values()].filter(({ module }) => !incoming.has(module.name));
      // A cycle has no first module. Keep all remaining nodes visible rather than inventing an order.
      const batch = ready.length ? ready : [...remaining.values()];
      const parentCenter = (name: string) => {
        const parents = nodes.filter((node) => node.module.depends_on.includes(name));
        return parents.length ? parents.reduce((sum, node) => sum + node.x, 0) / parents.length : 240;
      };
      batch.sort((a, b) => parentCenter(a.module.name) - parentCenter(b.module.name));
      for (const [i, entry] of batch.entries()) {
        const columns = Math.min(3, batch.length - Math.floor(i / 3) * 3);
        nodes.push({
          ...entry,
          x: (480 - columns * 152) / 2 + (i % 3) * 152,
          y: 24 + (row + Math.floor(i / 3)) * 96,
          layer,
        });
        remaining.delete(entry.module.name);
      }
      row += Math.ceil(batch.length / 3);
    }
  }
  const byName = new Map(nodes.map((node) => [node.module.name, node]));
  const links: { from: string; to: string; path: string }[] = [];
  let missing = 0;
  for (const source of nodes) {
    for (const name of source.module.depends_on) {
      const target = byName.get(name);
      if (!target) {
        missing++;
        continue;
      }
      const sx = source.x + 70;
      const tx = target.x + 70;
      const sameRow = source.y === target.y;
      const sy = source.y + (source.y <= target.y ? 44 : 0);
      const ty = target.y + (source.y >= target.y ? 44 : 0);
      const bend = sameRow ? sy + 38 : (sy + ty) / 2;
      const path =
        source === target
          ? `M${sx - 20},${sy} C${sx - 45},${sy + 40} ${sx + 45},${sy + 40} ${sx + 20},${sy}`
          : `M${sx},${sy} C${sx},${bend} ${tx},${bend} ${tx},${ty}`;
      links.push({ from: source.module.name, to: name, path });
    }
  }
  return {
    nodes,
    links,
    missing,
    height: Math.max(120, row * 96 + 8),
    layers: order,
  };
}
