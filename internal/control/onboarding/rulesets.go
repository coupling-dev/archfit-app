package onboarding

import (
	"context"
	"strconv"

	"github.com/alexei-led/archfit-app/internal/github/api"
)

// RulesetFinding is read-back or advice, never an automatic settings change.
type RulesetFinding struct {
	Code  string
	State string // ok, warning, unknown, not_applicable
	Text  string
}

// RulesetReport reports GitHub enforcement separately from repository admission.
type RulesetReport struct {
	Findings []RulesetFinding
}

func (s *setups) rules(ctx context.Context, r api.Repo) RulesetReport {
	report := RulesetReport{Findings: []RulesetFinding{}}
	add := func(code, state, text string) {
		report.Findings = append(report.Findings, RulesetFinding{code, state, text})
	}
	branch, err := s.gh.BranchPolicy(ctx, r.RepoRef, r.DefaultBranch)
	if err != nil {
		add("branch_rules", "unknown", "Default-branch rules are unknown/unreadable.")
		return report
	}
	app, err := s.gh.AppIntegrationID(ctx)
	if err != nil {
		add("integration", "unknown", "The App integration_id is unknown/unreadable.")
		return report
	}
	pulls, check := false, false
	relevant := map[int64]bool{}
	for _, rule := range branch {
		relevant[rule.RulesetID] = true
		switch rule.Type {
		case "pull_request":
			pulls = true
			if rule.Parameters.Approvals > 0 {
				add("approvals", "warning", "Required approvals are greater than zero; GitHub reviews are separate from Archfit owner approval.")
			}
		case "required_status_checks":
			for _, c := range rule.Parameters.Checks {
				if c.Context == gateCheckName && c.IntegrationID != nil && *c.IntegrationID == app && app > 0 {
					check = true
				}
			}
		}
	}
	if pulls {
		add("pull_requests", "ok", "Default-branch changes require a pull request.")
	} else {
		add("pull_requests", "warning", "Require a pull request before merging into the default branch.")
	}
	if check {
		add("gate_check", "ok", "archfit/gate is required with this App's integration_id.")
	} else {
		add("gate_check", "warning", "Require archfit/gate with this App's integration_id ("+strconv.FormatInt(app, 10)+").")
	}
	sets, err := s.gh.ListRulesets(ctx, r.RepoRef)
	if err != nil {
		add("rulesets", "unknown", "Rulesets and administrator bypass are unknown/unreadable.")
		return report
	}
	pushSupported := r.OwnerType == "Organization" && (r.Visibility == "private" || r.Visibility == "internal")
	push, adminUnknown, unreadable := false, false, false
	adminAll, branchSets := true, 0
	for _, summary := range sets {
		rs, err := s.gh.GetRuleset(ctx, r.RepoRef, summary.ID)
		if err != nil {
			unreadable = true
			continue
		}
		if rs.Enforcement != "active" || !relevant[rs.ID] && (rs.Target != "push" || !pushSupported) {
			continue
		}
		if relevant[rs.ID] && rs.Target == "branch" {
			branchSets++
			if rs.CanBypass == "always" || rs.CanBypass == "pull_requests_only" {
				add("app_bypass", "warning", "The App can bypass branch ruleset "+rs.Name+"; remove its bypass to keep GitHub enforcement effective.")
			}
			if rs.CanBypass == "" {
				add("app_bypass", "unknown", "The App's bypass ability for branch ruleset "+rs.Name+" is unknown/unreadable.")
			}
			admin := false
			if rs.BypassActors == nil {
				adminUnknown = true
			} else {
				for _, a := range *rs.BypassActors {
					if a.Mode == "always" && (a.Type == "OrganizationAdmin" || a.Type == "RepositoryRole" && a.ID == 5) {
						admin = true
					}
				}
			}
			adminAll = adminAll && admin
		}
		if rs.Target == "push" && pushSupported {
			restrictsPolicy := false
			for _, rule := range rs.Rules {
				if rule.Type != "file_path_restriction" {
					continue
				}
				for _, p := range rule.Parameters.Paths {
					if p == ".archfit.yaml" || p == "**" || p == "*" {
						restrictsPolicy = true
					}
				}
			}
			if restrictsPolicy {
				push = true
				switch rs.CanBypass {
				case "always":
					add("policy_push_bypass", "ok", "The App can bypass policy push ruleset "+rs.Name+" to write approved policy proposals.")
				case "":
					add("policy_push_bypass", "unknown", "The App's bypass ability for policy push ruleset "+rs.Name+" is unknown/unreadable.")
				default:
					add("policy_push_bypass", "warning", "Allow the App to always bypass policy push ruleset "+rs.Name+" so it can write approved policy proposals.")
				}
			}
		}
	}
	switch {
	case unreadable || adminUnknown:
		add("administrator_bypass", "unknown", "Administrator bypass is unknown/unreadable; the App has no Administration permission. An administrator bypass, if the ruleset lists one, is the emergency path. Ask an administrator to check it.")
	case branchSets > 0 && adminAll:
		add("administrator_bypass", "ok", "An administrator bypass is listed for every applicable branch ruleset as the emergency path.")
	default:
		add("administrator_bypass", "warning", "No administrator bypass covers every applicable branch ruleset. An administrator bypass, if the ruleset lists one, is the emergency path.")
	}
	switch {
	case !pushSupported:
		add("policy_push", "not_applicable", "A push ruleset for .archfit.yaml is available only for organization-owned private or internal repositories.")
	case unreadable:
		add("policy_push", "unknown", "The .archfit.yaml push ruleset is unknown/unreadable.")
	case push:
		add("policy_push", "ok", "An active push ruleset restricts .archfit.yaml.")
	default:
		add("policy_push", "warning", "Add an active push ruleset restricting .archfit.yaml; use the administrator bypass only as the emergency path.")
	}
	return report
}
