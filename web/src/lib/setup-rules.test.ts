import { describe, expect, it } from 'vitest';
import { setupRuleHelp } from './setup-rules';

const repo = { owner: 'acme', name: 'app', fullName: 'acme/app' };
const help = (code: string, state = 'warning', text = '') => setupRuleHelp({ code, state, text }, repo);

describe('setup rule guidance', () => {
  it.each([
    'branch_rules',
    'integration',
    'gate_check',
    'pull_requests',
    'administrator_bypass',
    'app_bypass',
    'policy_push',
    'policy_push_bypass',
    'rulesets',
    'approvals',
  ])('%s explains the setting and gives a concrete action at the repository settings', (code) => {
    for (const state of ['ok', 'unknown', 'warning']) {
      const result = help(code, state);
      expect(result.title).not.toBe('');
      expect(result.explanation).not.toBe('');
      expect(result.instructions.length).toBeGreaterThan(0);
      expect(result.links).toContainEqual({
        label: 'Repository rulesets',
        href: 'https://github.com/acme/app/settings/rules',
      });
    }
  });

  it('explains how to require the exact check from the correct app in both GitHub settings', () => {
    const result = help('gate_check');
    expect(result.explanation).toContain('only stops a merge when that check is required');
    const instructions = result.instructions.join(' ');
    for (const value of [
      'Active',
      'default branch',
      'Require status checks to pass',
      'archfit/gate',
      'Archfit app as its source',
      'classic branch protection',
      'run the Archfit workflow',
    ]) {
      expect(instructions).toContain(value);
    }
    expect(result.links).toContainEqual({
      label: 'Classic branch protection',
      href: 'https://github.com/acme/app/settings/branches',
    });
  });

  it.each(['administrator_bypass', 'rulesets', 'branch_rules'])(
    '%s gives permission and manual inspection paths',
    (code) => {
      const result = help(code, 'unknown');
      expect(result.links).toEqual(
        expect.arrayContaining([
          { label: 'Personal-account App installations', href: 'https://github.com/settings/installations' },
          {
            label: 'Organization App installations (acme)',
            href: 'https://github.com/organizations/acme/settings/installations',
          },
        ]),
      );
      expect(result.instructions.join(' ')).toContain('Administration: Read-only');
      expect(result.instructions.join(' ')).toContain('not something this setup asks you to grant');
        expect(result.instructions.join(' ')).toContain('To check by hand');
      expect(result.instructions.join(' ')).toContain('Bypass list');
    },
  );

  it('explains that unreadable administrator bypass is advisory, not a permission request', () => {
    const result = help('administrator_bypass', 'unknown');
    expect(result.explanation).toContain('does not hold Administration permission');
    expect(result.explanation).toContain('does not block the required check');
    expect(result.instructions.join(' ')).toContain('no bypass actors');
    expect(result.instructions.join(' ')).toContain('administrators only for emergencies');
    expect(result.instructions.join(' ')).toContain(
      'an unreadable list stays marked Verify yourself',
    );
    expect(result.instructions.join(' ')).toContain('An empty list is stricter');
    expect(help('administrator_bypass').explanation).toContain('Administrator Always bypass is missing');
    expect(help('administrator_bypass', 'ok').explanation).toContain('can skip the required check');
  });

  it('distinguishes removing branch bypass from allowing policy push bypass and preserves the ruleset name', () => {
    const branch = help('app_bypass', 'warning', 'The App can bypass branch ruleset protect-main.');
    const push = help(
      'policy_push_bypass',
      'unknown',
      'The App bypass for policy push ruleset policy-only cannot be read.',
    );
    expect(branch.explanation).toContain('protect-main');
    expect(branch.instructions.join(' ')).toContain('Remove the Archfit app');
    expect(push.explanation).toContain('policy-only');
    expect(push.instructions.join(' ')).toContain('Add or keep the Archfit app with bypass mode Always');
    expect(push.instructions.join(' ')).toContain('do not grant it bypass on branch rulesets');
  });

  it('gives no work for inapplicable policy restrictions', () => {
    const result = help('policy_push', 'not_applicable');
    expect(result.explanation).toContain('organization-owned private or internal repositories');
    expect(result.explanation).toContain('nothing to do');
    expect(result.instructions).toEqual([]);
    expect(result.explanation).not.toContain('Not available');
  });

  it('names the policy file restriction and the required app exception', () => {
    const result = help('policy_push');
    for (const value of [
      'New push ruleset',
      'Active',
      'Restrict file paths',
      '.archfit.yaml',
      'Archfit app with Always',
    ]) {
      expect(result.instructions.join(' ')).toContain(value);
    }
  });

  it('keeps an unknown finding visible with its server explanation', () => {
    const result = help('future_rule', 'unknown', 'A new rule could not be checked.');
    expect(result.explanation).toBe('A new rule could not be checked.');
    expect(result.links[0].href).toBe('https://github.com/acme/app/settings/rules');
  });

  it('encodes repository path segments in GitHub links', () => {
    const result = setupRuleHelp(
      { code: 'rulesets', state: 'unknown', text: '' },
      { owner: 'a/b', name: 'c?d', fullName: 'a/b/c?d' },
    );
    expect(result.links.map((link) => link.href)).toContain('https://github.com/a%2Fb/c%3Fd/settings/rules');
    expect(result.links.map((link) => link.href)).toContain(
      'https://github.com/organizations/a%2Fb/settings/installations',
    );
  });
});
