import { describe, expect, it } from 'vitest';
import { focusLabel, legendKinds, legendWords, type MapLine } from './model';

describe('focusLabel', () => {
  const cases: [MapLine, string][] = [
    [{ kind: 'allowed', from: 'handlers', to: 'api' }, 'handlers → api · allowed'],
    [{ kind: 'requested', from: 'handlers', to: 'api', request: 431 }, 'handlers → api · requested #431'],
    [{ kind: 'requested', from: 'handlers', to: 'api' }, 'handlers → api · requested'],
    [{ kind: 'exception', from: 'handlers', to: 'api', expires: '2027-01-15' }, 'handlers → api · exception until 2027-01-15'],
    [{ kind: 'exception', from: 'handlers', to: 'api' }, 'handlers → api · exception'],
    [{ kind: 'deny', from: 'handlers', to: 'api', rule: 'graph' }, 'handlers → api · a rule denies it · graph'],
    [{ kind: 'drift', from: 'handlers', to: 'api', rule: 'graph' }, 'handlers → api · drift · graph'],
    [{ kind: 'drift', from: 'handlers', to: 'api' }, 'handlers → api · drift'],
  ];
  it.each(cases)('names both ends and the status of %j', (line, label) => {
    expect(focusLabel(line)).toBe(label);
  });
});

describe('legend', () => {
  it('has one entry per line kind, in the words of the design', () => {
    expect(legendKinds.map((k) => legendWords[k])).toEqual([
      'An allowed dependency',
      'A requested dependency',
      'One a rule denies',
      'An exception (with its date)',
      'Drift',
    ]);
  });
});
