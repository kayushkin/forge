# Forge TODO

## Deploy History Grouping
Currently deploys are tracked per-commit per-target. But a single logical change often spans
multiple repos (e.g. bus-agent API + kayushkin frontend + forge schema). Need to:
- Group related commits across projects into a single "changeset" or "release"
- Show changesets in deploy history instead of individual commits
- Allow deploying/reverting an entire changeset atomically
- Could use a `changesets` table linking multiple deploy records, or tag deploys with a changeset ID
