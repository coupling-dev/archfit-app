import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { readOwnerView, ownerViewPath, outcomeText, ownerFileVersion, type OwnerKind } from './owner';
import { diffLines } from './diff';

const route = { action: 'first' as const, pull: 0, owner: 'acme', name: 'app', fullName: 'acme/app' };
const fixture = (name: string): unknown =>
  JSON.parse(
    readFileSync(new URL(`../../../internal/control/ui/testdata/owner/${name}.json`, import.meta.url), 'utf8'),
  );

describe('owner file presentation', () => {
  it.each([
    ['approved', 'Previously approved'],
    ['base', 'Pull request base'],
    ['head', 'Pull request'],
    ['workflow-base', 'Default branch'],
    ['workflow-head', 'Pull request'],
    ['current', 'Default branch'],
    ['outside', 'Default branch'],
    ['result', 'Proposed result'],
    ['unrecognized', 'File version'],
  ])('labels %s as %s', (role, label) => {
    expect(ownerFileVersion(role)).toBe(label);
  });

  it('preserves unified diff bytes and distinguishes headers from changed content', () => {
    const text =
      '--- a/file\n+++ b/file\n@@ -1,3 +1,3 @@\n same\n--- content\n+++ content\n@@ -9 +9 @@\n-old\r\n+new\n\\ No newline at end of file';
    const lines = diffLines(text);
    expect(lines.map((line) => line.kind)).toEqual([
      'header',
      'header',
      'hunk',
      'ctx',
      'del',
      'add',
      'hunk',
      'del',
      'add',
      'ctx',
    ]);
    expect(lines.map((line) => line.text).join('')).toBe(text);
  });

  it('does not discard unrecognized lines or invent changes for an empty diff', () => {
    expect(diffLines('')).toEqual([]);
    expect(diffLines('Line endings: LF → CRLF\n')).toEqual([
      { text: 'Line endings: LF → CRLF\n', kind: 'ctx' },
    ]);
    expect(diffLines('@ hunk\n+added\n-removed\n context\n')).toEqual([
      { text: '@ hunk\n', kind: 'hunk' },
      { text: '+added\n', kind: 'add' },
      { text: '-removed\n', kind: 'del' },
      { text: ' context\n', kind: 'ctx' },
    ]);
  });
});

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
  it.each([true, false])('reads the passkey flag: %s', (passkey) => {
    const raw = { ...(fixture('first') as object), passkey };
    expect(readOwnerView(raw, route).passkey).toBe(passkey);
  });
  it.each([undefined, null, 'false', 1])('refuses a malformed passkey flag: %j', (passkey) => {
    const raw = { ...(fixture('first') as object), passkey };
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
