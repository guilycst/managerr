# Resume Managerr

Current deliverable: detailed specification and implementation lanes for v0.0.1.
Planning baseline is consolidated. No application implementation is authorized.
The last interview answer selected MIT. Do not restart the questionnaire.
Read state.json for publication verification and current execution phase.

## Recovery sequence

1. Read repository AGENTS.md and current user instructions.
2. Read state.json, tasks.json, implementation.md, and the assigned task's specs
   and acceptance cases. User interview records requirements, decisions separates
   selected defaults and open compatibility gates.
3. Inspect Git status, HEAD, remotes, branch/worktree ownership and running agents.
   Compare those observations to recorded task ownership. Preserve unknown edits.
4. Read active handoffs. If state and Git disagree, record discrepancy and reconcile
   evidence before dispatch. A missing agent or expired lease is not a finished task.
5. If implementation is still unauthorized, work only on requested planning changes.
   Otherwise dispatch only unblocked tasks with nonoverlapping ownership and review capacity.

## Established decisions

- Public MIT repository; root API/domain/worker module, nested HTTP-only UI/BFF,
  nested tools module, oapi-codegen, SQLite/sqlc/golang-migrate, Goshtoso UI.
- Directory discovery plus qBittorrent/NZBGet provenance/descriptors; full Arr catalog;
  multiple instances; separate registration/import/Jellyfin/Seerr observations.
- Two approvals for unknown Arr titles; independent action API; durable API-owned
  ordered workflows; idempotent desired-state checks, cancellation and uncertainty.
- File operations include copy/hardlink/move/rename/trash/restore/permanent delete.
  Default trash 30 days, UI trash then purge, direct API hard delete supported.
- qBittorrent stopped in trash; permanent deletion removes record without deleting
  unselected payload. Restore leaves stopped as selected engineering default.
- YAML loaded only at startup and read-only through app; managed config editable;
  encrypted managed credentials, supplied key or persistent /data/keys/credentials.key.
- v0.0.1 has no app authentication; Seerr reads only; auth/OIDC deferred to v0.0.2.

## Unresolved gates

Native Arr import can bypass preview rejection and has an external race problem.
Do not enable writes or weaken no-overwrite promises without X-05 evidence and
recorded resolution. Upstream versions/capabilities, SQLite/filesystem behavior,
Goshtoso/App Shell compatibility and actual deployment coordinates have separate
gates in decisions.md. Planning completeness does not mean these tests have passed.

## Current validation and next step

Run `python3 scripts/check_planning.py` and `git diff --check` after planning edits.
Use tasks.json for dependencies, state.json for live ownership/status, and
handoffs/TEMPLATE.md for bounded dispatch/checkpoints. See planning evidence for
what was actually verified. No runtime tests or deployed behavior are claimed.

At every context boundary record the task, exact base/current commit, changed paths,
checks and next safe action. Keep credentials, real inventories and private runtime
coordinates outside this public repository. Never reset or delete unknown work.
