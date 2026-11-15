# Read-only UI state fixtures

The browser suite fills gaps in the live UI audit without changing production.
Run `npm --prefix web run docs:shots` after `npm --prefix web run build` to
capture each state at 390×844 and 1280×900. These are **local contract fixtures,
not live screenshots**.

[User navigation](user-guide.md#repositories) uses `/` for Repositories.
`/repos` remains unknown in the router unit table and browser route-parity suite.
No compatibility route was added.

## State sources

The cases in [state-fixtures.ts](../web/e2e/state-fixtures.ts) reuse checked-in
API fixtures and existing test variants. They add no production facts or API fields.

| State                | Source / deliberate test variant                                                                              | What the browser checks                                                               |
| -------------------- | ------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------- |
| Waiting installation | Existing `fixtures.ts` 403 + `waiting: octo` response                                                         | Named account, no repository tabs or sign-in action                                   |
| Empty list           | `internal/control/ui/testdata/repos/no-repositories.json`                                                     | Empty message, no invented repository                                                 |
| Missing report       | `internal/control/ui/testdata/pull/no-result.json`                                                            | Existing PR with no result, not pass or missing repository                            |
| Expired session      | Existing session 401 contract, also returned by the concurrent pull read                                      | Sign-in link preserves the requested PR URL, no repository tabs                       |
| Closed request       | `request/open.json` with `state: closed`, as in `request.spec.ts`                                             | Read-only request, no answer buttons                                                  |
| Denied role          | `pull/unknown-workflow-viewer.json`                                                                           | Named owner role without an owner-action link; not an invented access verdict         |
| Unknown ruleset      | `home/not-set-up.json` with the existing `home.spec.ts` unknown/not-applicable rule matrix                    | Unknown still requires an admin check; unavailable is separate; progress stays 2 of 4 |
| Stale owner action   | 409 `owner_not_due` sentence from `internal/control/ui/wording.go`                                            | Error is visible, no approval control                                                 |
| Long names           | `policy/pumba.json` with the deterministic long module/layer strings already used in `policy-reading.spec.ts` | Expanded module details wrap at both widths; no added approval or enforcement facts   |

The stale-action and closed-request cases record the current UI, including its
existing recovery/answer wording. They do not claim those audit findings are fixed.

All cases use the production build and CSP, a fixed browser clock, light/dark axe
checks, body-width assertions, text goldens, and screenshots under
`docs/images/states/<state>-<width>.png`. The read-only helper aborts every method
other than GET/HEAD/OPTIONS before dispatch, including auth forms outside `/api`.
Each case asserts that no write was attempted. No passkey ceremony or final
approval is triggered. Existing write-flow tests remain separate.

## Landing and colour identity

The landing page uses author/owner and Pass/Fail/Waiting for owner/No verdict.
It describes CI checks and App report/policy reads rather than promising that
code never leaves CI. Footer links have a visible, assistive-technology-hidden separator.

Removed unused CSS tokens: `--bg-inset`, `--bg-emphasis`, `--shadow-md`,
`--radius-lg`. No application source consumed them. The light/dark computed
colour goldens in [token-colours.spec.ts](../web/e2e/token-colours.spec.ts) were
captured **before removal**, then matched after removal. They cover body, header,
brand, primary action and hover, flow cards, verdict colours, and footer.
Active colour values and the visual identity are unchanged; the goldens are
not a claim of exhaustive visual or accessibility certification.

## Screenshot pairs

Each link below has a corresponding `-1280.png` desktop capture.

- [Landing](images/states/landing-390.png)
- [Waiting installation](images/states/waiting-installation-390.png)
- [Empty list](images/states/empty-list-390.png)
- [Missing report](images/states/missing-report-390.png)
- [Expired session](images/states/expired-session-390.png)
- [Closed request](images/states/closed-request-390.png)
- [Denied role](images/states/denied-role-390.png)
- [Unknown ruleset](images/states/unknown-ruleset-390.png)
- [Stale owner action](images/states/stale-owner-action-390.png)
- [Long names](images/states/long-names-390.png)
