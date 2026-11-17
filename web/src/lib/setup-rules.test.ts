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
  ])('%s has a title and an explanation at every state', (code) => {
    for (const state of ['ok', 'unknown', 'warning']) {
      const result = help(code, state);
      expect(result.title).not.toBe('');
      expect(result.explanation).not.toBe('');
    }
  });

  it('says only what to tick for the required rows; the shared GitHub steps are not repeated', () => {
    const gate = help('gate_check').instructions.join(' ');
    expect(gate).toContain('Tick Require status checks to pass, add archfit/gate, and choose the Archfit app as its source.');
    expect(gate).not.toContain('Enforcement status');
    expect(help('pull_requests').instructions).toEqual(['Tick Require a pull request before merging.']);
  });

  it('folds the administrator bypass row into an optional note with no action of its own', () => {
    const result = help('administrator_bypass', 'unknown');
    expect(result.explanation).toMatch(/^Optional\./);
    expect(result.explanation).toContain('The App cannot read bypass lists');
    expect(result.instructions).toEqual([]);
  });

  it('names the approvals row optional and keeps it out of the required check', () => {
    expect(help('approvals', 'warning').explanation).toMatch(/^Optional\./);
  });

  it('keeps the App subject to branch rules and removes it from the bypass list', () => {
    const result = help('app_bypass', 'warning', 'The App can bypass branch ruleset protect-main.');
    expect(result.explanation).toBe('The App can bypass branch ruleset protect-main.');
    expect(result.instructions.join(' ')).toContain('Remove the Archfit app from Bypass list');
  });

  it('names the policy file restriction and the App exception', () => {
    const result = help('policy_push').instructions.join(' ');
    expect(result).toContain('restricts file paths to .archfit.yaml');
    expect(result).toContain('set Enforcement status to Active');
    expect(result).toContain('let the Archfit app bypass it (Always)');
  });

  it('points an unreadable row at a manual check, not at the permission it lacks', () => {
    for (const code of ['rulesets', 'branch_rules']) {
      const result = help(code, 'unknown');
      expect(result.instructions.join(' ')).toContain('Check it in Settings → Rules by hand');
      expect(result.instructions.join(' ')).not.toContain('Administration');
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
