import { describe, expect, it } from 'vitest';
import { allowedLine } from './sharing';

describe('allowedLine', () => {
  it.each([
    [{ allowed: false }, 'Not allowed'],
    [{ allowed: false, by: 'alice', at: '2026-10-20T09:00:00Z' }, 'Not allowed'],
    [{ allowed: true, by: 'alice', at: '2026-10-20T09:00:00Z' }, 'Allowed by @alice on 20 October 2026'],
    [{ allowed: true, by: 'alice', at: '2026-10-20T23:30:00Z' }, 'Allowed by @alice on 20 October 2026'],
    [{ allowed: true, at: '2026-10-20T09:00:00Z' }, 'Allowed on 20 October 2026'],
    [{ allowed: true, by: 'alice' }, 'Allowed by @alice'],
    [{ allowed: true, by: 'alice', at: 'yesterday' }, 'Allowed by @alice'],
    [{ allowed: true }, 'Allowed'],
  ])('%o', (f, want) => {
    expect(allowedLine(f)).toBe(want);
  });
});
