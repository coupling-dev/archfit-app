# Archfit App UI review: Guardrails prototype on pilot data

Status: design review, 18 October 2026. Prototype: [`prototypes/index.html`](prototypes/index.html).
Reviewed as a peer chat: Claude designed and edited, Codex reviewed and challenged (read-only).

Inputs: the pilot repository `coupling-dev/pumba-pilot` (Go). Reports: `master` at `aed8b36` and PR #4 at
`9d33f4d` (adds a module cycle `pkg_chaos` ⇄ `pkg_container`). Live GitHub: the `archfit/state` check run 117634137811,
the PR #4 comment, setup PRs #2, #3 and the baseline PR #5 (merged 09:17:54Z, after the PR #4 run).

## Summary

The earlier prototype showed a product that the data cannot support yet: an `acme` organization with loops, owner tasks
the engine does not emit (`needs_owner_decision` for coupling), and two green checks. The real pilot shows three
problems that matter more:

1. **The headline contradicts the effect.** PR #4 says "Blocked by architecture findings" and "Fix the blocking
   findings". The gate is advisory, `archfit/state` is neutral, there is no `archfit/gate`, and GitHub lets the PR merge.
2. **"New" is not "added by this PR".** With no baseline, all 6 coupling advisories on `master` show as new on PR #4.
   With a baseline, "new" means "not accepted". "Introduced" needs a PR comparison (`--base`), which the pilot
   workflow does not run.
3. **Too much in one comment.** About 13 KB: 8 findings with location lists, 9 dimension lines, 16 gap lines, identity,
   a validation block and a 4 KB JSON block.

The prototype was rebuilt as a repository-scoped App on the pilot data, with a scenario switch that shows the real run
next to projected ones. A fourth problem came out of the review: **in advisory mode a PR result goes out of date
silently** when the baseline or policy changes on `master` (see the PR screen).

## Simplification pass (v2)

Owner feedback on v1: too many views, too many words, unclear actions. v2 keeps the content and cuts the surface.

| v1                                                   | v2                                                                              |
| ---------------------------------------------------- | ------------------------------------------------------------------------------- |
| 8 tabs                                               | 3 tabs, one question each: Overview, Pull requests, Architecture                |
| Decisions tab                                        | "Needs you" on Overview: one card per decision, one primary button              |
| Setup tab                                            | a progress track on Overview                                                    |
| Evidence tab                                         | a folded strip of 9 dimensions on Overview, with "To measure more"              |
| On GitHub tab                                        | an "Archfit / On GitHub" switch on the PR page; "Today" vs "Proposed" inside it |
| States tab                                           | a line in Design notes                                                          |
| "Use case / Main point / Data" sidebar on every page | removed; field paths appear only with "Data sources" on                         |
| Scenario buttons on two pages                        | one "Demo" select in the header, marked as prototype-only                       |
| Prose about the cycle                                | a two-node diagram: 40 imports one way, 1 the other                             |
| Advisories, locations, policy, YAML always visible   | folded, with the count in the summary                                           |

Codex review of v2 (round 4) restored five things the fold had lost, each without a new tab:

1. Recovery states in place of results: "No report yet", "Action required: report could not be used", "Finish setup".
   Each has one action and hides counts and tasks it cannot support (Demo select shows them).
2. Attribution: a folded line under the status chips, "No baseline in this run · PR attribution unavailable ·
   customer-attested". Advisories say "Also in the master report", not "not this PR's job". The merge box says
   "Archfit does not block merging", not "Merge allowed"; "Merging is blocked" only with a verified ruleset.
3. Outdated result: an "Open workflow run" button next to the chip. "Re-run" stays on GitHub until the App can
   re-run with its own permission.
4. Trust and evidence limits: the customer-attested sentence and per-dimension coverage and confidence in the
   Overview evidence fold.
5. Policy edits are proposals, not owner-only: "Propose change", draft PR, owner approves. Read-only users see
   "View policy". "Request an exception" appears only when the gate fails, and never clears it by itself.

Rules: status first; one primary action per card; details fold away, but nothing needed to act lives only in a fold.
The sections below describe v1 screens; their content now sits in the v2 places above.

## Architecture graph (priority)

Owner feedback on the live page (`/r/{o}/{n}/graph`): it works, but it is unclear and ugly. Causes, from the code:
layers are columns (`static/graph.js`), and the pilot declares no layers, so all 6 modules stand in one column with
links looping to the right; a 9-line paragraph explains five statuses; every link looks the same; two tables repeat
the drawing.

