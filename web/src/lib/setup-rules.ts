import type { HomeSetup } from './home';
import type { Repo } from './route';

interface RuleHelp {
  title: string;
  explanation: string;
  instructions: string[];
  links: { label: string; href: string }[];
}

// The three GitHub steps shared by every ruleset row are stated once, under the Setup step, not here.
export function setupRuleHelp(rule: HomeSetup['rules'][number], repo: Repo): RuleHelp {
  const github = `https://github.com/${encodeURIComponent(repo.owner)}/${encodeURIComponent(repo.name)}`;
  const rules = { label: 'Repository rulesets', href: `${github}/settings/rules` };
  const installations = [
    { label: 'Personal-account App installations', href: 'https://github.com/settings/installations' },
    {
      label: `Organization App installations (${repo.owner})`,
      href: `https://github.com/organizations/${encodeURIComponent(repo.owner)}/settings/installations`,
    },
  ];
  // Only a row the App cannot read asks for the installation settings; the App deliberately holds no Administration permission.
  const unreadable = 'The App cannot read this. Check it in Settings → Rules by hand, then select Check again.';

  switch (rule.code) {
    case 'gate_check':
      return {
        title: 'Require archfit/gate',
        explanation:
          rule.state === 'ok'
            ? 'GitHub does not merge a change until archfit/gate passes.'
            : 'Until this is on, a failing archfit/gate check does not block a merge.',
        instructions: [
          'Tick Require status checks to pass, add archfit/gate, and choose the Archfit app as its source.',
          'If the check is not listed yet, run the Archfit workflow on a pull request first.',
        ],
        links: [rules],
      };
    case 'pull_requests':
      return {
        title: 'Require a pull request before merging',
        explanation:
          rule.state === 'ok'
            ? 'Changes to the default branch go through a pull request.'
            : 'Without this, a change can be pushed straight to the default branch.',
        instructions: ['Tick Require a pull request before merging.'],
        links: [rules],
      };
    case 'administrator_bypass':
      return {
        title: 'Administrator bypass',
        explanation:
          'Optional. Keep an emergency bypass for administrators only; a bypass skips the required check. The App cannot read bypass lists, so check Bypass list under each default-branch ruleset yourself.',
        instructions: [],
        links: [rules],
      };
    case 'app_bypass':
      return {
        title: 'Keep the App subject to branch rules',
        explanation: rule.text || 'Remove the Archfit app from the bypass list, so its own changes cannot skip the required check.',
        instructions: ['Remove the Archfit app from Bypass list, then save.'],
        links: [rules],
      };
    case 'policy_push':
      if (rule.state === 'not_applicable')
        return {
          title: 'Restrict .archfit.yaml',
          explanation: 'This GitHub restriction does not apply to this repository, so there is nothing to do.',
          instructions: [],
          links: [],
        };
      return {
        title: 'Restrict .archfit.yaml',
        explanation:
          rule.state === 'ok'
            ? 'Direct edits to .archfit.yaml are blocked.'
            : rule.state === 'unknown'
              ? 'The App cannot confirm that direct edits to .archfit.yaml are blocked.'
              : 'Without this, .archfit.yaml can change without an approved policy.',
        instructions: [
          'Add a push ruleset that restricts file paths to .archfit.yaml, set Enforcement status to Active, and let the Archfit app bypass it (Always) so it can write approved policy pull requests.',
        ],
        links: [rules],
      };
    case 'policy_push_bypass':
      return {
        title: 'Let the App write policy pull requests',
        explanation: rule.text || 'The App needs bypass on the policy push ruleset to write approved policy pull requests.',
        instructions: ['Add the Archfit app to Bypass list on that ruleset with mode Always. Do not grant it bypass on branch rules.'],
        links: [rules],
      };
    case 'rulesets':
    case 'branch_rules':
      return {
        title: rule.code === 'rulesets' ? 'Read repository rulesets' : 'Read default-branch rules',
        explanation:
          rule.code === 'rulesets'
            ? 'The App cannot read rulesets, so it cannot confirm the required check or bypass access.'
            : 'The App cannot read the default-branch rules, so it cannot confirm the required check.',
        instructions: [unreadable],
        links: [...installations, rules],
      };
    case 'integration':
      return {
        title: 'Identify the Archfit app',
        explanation: 'The required check cannot be matched to the Archfit app until its integration ID can be read.',
        instructions: ['Make sure this repository is selected in the App installation, then select Check again.'],
        links: [...installations, rules],
      };
    case 'approvals':
      return {
        title: 'GitHub pull request approvals',
        explanation:
          'Optional. GitHub approvals are separate from the owner’s passkey approval; one does not replace the other.',
        instructions: ['Set Required approvals to the count your team needs. For a solo repository, use 0.'],
        links: [rules],
      };
    default:
      return {
        title: 'Repository rule',
        explanation: rule.text || 'The App could not explain this repository rule.',
        instructions: [],
        links: [rules],
      };
  }
}
