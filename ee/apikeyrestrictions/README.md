# Token permissions

Enterprise token policies separate Updates branch rules from application-wide
Build actions. Both domains default to no access; an empty IP allowlist allows
any source IP. MIT and stateless authorization remain unchanged.

The dashboard access API reads this shape and replaces the whole policy on PUT
(omit `apiKeyId` from the PUT body):

```json
{
  "apiKeyId": "42",
  "updates": {
    "branchRules": [
      { "pattern": "staging", "actions": ["publish"] }
    ]
  },
  "build": { "actions": ["create"] },
  "allowedIps": []
}
```

- Updates: `read`, `publish`, `rollback`. Publish and rollback imply read.
  Full access is an explicit `*` rule granting publish and rollback (read may
  also be listed). No rules means no Updates access.
- Build: `read`, `create`, `cancel`. Create and cancel imply Build read access;
  neither grants any Updates permission. Grants are stored explicitly, without
  expanding implied actions. There are no Build CLI routes yet: the catalog,
  validation, persistence and editor are ready; future routes must enforce
  these actions, their read implication and the common IP allowlist.
- Both domain objects and all three arrays are required on PUT, including empty
  arrays. Legacy, partial and unknown-field payloads are rejected to avoid
  silently clearing permissions from an outdated dashboard.
- Access changes, including Build actions, are recorded in the audit trail.
  Management requires an active Enterprise license. Without one, restrictions
  are not enforced, matching the existing behavior on license expiry.

## Migration and deployment

`20260906120000_api_key_build_access.sql` adds `api_keys.build_actions` with an
empty default. Existing tokens gain no Build permissions. Existing tokens with
no branch rules receive an explicit full-access Updates rule; existing scoped
rules are unchanged. Tokens created after migration have no grants by default.

Deploy the migration, backend and dashboard together, with old backend replicas
stopped before accepting writes under the new policy format. Old code interprets
an empty Updates policy as full access, so mixed-version serving is unsafe.
An already-open old dashboard must reload before editing token access.

The down migration refuses to run while a live token has no Updates rules:
the old interpretation would otherwise widen its permissions. Explicit Updates
rules remain in place when downgrading; Build grants are removed with the column.
