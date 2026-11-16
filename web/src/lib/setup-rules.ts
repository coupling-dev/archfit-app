import type { HomeSetup } from './home';
import type { Repo } from './route';

interface RuleHelp {
  title: string;
  explanation: string;
  instructions: string[];
  links: { label: string; href: string }[];
}

export function setupRuleHelp(rule: HomeSetup['rules'][number], repo: Repo): RuleHelp {
  const github = `https://github.com/${encodeURIComponent(repo.owner)}/${encodeURIComponent(repo.name)}`;
  const rules = { label: 'Repository rulesets', href: `${github}/settings/rules` };
  const branches = { label: 'Classic branch protection', href: `${github}/settings/branches` };
  // The setup response has no owner type, so label both installation paths rather than guessing.
  const installations = [
    { label: 'Personal-account App installations', href: 'https://github.com/settings/installations' },
    {
      label: `Organization App installations (${repo.owner})`,
      href: `https://github.com/organizations/${encodeURIComponent(repo.owner)}/settings/installations`,
    },
  ];
  const permission =
    'The App deliberately does not hold Administration permission. To inspect its access, open the App installations for the account that owns this repository → Configure Archfit → Permissions. Administration: Read-only would be needed to read bypass lists; it is not something this setup asks you to grant.';
  const branchRule =
    'Open Repository rulesets, create or edit a branch ruleset, set Enforcement status to Active, and target the default branch.';

  switch (rule.code) {
    case 'gate_check':
      return {
        title: 'Require archfit/gate',
        explanation:
          rule.state === 'ok'
            ? 'GitHub requires the Archfit app’s archfit/gate check before merging.'
            : 'archfit/gate reports a result; GitHub only stops a merge when that check is required.',
        instructions: [
          `${branchRule} Enable Require status checks to pass, add archfit/gate, and select the Archfit app as its source. Save changes.`,
          'If you use classic branch protection instead, edit the default-branch rule under Branches → Require status checks to pass before merging → add archfit/gate and select the Archfit app. If the check is not listed yet, run the Archfit workflow on a pull request first.',
        ],
        links: [rules, branches],
      };
    case 'pull_requests':
      return {
        title: 'Require pull requests',
        explanation:
          rule.state === 'ok'
            ? 'Default-branch changes require a pull request, so changes go through checks before merging.'
            : 'Direct pushes can skip pull request checks; require a pull request for default-branch changes.',
        instructions: [
          `${branchRule} Enable Require a pull request before merging and save changes. In classic branch protection, enable the same option for the default branch.`,
        ],
        links: [rules, branches],
      };
    case 'administrator_bypass':
      return {
        title: 'Administrator bypass',
        explanation:
          rule.state === 'unknown'
            ? 'The App does not hold Administration permission, so it cannot read this ruleset’s bypass list. Verify it yourself; this row does not block the required check.'
            : rule.state === 'ok'
              ? 'Every applicable branch ruleset allows administrators to bypass for emergencies; this access can skip the required check.'
              : 'Administrator Always bypass is missing from at least one applicable branch ruleset, or no applicable branch ruleset was found.',
        instructions: [
          'To check by hand, open Repository rulesets, then each ruleset targeting the default branch → Bypass list. Recommended: no bypass actors, or repository/organization administrators only for emergencies. Remove other actors; if emergency access is needed, set the administrator bypass mode to Always. Save changes.',
          permission,
          'An empty list is stricter and can be intentional. This is advisory: an unreadable list stays marked Verify yourself even when the required check is On.',
        ],
        links: [...installations, rules],
      };
    case 'app_bypass':
      return {
        title: 'Keep the App subject to branch rules',
        explanation: rule.text || 'The App must follow branch rules so its changes cannot skip the required check.',
        instructions: [
          'Open Repository rulesets → the named branch ruleset → Bypass list. Remove the Archfit app and save changes. Do this for each branch ruleset targeting the default branch; it keeps the required check effective.',
        ],
        links: [rules],
      };
    case 'policy_push':
      return rule.state === 'not_applicable'
        ? {
            title: 'Policy-push restriction',
            explanation:
              'This GitHub restriction exists only for organization-owned private or internal repositories, so it does not apply here and there is nothing to do.',
            instructions: [],
            links: [rules],
          }
        : {
            title: 'Policy-push restriction',
            explanation:
              rule.state === 'ok'
                ? 'An active push ruleset restricts .archfit.yaml, protecting the policy from direct edits.'
                : rule.state === 'unknown'
                  ? 'The App cannot confirm the .archfit.yaml push restriction, which protects the policy from direct edits.'
                  : 'Without a push restriction, .archfit.yaml can be changed directly rather than through an approved policy proposal.',
            instructions: [
              'Open Repository rulesets → New ruleset → New push ruleset (or edit the policy push ruleset). Set Enforcement status to Active, enable Restrict file paths, and add .archfit.yaml. In Bypass list, allow the Archfit app with Always mode so it can write approved policy proposals; keep any administrator bypass for emergencies only. Save changes.',
            ],
            links: [rules],
          };
    case 'policy_push_bypass':
      return {
        title: 'Let the App write policy proposals',
        explanation: rule.text || 'The App needs push-ruleset bypass to write approved policy proposals.',
        instructions: [
          'Open Repository rulesets → the named policy push ruleset → Bypass list. Add or keep the Archfit app with bypass mode Always, then save changes. This lets the App write approved policy proposals; do not grant it bypass on branch rulesets.',
        ],
        links: [rules],
      };
    case 'rulesets':
    case 'branch_rules':
      return {
        title: rule.code === 'rulesets' ? 'Read repository rulesets' : 'Read default-branch rules',
        explanation:
          rule.code === 'rulesets'
            ? 'The App cannot read rulesets, so it cannot confirm required checks or bypass access.'
            : 'The App cannot read default-branch rules, so it cannot confirm that GitHub requires pull requests and archfit/gate.',
        instructions: [
          permission,
          'To check by hand, open Repository rulesets and inspect each active ruleset targeting the default branch. Require a pull request and archfit/gate from the Archfit app; check each Bypass list for unintended access.',
        ],
        links: [...installations, rules],
      };
    case 'integration':
      return {
        title: 'Identify the Archfit app',
        explanation:
          'The App’s integration ID could not be read, so the required check cannot be matched to the Archfit app.',
        instructions: [
          'Open the App installations for the account that owns this repository → Configure Archfit → Repository access. Make sure this repository is selected, then select Check here. If the ID still cannot be read, ask the App operator to check its GitHub credentials.',
        ],
        links: [...installations, rules],
      };
    case 'approvals':
      return {
        title: 'GitHub pull request approvals',
        explanation:
          'GitHub requires pull request approvals separately from the Archfit owner’s passkey approval; one does not replace the other.',
        instructions: [
          'Open Repository rulesets → the default-branch ruleset → Require a pull request before merging → Required approvals. Keep the count your team requires and obtain those GitHub approvals as well as Archfit owner approval. For a solo repository, use 0 if no separate reviewer is available.',
        ],
        links: [rules, branches],
      };
    default:
      return {
        title: 'Repository rule',
        explanation: rule.text || 'The App could not explain this repository rule.',
        instructions: [
          'Open Repository rulesets to inspect the setting with a repository administrator, then select Check here.',
        ],
        links: [rules],
      };
  }
}
