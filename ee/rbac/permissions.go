// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

package rbac

// Permission is one dashboard action a member can be granted on an app.
// Admins never need one: the admin flag bypasses the whole permission model.
// The strings are stored as-is in roles.permissions and
// user_app_grants.extra_permissions, so renaming one is a data migration.
type Permission string

const (
	PermAppDelete       Permission = "app:delete"
	PermAppRename       Permission = "app:rename"
	PermCertificateRead Permission = "certificate:read"
	PermBranchCreate    Permission = "branch:create"
	PermBranchDelete    Permission = "branch:delete"
	// PermBranchProtect toggles branch protection on and off. Deliberately
	// separate from PermBranchDelete: protecting production is routine,
	// deleting branches is not.
	PermBranchProtect     Permission = "branch:protect"
	PermChannelCreate     Permission = "channel:create"
	PermChannelDelete     Permission = "channel:delete"
	PermChannelEditBranch Permission = "channel:edit-branch"
	// PermChannelRolloutManage covers the whole lifecycle of a channel
	// rollout (start, adjust the percentage, promote or revert): being able
	// to move a rollout forward and being able to back it out are the same
	// level of trust.
	PermChannelRolloutManage Permission = "channel-rollout:manage"
	// PermUpdateRolloutManage is the per-update sibling: set the rollout
	// percentage of a single update or revert it.
	PermUpdateRolloutManage Permission = "update-rollout:manage"
	// PermApiKeysManage mints and revokes the app's publishing tokens and
	// edits their enterprise restrictions (IP allowlists, protected-branch
	// access).
	PermApiKeysManage Permission = "apikeys:manage"
	// PermIdentityManage edits the device-identity metadata allowlist (the
	// dashboard "Identity" section): which metadata keys are accepted and
	// their types. Reading identity and browsing devices stays open to any app
	// viewer; only shaping the allowlist needs this.
	PermIdentityManage Permission = "identity:manage"
)

// AllPermissions is the catalog, in the order the dashboard displays it.
var AllPermissions = []Permission{
	PermAppDelete,
	PermAppRename,
	PermCertificateRead,
	PermBranchCreate,
	PermBranchDelete,
	PermBranchProtect,
	PermChannelCreate,
	PermChannelDelete,
	PermChannelEditBranch,
	PermChannelRolloutManage,
	PermUpdateRolloutManage,
	PermApiKeysManage,
	PermIdentityManage,
}

var permissionSet = func() map[Permission]struct{} {
	set := make(map[Permission]struct{}, len(AllPermissions))
	for _, p := range AllPermissions {
		set[p] = struct{}{}
	}
	return set
}()

// IsValidPermission reports whether the string is part of the catalog. Every
// write path validates through it so an unknown string can never reach the
// database, where it would silently grant nothing.
func IsValidPermission(p string) bool {
	_, ok := permissionSet[Permission(p)]
	return ok
}
