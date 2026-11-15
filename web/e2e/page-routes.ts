// Shared by the browser words spec and the route-parity gate; neither server routes nor client kinds come from here.
export const pageRoutes = [
  { kind: 'home', path: '/', heading: 'Repositories' },
  { kind: 'admin', path: '/admin', heading: 'Installations' },
  { kind: 'repo', path: '/r/acme/app', heading: 'Home' },
  { kind: 'policy', path: '/r/acme/app/policy', heading: 'Policy' },
  { kind: 'pull', path: '/r/acme/app/pull/4', heading: 'Archfit: pass' },
  { kind: 'request', path: '/r/acme/app/requests/431', heading: 'Request #431' },
  { kind: 'owner', path: '/r/acme/app/owner/first', heading: 'Approve the first policy' },
  { kind: 'owner', path: '/r/acme/app/owner/workflow?pull=7', heading: 'Approve the workflow' },
  { kind: 'owner', path: '/r/acme/app/owner/confirm', heading: 'Confirm the policy' },
  { kind: 'owner', path: '/r/acme/app/owner/revert', heading: 'Revert the policy' },
  { kind: 'passkeys', path: '/r/acme/app/passkeys', heading: 'Passkeys' },
  { kind: 'unknown', path: '/not-a-page', heading: 'This page cannot be shown' },
];
