import { describe, expect, it } from 'vitest';
import type { GLink } from './graph';
import { crossesWord, crossingWord, decisionWord, flaggedSummary, flagWord, quadrantWord } from './seam';

const link = (more: Partial<GLink> = {}): GLink => ({
  from: 'a',
  to: 'b',
  status: 'observed',
  count: 1,
  origin: '',
  strength: 'functional',
  distance: 'across modules',
  volatility: 'high',
  volatility_source: 'declared',
  imbalance: '',
  accepted: '',
  move: '',
  quadrant: '',
  crossing: '',
  container: '',
  distributed_monolith: false,
  ...more,
});

describe('seam words', () => {
  it('words every engine quadrant', () => {
    expect(Object.keys(quadrantWord).sort()).toEqual(['cohesive', 'loose', 'low_cohesion', 'tight']);
  });

  it.each([
    ['deploy_unit', 'deploy-unit boundary'],
    ['ownership', 'owner boundary'],
    ['module_boundary', 'module boundary'],
    ['declared_external', 'module boundary'],
    ['', ''],
  ])('names the %s crossing as the engine gate does', (crossing, want) => {
    expect(crossingWord(crossing)).toBe(want);
  });

  it('says where the distance was measured', () => {
    expect(crossesWord(link({ crossing: 'ownership', container: 'pkg_core' }))).toBe(
      'the owner boundary, within pkg_core',
    );
    expect(crossesWord(link({ crossing: 'module_boundary' }))).toBe('the module boundary');
    expect(crossesWord(link({ crossing: 'deploy_unit', distributed_monolith: false }))).toBe(
      'the deploy-unit boundary',
    );
    expect(crossesWord(link())).toBe('');
  });

  it('gives the engine gate reason only for a flagged dependency', () => {
    const flagged = link({ distributed_monolith: true, crossing: 'module_boundary', container: 'system' });
    expect(flagWord(flagged)).toBe(
      'functional coupling across the module boundary at high volatility (container system)',
    );
    expect(flagWord(link({ crossing: 'module_boundary' }))).toBe('');
    expect(flagWord(link({ distributed_monolith: true, crossing: 'deploy_unit', strength: 'intrusive' }))).toBe(
      'intrusive coupling across the deploy-unit boundary (a distributed monolith) at high volatility',
    );
  });

  it('counts flagged dependencies and calls only the deploy-unit share a distributed monolith', () => {
    const flagged = (crossing: string) => link({ distributed_monolith: true, crossing });
    expect(flaggedSummary([link()])).toBe('');
    expect(flaggedSummary([flagged('module_boundary'), link()])).toBe(
      '1 dependency couples strongly across a module boundary at high volatility.',
    );
    expect(flaggedSummary([flagged('module_boundary'), flagged('deploy_unit'), flagged('ownership')])).toBe(
      '3 dependencies couple strongly across a module boundary at high volatility; 1 of them across a deploy unit, a distributed monolith.',
    );
  });

  it.each([
    ['fail', 4, 'hard gates failed on 4 findings.'],
    ['fail', 1, 'hard gates failed on 1 finding.'],
    ['fail', 0, 'hard gates failed without a blocking finding; the archfit/state check says why.'],
    ['unmeasured', 0, 'hard gates could not be measured; the archfit/state check lists what is missing.'],
    ['pass', 0, ''],
    ['later', 2, ''],
  ])('states hard gates %s with %d blockers', (hard_gates, blockers, want) => {
    expect(decisionWord({ hard_gates, blockers })).toBe(want);
  });
});
