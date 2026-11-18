import { describe, expect, it } from 'vitest';
import { statusStyle } from './status';

describe('status words', () => {
  it.each([
    ['pass', 'success', 'check-circle'],
    ['fail', 'danger', 'x-circle'],
    ['expired', 'danger', 'x-circle'],
    ['waiting', 'attention', 'clock'],
    ['unknown', 'attention', 'question'],
    ['to do', 'attention', 'alert'],
    ['not confirmed', 'neutral', 'question'],
  ])('shows %s as %s with %s', (word, tone, icon) => {
    expect(statusStyle(word)).toEqual({ tone, icon });
  });

  it('gives the other repository states no colour', () => {
    for (const word of ['clean', 'drift', 'changed outside the flow', 'not set up'])
      expect(statusStyle(word)).toEqual({ tone: 'neutral', icon: 'dot' });
  });

  it('never shows an unknown word as a pass', () => {
    expect(statusStyle('Mystery state')).toEqual({ tone: 'neutral', icon: 'dot' });
    expect(statusStyle('Pass').tone).toBe('neutral');
  });

  it('does not match object prototype names', () => {
    expect(statusStyle('constructor')).toEqual({ tone: 'neutral', icon: 'dot' });
    expect(statusStyle('toString').tone).toBe('neutral');
  });
});
