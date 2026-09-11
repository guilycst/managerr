# Working on Mastarr

## Current authorization

The deliverable is a detailed specification and implementation plan for v0.0.1.
Do not start implementation or operate a live media stack from a task listing.
When the user authorizes implementation, record that instruction and advance
`docs/execution/state.json` before dispatching work.

This repository is public and MIT licensed. Use synthetic media and fixtures.
Never commit credentials, cookies, tracker URLs, passkeys, real inventories,
private hostnames, or user-specific runtime paths.

## Communication and environment

Use the available caveman skill at full intensity, with its clarity exceptions.
Apply unslop to writing. Use managing-go-environment when working on Go environment
configuration, including its documentation. Prefer caarlos0/env/v11 and envdoc.

## Read before editing

1. `docs/execution/RESUME.md` and `docs/execution/state.json`.
2. `docs/plans/implementation.md` and the assigned task in `docs/execution/tasks.json`.
3. Linked specifications and acceptance cases.
4. Git status, HEAD, branch/worktree ownership, remotes, and active agents.

Use rg for discovery. Preserve unrelated changes. Never reset, rebase, clean,
remove a worktree, or force-push to resolve ownership uncertainty.

## Architecture boundaries

- Root module owns API, domain, SQLite, workers, upstream and filesystem adapters.
- Nested ui module consumes HTTP only. No root internals, DB, direct upstream calls,
  media mounts, or safety decisions in the BFF.
- Nested tools module owns oapi-codegen, sqlc, migrate, envdoc, and templ generators.
- C-01 creates authoritative api/openapi.yaml. Until then the HTTP spec is proposed.
- Generate strict oapi-codegen server bindings using net/http and a UI HTTP client
  from the same contract. Verify modules with GOWORK=off, without local replace.
- Use supported upstream APIs. Never edit upstream databases. Direct filesystem
  operations are allowed only through the approved, root-constrained action port.

## Product invariants

- Discovery is read-only. Missing evidence is unknown, not untracked.
- Unknown Arr titles need registration approval and later exact import approval.
- Approval binds immutable plan revision and scope. v0.0.1 has no application
  authentication; actor attribution is explicitly unauthenticated.
- Accepted command, imported file, Jellyfin availability, and Seerr status differ.
- Already-materialized desired state succeeds without another write.
- Lost write responses require read-only reconciliation before any mutation retry.
- Copy/hardlink never silently becomes move or another transfer mode.
- Trash defaults to 30 days. UI purges only from trash; API supports explicit direct
  hard delete. qBittorrent stays stopped while associated payload is trashed;
  permanent deletion removes the torrent record without deleting unselected files.
- No Seerr writes, automatic approval, release searching, indexer management, or
  broad automatic repair of other managers in v0.0.1.

## Agent lanes and durable progress

When implementation is authorized, use bounded subagents from the plan. Start
with two implementation workers. A third requires recorded independent ownership
and review capacity. Reserve coordinator and independent reviewer capacity.
Use the available reviewer role for a reviewer who did not author the batch.
Internal delegation does not create user-visible Codex tasks.

Before dispatch record task ID, owner, base SHA, owned files, branch/worktree or
shared checkout ownership, expected checks, and handoff path. Never give two
writers the same file. Coordinator owns shared contracts, task definitions,
execution state, integration, and publication. Freeze contracts before consumers.

Workers change only assigned files and their own handoff. They are not alone in
the repository and must accommodate others' changes. Checkpoint every completed
task, dependency change, blocker, integration, and impending context boundary.
Record exact commits and command outcomes; narrative claims are insufficient.

After compaction, reconcile disk state with live Git and agents. Unknown ownership
means stop conflicting writes, not delete files. Keep private runtime coordinates
outside public handoffs. Use the handoff template in docs/execution/handoffs.

## Definition of done

Run assigned acceptance cases, relevant module checks, and clean regeneration.
UI work includes browser behavior, accessibility, initial metadata and assets.
Independent review checks approval binding, uncertain writes, cancellation,
restart behavior, data loss, and contract drift. Unresolved findings remain open.
Local checks, CI, release publication, deployment, and verified live behavior are
separate gates. Do not tag, release, deploy, or mutate real media from local success.
