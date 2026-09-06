// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

package apikeyrestrictions

import (
	"fmt"
	"strings"
	"xprem/internal/branch"
	"xprem/internal/validation"
)

// maxUpdateRules bounds the number of rules a single API key may hold.
const maxUpdateRules = 50

// UpdateAction is what an Updates CLI request does to the branch it names.
type UpdateAction string

const (
	UpdateActionRead     UpdateAction = "read"
	UpdateActionPublish  UpdateAction = "publish"
	UpdateActionRollback UpdateAction = "rollback"
)

// AllUpdateActions is the catalog, in increasing order of trust.
var AllUpdateActions = []UpdateAction{UpdateActionRead, UpdateActionPublish, UpdateActionRollback}

func IsValidUpdateAction(action string) bool {
	for _, known := range AllUpdateActions {
		if string(known) == action {
			return true
		}
	}
	return false
}

// Implies reports whether granting a covers a request for b. UpdateActionPublish and
// UpdateActionRollback both imply UpdateActionRead, but UpdateActionPublish does not imply
// UpdateActionRollback.
func (a UpdateAction) Implies(b UpdateAction) bool {
	if a == b {
		return true
	}
	return b == UpdateActionRead && (a == UpdateActionPublish || a == UpdateActionRollback)
}

// UpdateRule grants a set of actions on every branch matching Pattern, where
// "*" stands for any run of characters. An API key holding no rules has
// no Updates access.
type UpdateRule struct {
	Pattern string         `json:"pattern"`
	Actions []UpdateAction `json:"actions"`
}

func (rule UpdateRule) Allows(branchName string, action UpdateAction) bool {
	if !matchBranchPattern(rule.Pattern, branchName) {
		return false
	}
	for _, granted := range rule.Actions {
		if granted.Implies(action) {
			return true
		}
	}
	return false
}

// AllowsUpdates requires an explicit grant and a nonempty Updates branch.
func AllowsUpdates(rules []UpdateRule, branchName string, action UpdateAction) bool {
	if branchName == "" {
		return false
	}
	for _, rule := range rules {
		if rule.Allows(branchName, action) {
			return true
		}
	}
	return false
}

// NormalizeUpdateRules validates the given rules and returns the form to
// persist, with actions deduplicated and reordered into catalog order.
func NormalizeUpdateRules(rules []UpdateRule) ([]UpdateRule, error) {
	if len(rules) > maxUpdateRules {
		return nil, validation.Errorf("rules", "a key cannot hold more than %d access rules", maxUpdateRules)
	}
	normalized := make([]UpdateRule, 0, len(rules))
	seen := make(map[string]struct{}, len(rules))
	for _, rule := range rules {
		if err := validation.NamePattern("pattern", rule.Pattern); err != nil {
			return nil, err
		}
		// "*" and "**" mean the same set of branches, so patterns are collapsed
		// before the duplicate check below.
		pattern := collapseWildcards(rule.Pattern)
		if _, duplicate := seen[pattern]; duplicate {
			return nil, validation.Errorf("pattern", "%q appears in more than one rule; merge them into one", pattern)
		}
		seen[pattern] = struct{}{}
		actions, err := normalizeUpdateActions(pattern, rule.Actions)
		if err != nil {
			return nil, err
		}
		normalized = append(normalized, UpdateRule{Pattern: pattern, Actions: actions})
	}
	return normalized, nil
}

// collapseWildcards rewrites any run of "*" as a single one.
func collapseWildcards(pattern string) string {
	return branch.CollapseWildcards(pattern)
}

func normalizeUpdateActions(pattern string, actions []UpdateAction) ([]UpdateAction, error) {
	granted := make(map[UpdateAction]struct{}, len(actions))
	for _, action := range actions {
		if !IsValidUpdateAction(string(action)) {
			return nil, validation.Errorf("actions", "unknown action %q on rule %q", string(action), pattern)
		}
		granted[action] = struct{}{}
	}
	if len(granted) == 0 {
		return nil, validation.Errorf("actions", "rule %q grants no action; delete the rule instead", pattern)
	}
	ordered := make([]UpdateAction, 0, len(granted))
	for _, action := range AllUpdateActions {
		if _, ok := granted[action]; ok {
			ordered = append(ordered, action)
		}
	}
	return ordered, nil
}

// matchBranchPattern matches name against pattern, "*" standing for any run of
// characters, including empty.
func matchBranchPattern(pattern, name string) bool {
	return branch.MatchPattern(pattern, name)
}

// describeUpdateRules renders a rule list for the audit trail.
func describeUpdateRules(rules []UpdateRule) []string {
	described := make([]string, 0, len(rules))
	for _, rule := range rules {
		actions := make([]string, 0, len(rule.Actions))
		for _, action := range rule.Actions {
			actions = append(actions, string(action))
		}
		described = append(described, fmt.Sprintf("%s:%s", rule.Pattern, strings.Join(actions, "+")))
	}
	return described
}
