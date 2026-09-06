// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

package apikeyrestrictions

import (
	"slices"
	"xprem/internal/validation"

	"github.com/google/uuid"
)

// BuildRule grants actions on one registered app identifier, regardless of profile.
type BuildRule struct {
	AppIdentifierID string        `json:"appIdentifierId"`
	Actions         []BuildAction `json:"actions"`
}

// SubmitAction never inherits permissions from Build or another destination.
type SubmitAction string

const (
	SubmitActionRead    SubmitAction = "read"
	SubmitActionUpload  SubmitAction = "upload"
	SubmitActionReview  SubmitAction = "review"
	SubmitActionRelease SubmitAction = "release"
)

// SubmitDestination identifies the store destination, not an OTA channel or build profile.
type SubmitDestination string

const (
	SubmitDestinationInternal   SubmitDestination = "internal"
	SubmitDestinationAlpha      SubmitDestination = "alpha"
	SubmitDestinationBeta       SubmitDestination = "beta"
	SubmitDestinationProduction SubmitDestination = "production"
	SubmitDestinationTestFlight SubmitDestination = "testflight"
	SubmitDestinationAppStore   SubmitDestination = "app-store"
)

// SubmitRule separates upload, review and release for one identifier and destination.
// Upload alone never authorizes distributing a build, even to testers.
type SubmitRule struct {
	AppIdentifierID string            `json:"appIdentifierId"`
	Destination     SubmitDestination `json:"destination"`
	Actions         []SubmitAction    `json:"actions"`
}

func (d SubmitDestination) platform() string {
	switch d {
	case SubmitDestinationInternal, SubmitDestinationAlpha, SubmitDestinationBeta, SubmitDestinationProduction:
		return "android"
	case SubmitDestinationTestFlight, SubmitDestinationAppStore:
		return "ios"
	default:
		return ""
	}
}

func (d SubmitDestination) supports(action SubmitAction) bool {
	if d.platform() == "" {
		return false
	}
	switch action {
	case SubmitActionRead, SubmitActionRelease:
		return true
	case SubmitActionUpload:
		return d != SubmitDestinationAppStore
	case SubmitActionReview:
		return d.platform() == "ios"
	default:
		return false
	}
}

func normalizeIdentifierID(value string) (string, error) {
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil {
		return "", validation.Errorf("appIdentifierId", "a registered app identifier ID is required")
	}
	return id.String(), nil
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
			if !rule.Destination.supports(action) {
				return nil, validation.Errorf("submit.actions", "action %q is not valid for %q", action, rule.Destination)
			}
		}
		actions := make([]SubmitAction, 0, len(rule.Actions))
		for _, action := range []SubmitAction{SubmitActionRead, SubmitActionUpload, SubmitActionReview, SubmitActionRelease} {
			if slices.Contains(rule.Actions, action) {
				actions = append(actions, action)
			}
		}
		result = append(result, SubmitRule{AppIdentifierID: id, Destination: rule.Destination, Actions: actions})
	}
	return result, nil
}

// Allows checks a resolved identifier ID; no wildcard or profile can widen the grant.
func (r BuildRule) Allows(identifierID string, action BuildAction) bool {
	if identifierID == "" || r.AppIdentifierID != identifierID {
		return false
	}
	switch action {
	case BuildActionRead:
		return slices.Contains(r.Actions, BuildActionRead) || slices.Contains(r.Actions, BuildActionCreate) || slices.Contains(r.Actions, BuildActionCancel)
	case BuildActionCreate, BuildActionCancel:
		return slices.Contains(r.Actions, action)
	default:
		return false
	}
}

// Allows requires the exact destination and action; write actions imply only read.
func (r SubmitRule) Allows(identifierID string, destination SubmitDestination, action SubmitAction) bool {
	if identifierID == "" || r.AppIdentifierID != identifierID || r.Destination != destination || !destination.supports(action) {
		return false
	}
	for _, granted := range r.Actions {
		if destination.supports(granted) && (granted == action || action == SubmitActionRead) {
			return true
		}
	}
	return false
}
