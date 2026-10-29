import { describe, expect, it } from 'vitest';
import { ageWord } from './age';

const now = Date.parse('2026-10-29T12:00:00Z');

describe('ageWord', () => {
  it.each([
    ['2026-10-29T12:00:00Z', 'just now'],
    ['2026-10-29T11:59:30Z', 'just now'],
    ['2026-10-29T12:00:30Z', 'just now'],
    ['2026-10-29T11:59:00Z', '1 minute ago'],
    ['2026-10-29T11:15:00Z', '45 minutes ago'],
    ['2026-10-29T11:00:00Z', '1 hour ago'],
    ['2026-10-29T09:30:00Z', '2 hours ago'],
    ['2026-10-28T12:00:00Z', '1 day ago'],
    ['2026-10-26T11:00:00Z', '3 days ago'],
    ['2026-07-01T00:00:00Z', '120 days ago'],
    ['yesterday', ''],
    ['', ''],
  ])('%s', (iso, want) => {
    expect(ageWord(iso, now)).toBe(want);
  });
});
