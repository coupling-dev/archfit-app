import { strengthWord, type GLink } from './graph';

// The engine's balance reading of a seam, in words. Every sentence restates the engine's own meaning
// (archfit internal/relationship/seam.go and score/gate.go); a token it does not know shows nothing.

// moveWord is the engine's balancing hypothesis: a move a human weighs, not an instruction. "balanced" needs no move.
export const moveWord: Record<string, string> = {
  move_functionality: 'Move the shared functionality into one module: that removes the boundary.',
  introduce_contract: 'Introduce a contract: the coupling is intrusive, or the target declares a public surface.',
  accept_low_volatility: 'Acceptable while the target stays stable: strong coupling into a low-volatility target.',
  declare_volatility: 'Declare the volatility: this is flagged only because it is undeclared.',
  expected_by_role: 'Expected for its role: a composition root, generated or test code.',
  follow_rule: 'Fix the rule violation on this dependency.',
};

// quadrantWord is the strength and distance quadrant: cohesive and loose are healthy, the other two worth explaining.
export const quadrantWord: Record<string, string> = {
  cohesive: 'cohesive: strong coupling, close together',
  loose: 'loose: weak coupling across a boundary',
  low_cohesion: 'low cohesion: weak coupling, close together',
  tight: 'tight: strong coupling across a boundary',
};

// crossingWord is the boundary as the engine's coupling gate names it: only a deploy unit is a distributed monolith.
export function crossingWord(crossing: string): string {
  switch (crossing) {
    case 'deploy_unit':
      return 'deploy-unit boundary (a distributed monolith)';
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
  const across = crossingWord(l.crossing) || 'module boundary';
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
