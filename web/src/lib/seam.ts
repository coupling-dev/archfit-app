import { strengthWord, type GLink } from './graph';

// The engine's balance reading of a seam, in words. Every sentence restates the engine's own meaning
// (archfit internal/relationship/seam.go and score/gate.go); a token it does not know shows nothing. The suggested
// move comes worded from the server, as the PR comment words it.

// quadrantWord is the strength and distance quadrant: cohesive and loose are healthy, the other two worth explaining.
export const quadrantWord: Record<string, string> = {
  cohesive: 'cohesive: strong coupling, close together',
  loose: 'loose: weak coupling across a boundary',
  low_cohesion: 'low cohesion: weak coupling, close together',
  tight: 'tight: strong coupling across a boundary',
};

// crossingWord is the boundary as the engine's coupling gate names it. Only a flagged dependency across a deploy unit
// is a distributed monolith, so flagWord adds that, never this.
export function crossingWord(crossing: string): string {
  switch (crossing) {
    case 'deploy_unit':
      return 'deploy-unit boundary';
    case 'ownership':
      return 'owner boundary';
    case '':
      return '';
  }
  return 'module boundary';
}

// crossesWord is where the distance was measured: the boundary and the module that holds both ends.
export function crossesWord(l: GLink): string {
  const across = crossingWord(l.crossing);
  if (!across) return '';
  return l.container ? `the ${across}, within ${l.container}` : `the ${across}`;
}

// flagWord is the engine's coupling gate reason for a flagged seam: "functional coupling across the module boundary at
// high volatility (container system)".
export function flagWord(l: GLink): string {
  if (!l.distributed_monolith) return '';
  const strength = strengthWord[l.strength] ?? 'strong coupling';
  let across = crossingWord(l.crossing) || 'module boundary';
  if (l.crossing === 'deploy_unit') across += ' (a distributed monolith)';
  const volatility = l.volatility ? `${l.volatility} volatility` : 'high volatility';
  return `${strength} across the ${across} at ${volatility}${l.container ? ` (container ${l.container})` : ''}`;
}

// flaggedSummary counts the flagged dependencies, and names a distributed monolith only for those across a deploy unit.
export function flaggedSummary(links: GLink[]): string {
  const flagged = links.filter((l) => l.distributed_monolith);
  if (!flagged.length) return '';
  const n = flagged.length;
  let s = `${n} ${n === 1 ? 'dependency couples' : 'dependencies couple'} strongly across a module boundary at high volatility`;
  const deploy = flagged.filter((l) => l.crossing === 'deploy_unit').length;
  if (deploy) s += `; ${deploy} of them across a deploy unit, a distributed monolith`;
  return s + '.';
}

// decisionWord is the engine's hard gates in one sentence; '' when they passed or the value is one it does not know.
export function decisionWord(d: { hard_gates: string; blockers: number }): string {
  switch (d.hard_gates) {
    case 'fail':
      return d.blockers
        ? `hard gates failed on ${d.blockers} ${d.blockers === 1 ? 'finding' : 'findings'}.`
        : 'hard gates failed without a blocking finding; the archfit/state check says why.';
    case 'unmeasured':
      return 'hard gates could not be measured; the archfit/state check lists what is missing.';
  }
  return '';
}

// ownerDecisionTitle explains an agent task the engine marks needs_owner_decision.
export const ownerDecisionTitle = 'Archfit leaves this to the repository owner: a policy decision, not a code change.';
