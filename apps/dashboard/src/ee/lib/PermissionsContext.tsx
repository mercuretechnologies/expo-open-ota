// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

import { createContext, useContext, useMemo, ReactNode } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { useCurrentUser } from '@/lib/CurrentUserContext';
import { useSelectedApp } from '@/lib/SelectedAppContext';

// Mirrors the server catalog (ee/rbac/permissions.go). An unknown string
// would simply never match, so drift fails closed.
export type Permission =
  | 'app:delete'
  | 'app:rename'
  | 'certificate:read'
  | 'branch:create'
  | 'branch:delete'
  | 'branch:protect'
  | 'channel:create'
  | 'channel:delete'
  | 'channel:edit-branch'
  | 'channel-rollout:manage'
  | 'update-rollout:manage'
  | 'apikeys:manage'
  | 'identity:manage'

type PermissionsContextValue = {
  // enabled reports whether fine-grained roles are enforced right now
  // (control plane + valid enterprise license).
  enabled: boolean;
  isAdmin: boolean;
  // can answers "may the current account do this on this app". Admins can do
  // everything; without enforcement members can do nothing (the community
  // read-only rule); enforced members follow their grants. Display gating
  // only: the server re-checks every mutation.
  can: (appId: string | null | undefined, permission: Permission) => boolean;
};

const PermissionsContext = createContext<PermissionsContextValue | null>(null);

export function PermissionsProvider({ children }: { children: ReactNode }) {
  // isAdmin from /api/me keeps the UI correct while the permission map is
  // still loading (an admin never flickers, a member starts read-only and
  // gains buttons when their grants arrive).
  const { isAdmin: sessionIsAdmin } = useCurrentUser();
  const { data } = useQuery({
    queryKey: ['me', 'permissions'],
    queryFn: () => api.getMyPermissions(),
  });

  const value = useMemo<PermissionsContextValue>(() => {
    const isAdmin = data ? data.isAdmin : sessionIsAdmin;
    const enabled = data?.enabled ?? false;
    const apps = data?.apps ?? null;
    return {
      enabled,
      isAdmin,
      can: (appId, permission) => {
        if (isAdmin) {
          return true;
        }
        if (!enabled || !appId) {
          return false;
        }
        return apps?.[appId]?.includes(permission) ?? false;
      },
    };
  }, [data, sessionIsAdmin]);

  return <PermissionsContext.Provider value={value}>{children}</PermissionsContext.Provider>;
}

export function usePermissions(): PermissionsContextValue {
  const context = useContext(PermissionsContext);
  if (!context) {
    throw new Error('usePermissions must be used within a PermissionsProvider');
  }
  return context;
}

// useAppPermission is the one-liner for the common case: "may I do this on
// the app currently selected in the dashboard".
export function useAppPermission(permission: Permission): boolean {
  const { selectedAppId } = useSelectedApp();
  const { can } = usePermissions();
  return can(selectedAppId, permission);
}
