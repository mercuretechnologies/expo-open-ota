// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

package apikeyrestrictions

import (
	"slices"

	"xprem/internal/validation"
)

// BuildAction belongs to Build only; it never grants an Updates action.
// Build CLI routes will consume these permissions when they are introduced.
type BuildAction string

const BuildActionCreate BuildAction = "create"

// BuildRule grants actions on one registered app identifier, regardless of profile.
type BuildRule struct {
	AppIdentifierID string        `json:"appIdentifierId"`
	Actions         []BuildAction `json:"actions"`
}

// Allows checks a resolved identifier ID; no wildcard or profile can widen the grant.
func (r BuildRule) Allows(identifierID string, action BuildAction) bool {
	return identifierID != "" && r.AppIdentifierID == identifierID && action == BuildActionCreate && slices.Contains(r.Actions, action)
}

func normalizeBuildRules(rules []BuildRule) ([]BuildRule, error) {
	if len(rules) > 50 {
		return nil, validation.Errorf("build.rules", "at most 50 rules are allowed")
	}
	result := make([]BuildRule, 0, len(rules))
	seen := map[string]bool{}
	for _, rule := range rules {
		id, err := normalizeIdentifierID(rule.AppIdentifierID)
		if err != nil {
			return nil, err
		}
		if seen[id] {
			return nil, validation.Errorf("build.rules", "duplicate app identifier")
		}
		seen[id] = true
		actions, err := normalizeBuildActions(rule.Actions)
		if err != nil {
			return nil, err
		}
		if len(actions) == 0 {
			return nil, validation.Errorf("build.rules", "a rule must grant at least one action")
		}
		result = append(result, BuildRule{AppIdentifierID: id, Actions: actions})
	}
	return result, nil
}

// normalizeBuildActions validates explicit grants, preserving an empty list
// as no access.
func normalizeBuildActions(actions []BuildAction) ([]BuildAction, error) {
	granted := make(map[BuildAction]bool, len(actions))
	for _, action := range actions {
		switch action {
		case BuildActionCreate:
			granted[action] = true
		default:
			return nil, validation.Errorf("build.actions", "unknown build action %q", action)
		}
	}
	normalized := make([]BuildAction, 0, len(granted))
	for _, action := range []BuildAction{BuildActionCreate} {
		if granted[action] {
			normalized = append(normalized, action)
		}
	}
	return normalized, nil
}
