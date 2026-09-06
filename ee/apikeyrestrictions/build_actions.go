// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

package apikeyrestrictions

import "xprem/internal/validation"

// BuildAction belongs to Build only; it never grants an Updates action.
// Build CLI routes will consume these permissions when they are introduced.
type BuildAction string

const (
	BuildActionRead   BuildAction = "read"
	BuildActionCreate BuildAction = "create"
	BuildActionCancel BuildAction = "cancel"
)

// normalizeBuildActions validates explicit grants, preserving an empty list
// as no access. Implied read access is not stored as an explicit grant.
func normalizeBuildActions(actions []BuildAction) ([]BuildAction, error) {
	granted := make(map[BuildAction]bool, len(actions))
	for _, action := range actions {
		switch action {
		case BuildActionRead, BuildActionCreate, BuildActionCancel:
			granted[action] = true
		default:
			return nil, validation.Errorf("build.actions", "unknown build action %q", action)
		}
	}
	normalized := make([]BuildAction, 0, len(granted))
	for _, action := range []BuildAction{BuildActionRead, BuildActionCreate, BuildActionCancel} {
		if granted[action] {
			normalized = append(normalized, action)
		}
	}
	return normalized, nil
}