**Question the graph answers:** the policy status of module relationships in this report: does the code follow the
approved policy, and where not? In 5 seconds: how many violations, between which modules. Findings that name no module
pair stay on the check and the PR page.

**Default view**

| Element      | Default                                                                                                          |
| ------------ | ---------------------------------------------------------------------------------------------------------------- |
| Banners      | `View.Stale` and `View.Incomplete` stay, above everything                                                        |
| Status chips | counts per `Link.Status`, also the filter: violation, advisory, accepted (baseline or waiver), allowed, observed |
| Problem list | left side, violations first, then advisories, then accepted; each by count; same visibility rule as the graph    |
| Modules      | ranked by dependency depth when no layers are declared; grouped in declared-layer bands when they are            |
| Links        | color = `Status` as `graph.Build` sets it (never recomputed); width = observed dependencies in 3 steps           |
| Observed     | thin grey, no label, named "observed", not "fine": with an incomplete report it can still break a rule           |
| Cycles       | both directions as two separate curves; a self-link as a loop                                                    |
| Click        | a link, problem or matrix cell: dims the rest, shows the status meaning and the findings behind it               |

**Readable from 6 to 40 modules**

1. Deterministic layout, computed once per report and scope, never per filter, so modules do not move. No layers:
   depth after a cycle break, two barycenter passes, ties by name. Depth is a layout only, never a permitted direction.
   Declared layers: bands in the policy's layer order; names alone give no hierarchy.
2. At most 6 modules per drawn line; a wider rank wraps.
3. Over 15 modules the graph starts in focus mode: modules and links with a violation, advisory or accepted exception.
   Showing allowed or observed links switches to the full graph. With no problem link, the full graph.
4. Matrix view for the full picture at any size: rows depend on columns; position does not establish permission.
5. Larger repositories (proposal, not validated): matrix plus search, grouped by path prefix.

**Data:** existing report facts, but not all reach the page today. `View.Nodes{Name, Layer, PolicyLine}` and
`View.Links{From, To, Count, Status}` feed the drawing. The detail panel needs a new projection: per link, the findings
behind it (rule, kind, status, why), so an advisory's text never stands in for an accepted or observed status. An
allowed link may have no finding. A link with `Count` 0 is finding-only, not proof of zero imports.

**Where:** `internal/control/ui/static/graph.js` (layout, filters, matrix, detail; DOM built with `textContent` and
`setAttribute`, `Map` keys for module names, never `innerHTML` with report strings), `templates/graph.html` (chips,
problem list, detail panel; the hint paragraph goes; Stale/Incomplete banners, policy links and the accessible tables
stay as the no-script fallback), `static/app.css` (new link and node classes), and `graph.Build` or the page handler
for the per-link findings. Prototype: `prototypes/graph-proto.js`.

## Surfaces and their jobs

| Surface                 | Who reads it                   | One job                                       |
| ----------------------- | ------------------------------ | --------------------------------------------- |
| Merge box (check title) | everyone on the PR             | Does Archfit block this merge?                |
| Check run summary       | the author, a reviewer         | Why, in one screen                            |
| PR comment              | the author and coding agents   | What to change, and the exact repair tasks    |
| App PR page             | anyone who follows the link    | Everything about this run                     |
| App repository pages    | architecture owners, the admin | State of `master`, decisions, evidence, setup |

## Headline matrix

The headline states the effect on merge. It comes from the protected gate mode (`.archfit-app.yaml` `gate:`, not in the
report) and the check conclusion, never from `verdict` alone. The raw `verdict` stays in details.

| conclusion, then `verdict`                                        | gate advisory                            | gate required                               |
| ----------------------------------------------------------------- | ---------------------------------------- | ------------------------------------------- |
| `failure` (`verdict: blocked` or `hard_gates: fail`, any verdict) | Architecture violations found · advisory | Architecture gate failing                   |
| `success`, `needs_attention`                                      | Architecture needs attention · advisory  | Architecture gate passed · attention needed |
| `success`, `healthy`                                              | Architecture healthy · advisory          | Architecture gate passed                    |

