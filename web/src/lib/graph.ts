// The module graph of a report (internal/control/ui/graph): what each drawn thing means, the problem list, focus mode
// and the input of the ELK layout. Pure: the canvas, the matrix and the tables all read their picture from here.
// Report strings are data and only ever reach the page as text.

import type { PullHead } from './api';

export type Status = 'violation' | 'accepted' | 'advisory' | 'allowed' | 'observed';

export interface GNode {
  name: string;
  layer: string;
  policy_line: number;
  volatility: string; // what the report's policy declares; 'undeclared'; '' when the page cannot say
  subdomain: string;
}

export interface GLink {
  from: string;
  to: string;
  status: Status;
  count: number;
  origin: '' | 'added' | 'existing';
  strength: string;
  distance: string;
  volatility: string; // the seam's: on a functional dependency, the worse end's
  volatility_source: string; // the engine's provenance: declared, inherited, cascade, undeclared
  imbalance: string;
  accepted: '' | 'violation' | 'advisory'; // what an accepted link accepts
  // The engine's balance reading of the strongest seam (lib/seam.ts words them); '' when it gives none.
  move: string; // the balancing move, worded as the PR comment words it
  quadrant: string;
  crossing: string; // the boundary raw_distance.basis names
  container: string; // the module holding both ends, or "system"
  distributed_monolith: boolean; // the engine's flag: strong coupling across a module boundary at high volatility
}

export interface GFinding {
  from: string;
  to: string;
  rule_id: string;
  rule: string; // the readable name, as on the pull request page
  meta: string; // severity and status, as there
  kind: string;
  status: string;
  why: string;
  more: number;
}

export interface View {
  nodes: GNode[];
  links: GLink[];
  findings: GFinding[];
  stale: boolean;
  incomplete: boolean;
}

export interface Graph {
  unavailable: string;
  view: View | null;
  stale_reason: string;
  policy_href: string;
  branch: string;
  pull: PullHead | null;
  added: number;
}

// Triage order: what needs a person first.
export const statuses: Status[] = ['violation', 'advisory', 'accepted', 'allowed', 'observed'];

export const statusWord: Record<Status, string> = {
  violation: 'violation',
  advisory: 'advisory',
  accepted: 'accepted',
  allowed: 'allowed',
  observed: 'observed',
};

// linkStatusWord names a link's status. Accepted says what it accepts, so gate debt never reads as an advisory.
export function linkStatusWord(l: GLink): string {
  return l.status === 'accepted' && l.accepted ? `accepted ${l.accepted}` : statusWord[l.status];
}

// moduleVolatility is what the policy the report was computed from declares for a module; '' when the page cannot
// say: the policy is not the report's, or does not name the module.
export function moduleVolatility(n: GNode): string {
  if (n.volatility) return `volatility ${n.volatility}`;
  return n.subdomain ? `subdomain ${n.subdomain}` : '';
}

// linkVolatility is a dependency's volatility, with where the engine took it from when that is not a declaration.
export function linkVolatility(l: GLink): string {
  const from = l.volatility_source;
  return from && from !== 'declared' && from !== l.volatility ? `${l.volatility} (${from})` : l.volatility;
}

// statusCount words a count of dependencies of one status, for the chips: the chips count dependencies, so a
// dependency "with a violation", never "violations", which the findings count.
export function statusCount(s: Status): string {
  return s === 'violation' ? 'with a violation' : s === 'advisory' ? 'with an advisory' : statusWord[s];
}

export const meaning: Record<Status, string> = {
  violation: 'An active gate finding, or a gate finding of a status this version does not know, names this dependency.',
  advisory: 'A finding that never blocks names this dependency.',
  accepted: 'A finding names this dependency, accepted by the baseline or a waiver. Accepted is never allowed.',
  allowed: 'A blocking allowlist or layer rule of the policy permits this dependency.',
  observed:
    'No active finding names this dependency. That is not proof that a rule permits it, and it can still break a rule the report did not evaluate.',
};

// Only an active finding is a problem. Accepted debt is known and drawn muted; it never reads as a problem.
const problemStatus: Record<Status, boolean> = {
  violation: true,
  advisory: true,
  accepted: false,
  allowed: false,
  observed: false,
};

// Integration strength, the main axis of Balanced Coupling, is the line width: weakest thinnest. A dependency the engine
// measured no strength for is drawn thin and dashed.
export const strengths = ['contract', 'model', 'functional', 'symmetric', 'intrusive'] as const;
export const strengthWidth: Record<string, number> = {
  contract: 1.5,
  model: 2.5,
  functional: 3.5,
  symmetric: 4.25,
  intrusive: 5,
};
export const unmeasuredWidth = 1.5;
export const acceptedWidth = 1.5;

