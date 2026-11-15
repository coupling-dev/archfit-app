import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { readOwnerView, ownerViewPath, outcomeText, type OwnerKind } from './owner';

const route = { action: 'first' as const, pull: 0, owner: 'acme', name: 'app', fullName: 'acme/app' };
const fixture = (name: string): unknown =>
  JSON.parse(
    readFileSync(new URL(`../../../internal/control/ui/testdata/owner/${name}.json`, import.meta.url), 'utf8'),
  );

describe('owner views', () => {
  it.each(['first', 'first-pull', 'workflow', 'confirm', 'revert', 'revert-answers'])(
    'reads the API golden %s',
    (name) => {
      const action = (name === 'revert-answers' ? 'revert' : name === 'first-pull' ? 'first' : name) as OwnerKind;
      const pull = name === 'first-pull' || name === 'workflow' ? 7 : 0;
      const v = readOwnerView(fixture(name), { ...route, action, pull });
      expect(v.kind).toBe(action);
      expect(v.action_digest).not.toBe(v.digest);
    },
  );
  it.each([null, {}, { status: 'unknown', files: [], diffs: [] }])('refuses malformed JSON: %j', (raw) => {
    expect(() => readOwnerView(raw, route)).toThrow('The owner action could not be read.');
  });
  it('refuses another action or pull', () => {
    expect(() => readOwnerView(fixture('first'), { ...route, action: 'confirm' })).toThrow();
    expect(() => readOwnerView(fixture('first'), { ...route, pull: 7 })).toThrow();
  });
  it('keeps the query binding', () => {
    expect(ownerViewPath(route)).toBe('owner/first/view');
    expect(ownerViewPath({ ...route, action: 'workflow', pull: 7 })).toBe('owner/workflow/view?pull=7');
  });
  it('never claims that an approval is a passing check', () => {
    expect(outcomeText('workflow', { approval: 'a1', check: 'blocked' })).toContain('could not run again');
    expect(outcomeText('first', { approval: 'a1', check: 'pending' })).toContain('waits for its run');
    expect(outcomeText('revert', { approval: 'a1', pull_request: 12, state: 'refused' })).toContain('has not merged');
  });
});
