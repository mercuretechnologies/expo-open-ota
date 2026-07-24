// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

import { Permission } from '@/ee/lib/PermissionsContext';

// Display metadata for the permission catalog, grouped the way the role and
// grant editors lay their toggles out. The values mirror ee/rbac/permissions.go.
export type PermissionOption = {
  value: Permission;
  label: string;
  description: string;
};

export type PermissionGroup = {
  label: string;
  permissions: PermissionOption[];
};

export const PERMISSION_GROUPS: PermissionGroup[] = [
  {
    label: 'App',
    permissions: [
      {
        value: 'app:rename',
        label: 'Rename the app',
        description: 'Change the display name of the app.',
      },
      {
        value: 'app:delete',
        label: 'Delete the app',
        description: 'Remove the app with all of its branches, channels and updates.',
      },
      {
        value: 'certificate:read',
        label: 'Download the signing certificate',
        description: 'Key material used to verify update signatures.',
      },
    ],
  },
  {
    label: 'Branches',
    permissions: [
      {
        value: 'branch:create',
        label: 'Create branches',
        description: 'Add new update branches to the app.',
      },
      {
        value: 'branch:delete',
        label: 'Delete branches',
        description: 'Protected branches always refuse deletion.',
      },
      {
        value: 'branch:protect',
        label: 'Protect branches',
        description: 'Toggle branch protection on and off.',
      },
    ],
  },
  {
    label: 'Channels',
    permissions: [
      {
        value: 'channel:create',
        label: 'Create channels',
        description: 'Add new release channels to the app.',
      },
      {
        value: 'channel:delete',
        label: 'Delete channels',
        description: 'Builds configured with a deleted channel stop receiving updates.',
      },
      {
        value: 'channel:edit-branch',
        label: 'Change the channel branch',
        description: 'Point a release channel at a different branch.',
      },
    ],
  },
  {
    label: 'Rollouts',
    permissions: [
      {
        value: 'channel-rollout:manage',
        label: 'Manage channel rollouts',
        description: 'Start, adjust, promote or revert a progressive branch rollout.',
      },
      {
        value: 'update-rollout:manage',
        label: 'Manage update rollouts',
        description: 'Set or revert the rollout percentage of a single update.',
      },
    ],
  },
  {
    label: 'API tokens',
    permissions: [
      {
        value: 'apikeys:manage',
        label: 'Manage API tokens',
        description: 'Create and revoke publishing tokens and edit their restrictions.',
      },
    ],
  },
  {
    label: 'Identity',
    permissions: [
      {
        value: 'identity:manage',
        label: 'Manage the identity allowlist',
        description: 'Choose which device metadata keys are accepted and their types. Reading identity and browsing devices stays open to any member.',
      },
    ],
  },
];
