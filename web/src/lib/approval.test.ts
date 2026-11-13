import { describe, expect, it } from 'vitest';
import type { Approval } from './api';
import { digestWord } from './approval';

const full = 'ab'.repeat(32);
const missing: Approval = {
  head_sha: 'a'.repeat(40),
  base_sha: 'b'.repeat(40),
  paths: ['.archfit-baseline.json', '.archfit-labels.yaml'],
  digests: { '.archfit-baseline.json': full, '.archfit-labels.yaml': '' },
  short_digests: { '.archfit-baseline.json': full.slice(0, 7), '.archfit-labels.yaml': '' },
  state: 'missing',
  can_decide: true,
  can_approve: true,
  can_withdraw: false,
  notification_problem: false,
  owners: ['alice'],
  settings_ref: 'b'.repeat(40),
  settings_url: 'https://github.com/acme/repo/blob/' + 'b'.repeat(40) + '/.archfit-app.yaml',
  gate_mode: 'required',
  required: true,
  decision_url: 'https://app.example/r/acme/repo/pull/1',
  objections: [],
  history: [],
  author_is_agent: false,
};

describe('digestWord', () => {
  it('names the short digest of a file and says a deletion is what is approved', () => {
    expect(digestWord(missing, '.archfit-baseline.json')).toBe('sha256 abababa');
    expect(digestWord(missing, '.archfit-labels.yaml')).toBe('deleted by this commit');
    expect(digestWord({ ...missing, digests: null }, '.archfit-baseline.json')).toBe('not read: the head is in a fork');
  });
});
