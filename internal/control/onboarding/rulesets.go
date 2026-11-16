package onboarding

import (
	"context"
	"sync"

	"github.com/alexei-led/archfit-app/internal/github/api"
)

const rulesetReadConcurrency = 4

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
	done := api.StartStage(ctx, "rules")
	count := 0
	defer func() { done(count, 0) }()
	add := func(code, state, text string) {
		report.Findings = append(report.Findings, RulesetFinding{code, state, text})
	}
	var branch []api.RepositoryRule
	var app int64
	var sets []api.Ruleset
	var branchErr, appErr, setsErr error
	var reads sync.WaitGroup
	reads.Go(func() { branch, branchErr = s.gh.BranchPolicy(ctx, r.RepoRef, r.DefaultBranch) })
	reads.Go(func() { app, appErr = s.gh.AppIntegrationID(ctx) })
	reads.Go(func() { sets, setsErr = s.gh.ListRulesets(ctx, r.RepoRef) })
	reads.Wait()
	count = 3
	if branchErr != nil {
		add("branch_rules", "unknown", "The App cannot read the default-branch rules.")
		return report
	}
	if appErr != nil {
		add("integration", "unknown", "The App integration ID is not available.")
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
				add("approvals", "warning", "GitHub requires pull request approvals. These are separate from the Archfit owner approval.")
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
		add("pull_requests", "warning", "Add a pull request rule to the default branch.")
	}
	if check {
		add("gate_check", "ok", "The App's archfit/gate check is required.")
	} else {
		add("gate_check", "warning", "Add the App's archfit/gate check as a required status check.")
	}
	if setsErr != nil {
		add("rulesets", "unknown", "The App cannot read rulesets or administrator bypass.")
		return report
	}
	pushSupported := r.OwnerType == "Organization" && (r.Visibility == "private" || r.Visibility == "internal")
	push, adminUnknown, unreadable := false, false, false
	adminAll, branchSets := true, 0
	// Keep response order and unknown findings, independent of completion order.
	details := make([]api.Ruleset, len(sets))
	readErrors := make([]error, len(sets))
	for start := 0; start < len(sets); start += rulesetReadConcurrency {
		for i := start; i < min(start+rulesetReadConcurrency, len(sets)); i++ {
			reads.Go(func() { details[i], readErrors[i] = s.gh.GetRuleset(ctx, r.RepoRef, sets[i].ID) })
		}
		reads.Wait()
	}
	count += len(sets)
	for i, rs := range details {
		if readErrors[i] != nil {
			unreadable = true
			continue
		}
		if rs.Enforcement != "active" || !relevant[rs.ID] && (rs.Target != "push" || !pushSupported) {
			continue
		}
		if relevant[rs.ID] && rs.Target == "branch" {
			branchSets++
			if rs.CanBypass == "always" || rs.CanBypass == "pull_requests_only" {
				add("app_bypass", "warning", "The App can bypass the branch ruleset "+rs.Name+"; remove its bypass to keep GitHub enforcement effective.")
			}
			if rs.CanBypass == "" {
				add("app_bypass", "unknown", "The App's bypass access for the branch ruleset "+rs.Name+" cannot be read.")
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
					add("policy_push_bypass", "unknown", "The App's bypass ability for policy push ruleset "+rs.Name+" cannot be read.")
				default:
					add("policy_push_bypass", "warning", "Allow the App to always bypass policy push ruleset "+rs.Name+" so it can write approved policy proposals.")
				}
			}
		}
	}
	switch {
	case unreadable || adminUnknown:
		add("administrator_bypass", "unknown", "The App cannot read administrator bypass without Administration permission. An admin must confirm the bypass list is intentional.")
	case branchSets > 0 && adminAll:
		add("administrator_bypass", "ok", "Every applicable branch ruleset lists an administrator bypass for emergencies.")
	default:
		add("administrator_bypass", "warning", "No administrator bypass covers every applicable branch ruleset.")
	}
	switch {
	case !pushSupported:
		add("policy_push", "not_applicable", "Policy-push restriction is available only for organization-owned private or internal repositories.")
	case unreadable:
		add("policy_push", "unknown", "The .archfit.yaml push ruleset cannot be read.")
	case push:
		add("policy_push", "ok", "An active push ruleset restricts .archfit.yaml.")
	default:
		add("policy_push", "warning", "Add an active push ruleset for .archfit.yaml. Keep administrator bypass for emergencies.")
	}
	return report
}
