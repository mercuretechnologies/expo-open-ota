// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

package apikeyrestrictions

import (
	"slices"

	"xprem/internal/validation"
)

// SubmitAction never inherits permissions from Build or another destination.
type SubmitAction string

const SubmitActionUpload SubmitAction = "upload"

// SubmitDestination identifies the store destination, not an OTA channel or build profile.
type SubmitDestination string

const (
	SubmitDestinationInternal   SubmitDestination = "internal"
	SubmitDestinationAlpha      SubmitDestination = "alpha"
	SubmitDestinationBeta       SubmitDestination = "beta"
	SubmitDestinationProduction SubmitDestination = "production"
	SubmitDestinationTestFlight SubmitDestination = "testflight"
)

// SubmitRule grants upload for one identifier and store destination.
type SubmitRule struct {
	AppIdentifierID string            `json:"appIdentifierId"`
	Destination     SubmitDestination `json:"destination"`
	Actions         []SubmitAction    `json:"actions"`
}

func (d SubmitDestination) platform() string {
	switch d {
	case SubmitDestinationInternal, SubmitDestinationAlpha, SubmitDestinationBeta, SubmitDestinationProduction:
		return "android"
	case SubmitDestinationTestFlight:
		return "ios"
	default:
		return ""
	}
}

// Allows requires the exact identifier, destination and upload action.
func (r SubmitRule) Allows(identifierID string, destination SubmitDestination, action SubmitAction) bool {
	return identifierID != "" && r.AppIdentifierID == identifierID && r.Destination == destination && destination.platform() != "" && action == SubmitActionUpload && slices.Contains(r.Actions, action)
}

func normalizeSubmitRules(rules []SubmitRule) ([]SubmitRule, error) {
	if len(rules) > 50 {
		return nil, validation.Errorf("submit.rules", "at most 50 rules are allowed")
	}
	result := make([]SubmitRule, 0, len(rules))
	seen := map[string]bool{}
	for _, rule := range rules {
		id, err := normalizeIdentifierID(rule.AppIdentifierID)
		if err != nil {
			return nil, err
		}
		if rule.Destination.platform() == "" {
			return nil, validation.Errorf("submit.destination", "unknown store destination")
		}
		key := id + ":" + string(rule.Destination)
		if seen[key] {
			return nil, validation.Errorf("submit.rules", "duplicate app identifier and destination")
		}
		seen[key] = true
		if len(rule.Actions) == 0 {
			return nil, validation.Errorf("submit.rules", "a rule must grant at least one action")
		}
		for _, action := range rule.Actions {
			if action != SubmitActionUpload {
				return nil, validation.Errorf("submit.actions", "action %q is not valid for %q", action, rule.Destination)
			}
		}
		actions := make([]SubmitAction, 0, len(rule.Actions))
		for _, action := range []SubmitAction{SubmitActionUpload} {
			if slices.Contains(rule.Actions, action) {
				actions = append(actions, action)
			}
		}
		result = append(result, SubmitRule{AppIdentifierID: id, Destination: rule.Destination, Actions: actions})
	}
	return result, nil
}
