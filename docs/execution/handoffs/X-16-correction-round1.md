# X-16 correction round one: path-bound Radarr preview

## Assignment

- Task ID and title: X-16 correction round one, correct Radarr manual-import
  preview semantics.
- Owner: `/root/x05_implementer`.
- Independent reviewer: `/root/x05_reviewer`.
- Correction base checkpoint: `aa9d7c753703a56225fb65fdbd2794b8bd1b1821`.
- Prior product: `3823f66eaca1796457d7dc9efa93d2ccf5b6bc67`.
- Prior handoff: `6ce4943f920971f82df661f3dcc31dbc648e2a66`.
- Prior review receipt: `d4b57f80b141e447ce7a45656ba5ce29814eb8b1`.
- Branch/worktree: shared checkout on `main`; coordinator owns execution state.
- Owned product path: `clients/radarr/`.
- Owned handoff path: `docs/execution/handoffs/X-16-correction-round1.md`.
- Acceptance contributions: A-07, A-09, A-10 and A-45.

## Finding and correction

The prior product modeled `PreviewLibraryImport` and `PreviewMovieImport` as a
movie-ID-only registered-library scan. The independent review checked Radarr
v3.2.2.5080 source at immutable commit
`2bca1a71a2ed5130ea642343cb76250f3bf5bc4e` and found that Radarr's native
manual-import service needs a source folder or a tracked download path. A
movie ID selects or narrows the movie associated with that source; it does not
resolve a registered movie's library path. The review therefore requested that
the compatibility boundary require a path or reject the unsupported mode.

Product commit: `30aa6734fa7705d61e3d93c9b7f93c8173c4b9b3`
(`fix(radarr): require path-bound manual previews`).

The correction keeps the contract read-only and path-bound:

- The OpenAPI 3.1.1 manual-import operation now requires `folder`. Its
  description records that `movieId` must not be sent by itself and only
  narrows a supplied source folder (with an optional native `downloadId`).
- `PreviewManualImport` always validates a nonempty source folder before
  network dispatch. It accepts an optional positive movie ID and sends it with
  the folder when a caller needs movie-scoped preview evidence.
- The compatibility-named `LibraryImportQuery`, `PreviewLibraryImport` and
  `PreviewMovieImport` remain available for callers of the first draft, but
  they now require and send an explicit folder with movie ID. They never
  resolve or scan a registered movie's library path.
- A movie-ID-only call returns the module's sanitized `ErrorInvalidInput`
  locally and makes zero HTTP requests. The test models the missing-path
  rejection without claiming that Radarr returns any particular native HTTP
  status.
- The former library fixture is now a valid source-folder plus movie-ID
  fixture. It verifies the exact GET query and returned movie identity, so a
  path-bound response cannot be reported as complete from a fictional
  movie-only scan.
- Generated oapi-codegen output was regenerated under
  `clients/radarr/internal/generated`; its generated parameter model now also
  marks `folder` as required.

All prior guarantees remain: no registration/import writes, no raw generated
types in the public client, strict duplicate/trailing/required-field checks,
bounded arrays and bodies, complete-array coverage, API-key transport,
deadlines, redirect refusal, sanitized upstream errors, and movie/movie-file
identity checks.

## Verification

All checks below were run after product commit
`30aa6734fa7705d61e3d93c9b7f93c8173c4b9b3` unless a command is identified as a
repository-wide check. Fixtures are synthetic `httptest` responses only; no
live Radarr service, credential, private endpoint, inventory, media file or
write was used.

| Command or scenario | Result |
| --- | --- |
| `GOWORK=off go test -mod=readonly -count=1 ./...` from `clients/radarr` | Passed. |
| `GOWORK=off go test -mod=readonly -race -count=3 -timeout=180s ./...` from `clients/radarr` | Passed. |
| `GOWORK=off go vet -mod=readonly ./...` from `clients/radarr` | Passed. |
| `GOWORK=off go mod verify` from `clients/radarr` | Passed: `all modules verified`. |
| `GOWORK=off GOPROXY=off GOSUMDB=off go generate ./...` from `clients/radarr` | Passed; generated output unchanged after the reproducibility run. |
| `GOWORK=off GOPROXY=off GOSUMDB=off ./check-generation.sh` from `clients/radarr` | Passed: `Radarr generation checks passed`. |
| Pinned Radarr Vacuum from `tools` with repository ruleset and `--fail-severity=warn` | Passed; quality 100/100 with zero warnings/errors. |
| Pinned Radarr golangci-lint with `GOWORK=off` | Passed: `0 issues.` |
| `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOWORK=off go build -mod=readonly ./...` from `clients/radarr` | Passed. |
| `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 GOWORK=off go build -mod=readonly ./...` from `clients/radarr` | Passed. |
| `python3 scripts/check-architecture.py` | Passed. |
| `./scripts/check-lint.sh --architecture-only` | Passed: architecture and standalone client isolation. |
| `python3 scripts/check_planning.py` | Passed: 53 tasks, 60 acceptance cases; local links resolve. |
| `./scripts/check-api.sh` | Passed; aggregate API/Vacuum gate remained 100/100. |
| `./scripts/check-guardrails.sh --ci` | Passed: aggregate generation, API/Vacuum, architecture, lint, tests, vet and module verification. |
| `git diff --check` and product pre-commit hook | Passed. |
| `TestManualImportLibraryPreviewRejectsMissingPathBeforeNetwork` | Passed; movie-only input returned `ErrorInvalidInput` with zero server calls. |
| `TestManualImportLibraryPreviewUsesNativeMovieScope` | Passed; valid folder plus movie ID sent both query values and returned scoped evidence. |

## Integration and resume checkpoint

The coordinator-owned root generation, lint, test, vet and CI matrices still
do not include the unpublished `clients/radarr` module. This correction did not
edit shared scripts, root dependencies, `go.work` or permanent `replace`
directives. A later C-06/bootstrap step must add Radarr to those matrices and
publish a consumable module before X-19/X-20 adapter migration.

G-01 remains open. Registration, manual-import execution, command, search and
other native writes remain outside this module.

- Product commit: `30aa6734fa7705d61e3d93c9b7f93c8173c4b9b3`.
- Correction handoff commit: pending; this file is intentionally committed
  separately.
- Independent review receipt: pending.
- Coordinator state update: pending; `docs/execution/state.json` was not
  changed by this lane.
- Next safe action: commit this handoff, then assign independent review against
  the exact product tree.