- No baseline in the run (`gate_reference.baseline_present = false`): append "· no baseline".
- `healthy` with no baseline is not an expected current-engine state (drift is then unmeasured, so `needs_attention`).
- Evidence problems override the matrix in both modes: an unusable or ineligible report is published as "Action
  required: <reason>" with `action_required`, never neutral (`publish.go:105`, no gate-mode condition). Examples:
  `report_invalid`, `missing_upload`, a non-comparable baseline. The automatic `stale_inputs` withdrawal after a change
  on `master` runs only in required mode today (`sweep/rules.go:21`).
- "Merge is blocked" is said only when a ruleset that requires `archfit/gate` is verified. Otherwise "gate failing".

## Screens

### Overview (repository, `master`)

- **Use case:** an owner opens the repository after setup or a week later. Is `master` in policy, does anything need me?
- **Main point:** one state badge for `master`, then open PRs, then owner decisions. Counts, no score.
- **Verdict on the old prototype:** organization overview with "blocked then repaired" loops and agent attribution.
  Good thesis, no data for it in the pilot: one repository, no loop history, no agent PRs. Kept for later.
- **Changes:** repository-scoped page. Stat strip: 0 rule violations, 6 coupling advisories (33 edges), 5/9 dimensions
  measured, 1 open PR with violations. Flash "Baseline merged after this report". Open PRs with their latest result,
  including "no Archfit result" for #1 (opened before setup). Recent setup PRs.
- **Open:** how to tell "no result because opened before setup" from "workflow did not run" without a run lookup.

### Pull request (App page, new)

- **Use case:** the author, a reviewer or an owner follows the link from the check or comment. What is wrong, does it
  block, what do I change?
- **Main point:** the banner: what was found and its effect on merge.
- **Changes:** banner per the matrix; rule violations grouped (one cycle, two findings, one per direction, with the
  location count per direction: 40 vs 1 at `pkg/container/flags.go:3`); advisories as a table with status `new` or
  `baseline`; a panel "What new means here" that separates baseline from PR comparison; an abbreviated agent-task
  preview with "Copy full tasks JSON", which copies the exact `agent_tasks[]` embedded from the run.
- **Scenarios in the prototype:** the real run (no baseline); "result outdated" (#5 merged after the run); and two
  projected ones: re-run with the baseline, gate required.
- **Finding from the review:** in advisory mode nothing marks PR #4 out of date after #5 merges.
  `internal/gate/sweep/rules.go:21` (rule 0) returns before stale detection for advisory repositories; the
  `stale_inputs` re-run in `internal/gate/module.md` (~line 1073) applies to required mode only. Live check
  117634137811 stayed neutral with its 08:56 result. The App page is the only place that can say "result predates
  baseline #5", by comparing the run's baseline digest with `master`.
- **Open:** should advisory mode re-run open PRs when protected inputs change, re-evaluate, and publish the resulting
  conclusion (neutral only if the new report is eligible)? It removes a silent wrong state. Owner decision.

### On GitHub (merge box, check summary, PR comment)

- **Use case:** most people meet Archfit here and never open the App.
- **Main point:** the merge box answers "does it block?" from the check title alone.
- **Changes:** "Published today" next to "Proposed" for all four scenarios.
  - Check title = headline. Today it is "Architecture state: blocked" (`render.go:86`) and the comment heading is
    "Blocked by architecture findings" (`render.go:71`).
  - Check summary (~1 KB): effect and where it comes from; one line per violation group; advisory count; baseline note;
    next step and App link; SHA, run, engine, the customer-attested sentence.
  - PR comment (~1.5 KB plus agent block): headline and effect; the cycle with example locations per direction and the
    fix; "No baseline yet: new does not mean added by this PR"; advisory count and link; the collapsed agent block with
    the exact `agent_tasks[]` and its markers; SHA and trust line.
  - Dimensions, evidence gaps, analyzers and identity move to the App.
- **Rule:** never the word "blocking" in advisory mode. Say "rule violation".

### Architecture

- **Use case:** an owner checks what the policy says and where the code strains it.
- **Main point:** red is a rule the policy enforces, yellow is advice, grey is observed. The map adds no meaning the
  policy lacks: no layers are declared, so position is dependency depth and says so.
- **Changes:** the real 6 modules and 7–8 seams; switch between `master` and PR #4 evidence (PR #4 adds
  `pkg_container → pkg_chaos` and turns `pkg_chaos → pkg_container` into a violation). Views: Map, Rules and modules,
  `.archfit.yaml`. Dropped the Charter/Matrix split and the Published/In review/Draft switch: one rule and six modules
  do not need them. "Propose a change" goes to the existing policy editor.