export const strengthWord: Record<string, string> = {
  contract: 'contract coupling',
  model: 'model coupling',
  functional: 'functional coupling',
  symmetric: 'symmetric coupling',
  intrusive: 'intrusive coupling',
};

export const focusLimit = 15; // above this many modules the canvas starts on the problems
export const maxDrawn = 300; // above this many modules the tables are the view

export function measured(l: GLink): boolean {
  return l.strength in strengthWidth;
}

export function lineWidth(l: GLink): number {
  return strengthWidth[l.strength] ?? unmeasuredWidth;
}

// isAdded is the pull request graph's "new": the comment's rule, so the comment, the pull request page and the graph
// say the same. The repository graph never claims one.
export function isAdded(l: GLink, pull: boolean): boolean {
  return pull && l.origin === 'added';
}

// A problem is a violation or an advisory, and on the pull request graph anything it added. The unit everywhere on the
// graph pages is one dependency: an ordered pair of modules A → B, as the engine's seam. A cycle is two dependencies.
export function isProblemStatus(s: Status): boolean {
  return problemStatus[s];
}

export function isProblem(l: GLink, pull: boolean): boolean {
  return problemStatus[l.status] || isAdded(l, pull);
}

// isAcceptedEarlier is known debt: accepted, and not added by this pull request. An accepted dependency the pull request
// added is new to the reviewer, so it stays a problem and is never drawn faint.
export function isAcceptedEarlier(l: GLink, pull: boolean): boolean {
  return l.status === 'accepted' && !isAdded(l, pull);
}

export function linkKey(l: { from: string; to: string }): string {
  return JSON.stringify([l.from, l.to]);
}

// countWord is the import count behind a dependency: how many imports of the pair the engine observed.
export function countWord(l: GLink): string {
  if (!l.count) return 'named by a finding';
  return `${l.count} ${l.count === 1 ? 'import' : 'imports'}`;
}

// dependencyWord counts dependencies, the graph's one unit.
export function dependencyWord(n: number): string {
  return `${n} ${n === 1 ? 'dependency' : 'dependencies'}`;
}

// balanceWord reads the engine's seam severity. The engine counts the none and low bands as balanced (its balancing
// hypothesis needs no move below medium), so only medium and worse read as imbalanced.
export function balanceWord(l: GLink): string {
  if (l.imbalance === 'none' || l.imbalance === 'low') return 'balanced';
  if (l.imbalance) return `imbalanced · ${l.imbalance}`;
  return measured(l) ? 'no imbalance reported' : 'not measured';
}

export interface ProblemRow {
  key: string;
  link: GLink;
  cycle: boolean; // the other direction is a problem too, of any status
  added: boolean;
}

// problemRows is the list above the canvas: one row per active dependency the filter shows. With emphasis on, what
// this pull request added comes first; then by status, then by import count.
export function problemRows(
  links: GLink[],
  pull: boolean,
  emphasis: boolean,
  show: Record<Status, boolean>,
): ProblemRow[] {
  return links
    .filter((l) => isProblem(l, pull) && show[l.status])
    .sort(
      (a, b) =>
        (emphasis ? Number(isAdded(b, pull)) - Number(isAdded(a, pull)) : 0) ||
        statuses.indexOf(a.status) - statuses.indexOf(b.status) ||
        b.count - a.count ||
        linkKey(a).localeCompare(linkKey(b)),
    )
    .map((l) => ({
      key: linkKey(l),
      link: l,
      cycle: links.some((r) => r.from === l.to && r.to === l.from && r.from !== r.to && isProblem(r, pull)),
      added: isAdded(l, pull),
    }));
}

// acceptedRows is the dependencies known and accepted earlier, by import count: the collapsed group under the problems.
export function acceptedRows(links: GLink[], pull: boolean, show: Record<Status, boolean>): GLink[] {
  return links
    .filter((l) => isAcceptedEarlier(l, pull) && show.accepted)
    .sort((a, b) => b.count - a.count || linkKey(a).localeCompare(linkKey(b)));
}

// focus is the part of the view the canvas starts on above focusLimit modules: the problem dependencies and the modules
// they join. Without a problem it is the whole view.
export function focus(view: View, pull: boolean): { nodes: GNode[]; links: GLink[] } {
  const links = view.links.filter((l) => isProblem(l, pull));
  if (!links.length) return { nodes: view.nodes, links: view.links };
  const keep = new Set(links.flatMap((l) => [l.from, l.to]));
  return { nodes: view.nodes.filter((n) => keep.has(n.name)), links };
}

