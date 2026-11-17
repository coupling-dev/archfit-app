import { describe, expect, it } from 'vitest';
import { statusStyle } from './status';

describe('status words', () => {
  it.each([
    ['pass', 'success', 'check-circle'],
    ['fail', 'danger', 'x-circle'],
    ['waiting', 'attention', 'clock'],
    ['unknown', 'neutral', 'question'],
    ['Action needed', 'attention', 'alert'],
    ['Ready for review', 'accent', 'info'],
  ])('shows %s as %s with %s', (word, tone, icon) => {
    expect(statusStyle(word)).toEqual({ tone, icon });
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