### Decisions

- **Use case:** an owner with ten minutes. What can only I decide, and where does each option end?
- **Main point:** the question, the facts, the options, each ending in a PR.
- **Changes:** two real decisions instead of five invented ones: declare module volatility (all 6 advisories carry
  `hypothesis = declare_volatility`; coupling unknown "volatility is undeclared on 78 edges"), and make the gate
  required (after rehearsal). A "Decided" list shows #5 and #3. Code repairs (the cycle) belong to the author or an
  agent and are not listed.
- **Codex correction:** volatility is declared in `.archfit.yaml` (`modules.<name>.volatility` or `.subdomain`,
  `archfit.schema.json:382`), not in the labels file (labels pin strength on a module pair). Do not promise that
  declaring stops critical scores: functional coupling uses the worse of both ends, so scores may change either way.
  This is App guidance from report facts, not an engine-emitted owner task.

### Evidence

- **Use case:** how much should I trust this result, and what would make it stronger?
- **Main point:** partial evidence is normal and fixable. Each gap ends in a possible next step.
- **Changes:** 9 dimensions with coverage as "observed / total + basis" (no average), confidence, gate, unknown count;
  the 15 unknown facts grouped by possible next step (declare in the policy; supply deployment evidence; feed more CI
  inputs; re-run with an admissible baseline; outside this report's claim); analyzers by status; inputs and identity.
  `master` and PR #4 switch.
- **Codex corrections:** a `deploy_unit` declaration alone does not close "no corroborating deploy manifest"; coverage
  facts need supplied, resolvable, attributable and fresh coverage, not only "enable"; time does not close drift; do
  not label absent analyzers "not needed" (the report has no per-tool applicability; say "this run reports no
  missing-analyzer coverage gaps").

### Setup

- **Use case:** the admin asks what is done, what is next, and when it is safe to require the gate.
- **Changes:** steps from real PRs (#2, #3, #5), "advisory" as the current step, then rehearse, then require. Settings
  read-only from `master`.

### States

One page of the standard states, same wording everywhere: no report yet, report expired (artifact retention), inputs
changed (required mode), report invalid, partial evidence, rate limited, not found (no confirmation that the repository
exists), loading (header first, never fake numbers).

## Data and API

"Raw" means the field is in the engine artifact but dropped by the App's report projection
(`internal/wire/report/contract.go:22` drops `findings[].matched_by`, `measurement`, seam scores; `Seam` keeps
`id, from_module, to_module, edges, strength, distance, volatility, severity, policy`).

| Screen element                     | Source                                                                                                                                                                                   | Exists today?                            | To build                                                                                                                                                               |
| ---------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Gate mode, effect on merge         | `.archfit-app.yaml` `gate` on the protected ref; check run conclusion                                                                                                                    | yes                                      | —                                                                                                                                                                      |
| "Merge is blocked"                 | `GET /repos/{o}/{r}/rules/branches/{branch}`                                                                                                                                             | no                                       | ruleset read; permission check                                                                                                                                         |
| Headline                           | `verdict`, `decision.{hard_gates,active_blockers}`, gate mode, `gate_reference.baseline_present`                                                                                         | yes                                      | new title mapping in `gate/feedback`                                                                                                                                   |
| Violation groups                   | `findings[].{id,kind,rule_id,status,severity,edge,locations,why,constraint}` + raw `findings[].matched_by.{cycle_modules,cycle_pairs_total,locations_total}`                             | partly                                   | project the cycle identity and counts (`decode.go:239` drops `matched_by`); one rule can have several cycles                                                           |
| Advisory edge count (33)           | `findings[].matched_by.group_count`                                                                                                                                                      | raw                                      | project `matched_by.group_count`                                                                                                                                       |
| Suggested action                   | `findings[].matched_by.hypothesis`, `seams[].hypothesis`                                                                                                                                 | raw                                      | project `hypothesis`                                                                                                                                                   |
| New vs baseline                    | `findings[].status`, `gate_reference.{baseline_present,status,reasons}`                                                                                                                  | yes                                      | —                                                                                                                                                                      |
| Introduced vs pre-existing         | `comparison.{status,origin_status,origin_reasons}`, `findings[].origin` (including `unknown`)                                                                                            | fields yes; `not_requested` in the pilot | compare against an explicit PR reference; render the supplied origin, never infer it                                                                                   |
| "Result predates baseline #5"      | the selected run's claimed `baseline_digest` vs SHA-256 of the baseline bytes on `master` (not the Git blob SHA); merge time of #5 vs run time                                           | no                                       | bind the page to one run; compute the digest; name the PR that changed it                                                                                              |
| Agent tasks                        | `agent_tasks[].{finding_id,rule_id,repair_kind,goal,constraints,files,validation,origin}`                                                                                                | yes                                      | copy button with the exact array                                                                                                                                       |
| PR page                            | `GET /pulls/{n}`, check runs for the head, the run's report artifact                                                                                                                     | no                                       | route `/r/{o}/{n}/pull/{number}`; lookup by head and run                                                                                                               |
| Open PRs with latest result        | `GET /pulls`, check runs per head                                                                                                                                                        | no                                       | one check-runs read per open PR (rate limit)                                                                                                                           |
| Map                                | `seams[].{from_module,to_module,edges,strength,distance,volatility,severity,policy}`                                                                                                     | yes                                      | PR-head evidence on the graph                                                                                                                                          |
| Critical edges per seam, owners    | `seams[].{critical_edges,raw_distance.from_owner,raw_distance.to_owner}`                                                                                                                 | raw                                      | project them                                                                                                                                                           |
| Dimensions                         | `dimensions.*.{status,confidence,gate,coverage,unknown[]}`                                                                                                                               | yes                                      | —                                                                                                                                                                      |
| Unknown facts grouped by next step | `dimensions.*.unknown[].{fact,reason,owner}`                                                                                                                                             | yes                                      | App wording table per `fact`                                                                                                                                           |
| Analyzers                          | `coverage.tools[].{tool,status,reason}`                                                                                                                                                  | yes                                      | —                                                                                                                                                                      |
| Identity                           | `comparison.*_hash` (retained); envelope digests, engine version, image, platform, run id (claimed)                                                                                      | partly                                   | resolve run and producer identity per page; the graph page has no envelope today (`graphpage.go:54`)                                                                   |
| Decisions                          | `dimensions.*.unknown[]`, raw `findings[].matched_by.hypothesis` / `seams[].hypothesis`, `agent_tasks[].repair_kind = needs_owner_decision` when emitted, protected settings, GitHub PRs | partly                                   | fact → decision rules (App guidance)                                                                                                                                   |
| Setup steps                        | current onboarding stage on `/r/{o}/{n}`; merged `archfit/*` PRs                                                                                                                         | partly                                   | baseline and rehearsal stages are not in the onboarding contract (`onboarding/contract.go:46`); PR title/author/merged_at are not in `api.Pull` (`api/contract.go:66`) |

## Consistency glossary

| Concept                                         | Word                                                  | Badge                       | Never                                   |
| ----------------------------------------------- | ----------------------------------------------------- | --------------------------- | --------------------------------------- |
| `kind=gate`, `status` new or expired_waiver     | rule violation (active)                               | red `violation`             | "blocking" in advisory mode             |
| `kind=gate`, `status` baseline / waived / fixed | accepted / waived / fixed                             | grey label with that word   | red, or counted as a violation          |
| `kind=advisory`                                 | advisory; "coupling advisory" only for coupling rules | yellow count; "never block" | "finding that blocks", "critical issue" |
| Advisory size                                   | "6 advisories · 33 edges"                             | —                           | "33 findings" next to "6 findings"      |
| `status=new`                                    | new (not in the baseline)                             | grey `new`                  | "introduced", "added by this PR"        |
| `status=baseline`                               | baseline                                              | grey `baseline`             | "fixed", "ignored"                      |
| `origin=introduced`                             | introduced by this PR                                 | only when `comparison` ran  | inferred from a `master` snapshot       |
| Gate mode                                       | advisory / required                                   | grey label                  | implied by verdict                      |
| Check `neutral`                                 | does not block merging                                | grey neutral icon           | "passed", "blocked"                     |
| `action_required`                               | Action required: <reason>                             | yellow alert                | neutral, or a verdict on the code       |
| Old result                                      | result predates <change>                              | yellow sync icon            | silent                                  |
| Dimension coverage                              | observed / total + basis                              | thin bar per row            | one percentage across dimensions        |
| Trust                                           | customer-attested                                     | shield icon, muted          | "verified"                              |
| Owner choice                                    | owner decision                                        | purple `owner decision`     | for code repairs                        |
| Repair                                          | `code_change` task                                    | in the agent block          | owner queue                             |

## What Codex challenged and how it was resolved

| #   | Challenge (Codex)                                                                                           | Evidence                                                    | Resolution                                                                                                                                             |
| --- | ----------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------ |
| 1   | Old prototype: two green checks, "healthy", invented owner task, "new" shown as "introduced"                | pilot reports, check run 117634137811                       | prototype rebuilt on pilot data                                                                                                                        |
| 2   | Headline must come from gate mode + check conclusion; gate mode is not in the report                        | `publish.go:68,105`, `.archfit-app.yaml`                    | headline matrix adopted                                                                                                                                |
| 3   | "16 evidence gaps" is not a canonical count: 15 unknown facts + missing baseline; absent tools are separate | report `dimensions.*.unknown[]`, `coverage.tools[]`         | Evidence groups facts; tools shown apart                                                                                                               |
| 4   | My "stale → action required" scenario is wrong in advisory mode                                             | `sweep/rules.go:21`; live check still neutral after #5      | scenario renamed "result outdated", neutral; required-mode stale moved to States; open question raised                                                 |
| 5   | Volatility is a `.archfit.yaml` module field, not a labels PR; scores may not drop                          | `archfit.schema.json:382`, `labels.go:75`, engine CLAUDE.md | decision text fixed; opens the policy editor                                                                                                           |
| 6   | Evidence grouping overclaimed what closes each gap                                                          | `acquisition/coverage.go:200`, `operations` metrics         | regrouped as "possible next step" with conditions                                                                                                      |
| 7   | Agent preview was labeled verbatim but truncated and invented "… 39 more"                                   | `agent_tasks[]`                                             | preview labeled; prototype copy uses the exact embedded array; GitHub block marked preview                                                             |
| 8   | `matched_by` and seam hypothesis/critical_edges/owners are not in the App projection                        | `wire/report/contract.go:22`, `Seam` struct                 | marked raw/NEW in the prototype and data table                                                                                                         |
| 9   | Overview's dimensions button opened PR #4 evidence                                                          | navigation                                                  | Evidence has a `master` / PR #4 switch; links set it; every coverage value follows the source (165/624 vs 166/625, 77 vs 78 edges, 156/171 vs 157/172) |

Agreed without dispute: one comment updated in place; the agent block stays in the comment, collapsed, with its
markers; no aggregate score; customer-attested wording.

## Recommendations

### Now, for the pilot

1. **Fix the headline and say "no baseline"** in `gate/feedback` per the matrix, together. No "blocking" in advisory
   mode. Small change in `render.go`; it removes a false statement on every PR.
2. **Shorten the visible feedback**: headline, effect, grouped violations, counts, next step. Keep dimensions, gaps
   and identity in collapsed sections until the App PR and Evidence pages exist to link to. Keep the agent block exact.
3. **Decide advisory refresh** (open question 1). Today the pilot shows an out-of-date result with no signal.
4. **Add the PR comparison** against an explicit base so that `origin` exists and "introduced by this PR" can be said
   when the engine says it.

### Next

5. App PR page and Evidence page, bound to one run, with report lookup by head and run.
6. Extend the report projection: `matched_by.{group_count,hypothesis,cycle_modules,locations_total}`,
   `seams[].{critical_edges,hypothesis}`, module owners from both ends.
7. Then remove the duplicated detail from the comment and check summary, and link to the App instead.
8. Repository overview with open-PR results; States wording in templates.

### Later

9. Decisions derived from report facts; ruleset read for "merge is blocked"; rehearsal tracking.
10. From the earlier prototype, when data exists: organization overview, "blocked then repaired" loops, agent
    attribution, waiver expiry timeline, agent policy edits held for an owner.

## Owner decisions (18 October 2026)

| Question                                               | Decision                                                                          |
| ------------------------------------------------------ | --------------------------------------------------------------------------------- |
| Advisory mode: open PRs after a baseline/policy change | Re-run them and publish the new result, as in required mode                       |
| PR comparison against the base branch                  | On by default in the workflow template, so the comment can say "added by this PR" |
| First owner action in the pilot                        | Gate first: rehearse, then require. Volatility later                              |
| Phase 1 (headline, short comment)                      | Start on a separate branch from `main`; PR only after the owner's OK              |
| Ruleset read for "merge is blocked"                    | Not now (default). Say "gate failing"                                             |