// worst is the strongest problem status on a module's dependencies, for its border; "" without one.
export function worst(links: GLink[], name: string): Status | '' {
  let w: Status | '' = '';
  for (const l of links) {
    if ((l.from === name || l.to === name) && problemStatus[l.status]) {
      if (!w || statuses.indexOf(l.status) < statuses.indexOf(w)) w = l.status;
    }
  }
  return w;
}

export function findingsOf(view: View, l: GLink): GFinding[] {
  return view.findings.filter((f) => f.from === l.from && f.to === l.to);
}

// layers is the declared layers in policy order: the order of each layer's first module in the policy text.
export function layers(nodes: GNode[]): string[] {
  const declared = nodes.filter((n) => n.layer && n.policy_line).sort((a, b) => a.policy_line - b.policy_line);
  return [...new Set(declared.map((n) => n.layer))];
}

// order is the modules for the matrix and the tables: by declared layer, then by name; undeclared modules last.
export function order(nodes: GNode[]): GNode[] {
  const ls = layers(nodes);
  const rank = (n: GNode) => (n.layer && ls.includes(n.layer) ? ls.indexOf(n.layer) : ls.length);
  return [...nodes].sort((a, b) => rank(a) - rank(b) || a.name.localeCompare(b.name));
}

export const nodeWidth = 176;
export const nodeHeight = 48;
const labelWidth = (text: string) => 12 + text.length * 7;

// elkInput is the ELK layered graph of the drawn part: top to bottom, one track per direction, room for each count
// label. Edge priority is the dependency count, so the heavy direction keeps downward. Declared layers are ELK
// partitions in policy order. A module's dependency on itself is not drawn; the matrix and the tables show it.
export function elkInput(nodes: GNode[], links: GLink[]) {
  const ls = layers(nodes);
  const partitioned = ls.length > 0;
  return {
    id: 'root',
    layoutOptions: {
      'elk.algorithm': 'layered',
      'elk.direction': 'DOWN',
      'elk.edgeRouting': 'SPLINES',
      'elk.spacing.nodeNode': '56',
      'elk.layered.spacing.nodeNodeBetweenLayers': '72',
      'elk.layered.spacing.edgeNodeBetweenLayers': '26',
      'elk.layered.spacing.edgeEdgeBetweenLayers': '18',
      'elk.spacing.edgeEdge': '16',
      'elk.spacing.edgeNode': '26',
      'elk.layered.crossingMinimization.strategy': 'LAYER_SWEEP',
      'elk.layered.nodePlacement.strategy': 'NETWORK_SIMPLEX',
      'elk.layered.mergeEdges': 'false',
      'elk.edgeLabels.inline': 'true',
      'elk.partitioning.activate': String(partitioned),
    },
    children: nodes.map((n) => ({
      id: n.name,
      width: nodeWidth,
      height: nodeHeight,
      layoutOptions: (partitioned
        ? { 'elk.partitioning.partition': String(ls.includes(n.layer) ? ls.indexOf(n.layer) : ls.length) }
        : {}) as Record<string, string>,
    })),
    edges: links
      .filter((l) => l.from !== l.to)
      .map((l) => ({
        id: linkKey(l),
        sources: [l.from],
        targets: [l.to],
        layoutOptions: { 'elk.layered.priority.direction': String(l.count) },
        labels: [{ text: String(l.count || '·'), width: labelWidth(String(l.count || '·')), height: 18 }],
      })),
  };
}

// smooth draws a line through ELK's points, as a Catmull-Rom spline turned into cubic Béziers; the ends stay exact.
export function smooth(pts: [number, number][]): string {
  const r = (v: number) => Math.round(v * 10) / 10;
  if (pts.length < 2) return '';
  if (pts.length === 2) return `M ${r(pts[0][0])} ${r(pts[0][1])} L ${r(pts[1][0])} ${r(pts[1][1])}`;
  let d = `M ${r(pts[0][0])} ${r(pts[0][1])}`;
  for (let i = 0; i < pts.length - 1; i++) {
    const p0 = pts[i - 1] ?? pts[i];
    const p1 = pts[i];
    const p2 = pts[i + 1];
    const p3 = pts[i + 2] ?? p2;
    const c1 = [p1[0] + (p2[0] - p0[0]) / 6, p1[1] + (p2[1] - p0[1]) / 6];
    const c2 = [p2[0] - (p3[0] - p1[0]) / 6, p2[1] - (p3[1] - p1[1]) / 6];
    d += ` C ${r(c1[0])} ${r(c1[1])} ${r(c2[0])} ${r(c2[1])} ${r(p2[0])} ${r(p2[1])}`;
  }
  return d;
}
