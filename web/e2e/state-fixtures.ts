import { readFileSync } from 'node:fs';
import type { Policy, RequestView } from '../src/lib/api';
import { readHome } from '../src/lib/home';
import type { Reply } from './support';

// Contract fixtures, not captured production data. See docs/ui-state-fixtures.md for each source.
function fixture<T>(name: string): T {
  return JSON.parse(
    readFileSync(new URL('../../internal/control/ui/testdata/' + name + '.json', import.meta.url), 'utf8'),
  );
}

const closed = fixture<RequestView>('request/open');
closed.state = 'closed';

const unknownRuleset = readHome(fixture('home/not-set-up'));
if (!unknownRuleset.setup) throw new Error('The setup fixture must include setup.');
unknownRuleset.setup.rules = [
  { code: 'pull_requests', state: 'ok', text: '' },
  { code: 'rulesets', state: 'unknown', text: '' },
  { code: 'gate_check', state: 'ok', text: '' },
  { code: 'policy_push', state: 'not_applicable', text: '' },
  { code: 'administrator_bypass', state: 'unknown', text: '' },
];

// The same deterministic identifier stress case as policy-reading.spec.ts; no new policy facts.
const longNames = fixture<Policy>('policy/pumba');
const module = longNames.modules[0];
if (!module) throw new Error('The policy fixture must include a module.');
const name = 'module_'.repeat(30);
const layer = 'layer_'.repeat(30);
longNames.modules = [{ ...module, name, layer, paths: [name + '/**'], depends_on: [name] }];
longNames.layers = [layer];

interface StateFixture {
  name: string;
  path: string;
  api: string;
  reply: Reply;
  heading: string;
  text: string;
}

export const stateFixtures: StateFixture[] = [
  {
    name: 'waiting-installation',
    path: '/r/octo/tool',
    api: '/api/repos/octo/tool/home',
    reply: [403, { error: 'This account is waiting to be admitted.', waiting: 'octo' }],
    heading: 'Check pull requests against your architecture policy',
    text: 'Your installation on octo is waiting to be admitted.',
  },
  {
    name: 'empty-list',
    path: '/',
    api: '/api/repos',
    reply: [200, fixture('repos/no-repositories')],
    heading: 'Repositories',
    text: 'No repository yet',
  },
  {
    name: 'missing-report',
    path: '/r/acme/app/pull/4',
    api: '/api/repos/acme/app/pulls/4',
    reply: [200, fixture('pull/no-result')],
    heading: 'Pull request #4',
    text: 'No check has reported for this pull request. It runs on the next push.',
  },
  {
    name: 'expired-session',
    path: '/r/acme/app/pull/4',
    api: '/api/session',
    reply: [401, { error: 'Sign in to continue.' }],
    heading: 'Check pull requests against your architecture policy',
    text: 'Sign in with GitHub',
  },
  {
    name: 'closed-request',
    path: '/r/acme/app/requests/431',
    api: '/api/repos/acme/app/requests/431',
    reply: [200, closed],
    heading: 'Request #431',
    text: 'No answer is open.',
  },
  {
    name: 'denied-role',
    path: '/r/acme/app/pull/4',
    api: '/api/repos/acme/app/pulls/4',
    reply: [200, fixture('pull/unknown-workflow-viewer')],
    heading: 'Pull request #4',
    text: 'An architecture owner named in the approved policy can review this.',
  },
  {
    name: 'unknown-ruleset',
    path: '/r/acme/app',
    api: '/api/repos/acme/app/home',
    reply: [200, unknownRuleset],
    heading: 'acme/app',
    text: 'Set up Archfit',
  },
  {
    name: 'stale-owner-action',
    path: '/r/acme/app/owner/workflow?pull=7',
    api: '/api/repos/acme/app/owner/workflow/view',
    reply: [409, { error: 'The repository no longer needs this action. Reload the page.' }],
    heading: 'Approve the workflow change',
    text: 'Nothing to approve: this repository does not need a workflow approval now.',
  },
  {
    name: 'long-names',
    path: '/r/acme/app/policy',
    api: '/api/repos/acme/app/policy',
    reply: [200, longNames],
    heading: 'Policy',
    text: name,
  },
];
