# C-03 independent review: domain ports and bootstrap contracts

## Decision

`changes_requested`.

Three P1 findings block C-03. One P2 finding makes the recorded generation
evidence inaccurate. The corrected implementation commit is
`b1be8aee8b95be19e584c576d376ebfe74b00468`; the dispatch base is
`04368e0ad286db4506717c121bb016c3f274e301`.

## Review identity and boundary

- Reviewer: `/root/c03_reviewer`, independent reviewer, not an author of the
  implementation.
- Reviewed paths: `internal/domain/`, `internal/ports/`,
  `internal/bootstrap/`, `docs/generated/environment.md`, and the coordinator
  generation/evidence changes in `scripts/generate.sh`, C-03 handoff, and
  `state.json`.
- Review checkout: shared `main`; coordinator evidence correction at
  `f9381374da6e5c0ee17d47604353894c8bafd5b1` records the actual candidate SHA.
- Isolation: inspected Git and repository files directly. No producer
  transcript, live media service, credential, real inventory, upstream write,
  release, or deployment was used.

## Findings

### P1: An absent tracking value does not require complete coverage

Disposition: `current_blocker`.

`TrackingObservation.Validate` accepts `TrackingAbsent` with an empty
`CoverageID` at `internal/domain/observations.go:285-306`. Even a supplied ID
does not bind the observation to coverage completeness or freshness. This lets
missing or partial evidence become an `absent` result, contrary to
`spec-001-media-reconciliation.md:75-85`, which requires fresh, complete,
relevant coverage and maps interrupted, truncated, forbidden, or disabled
sources to `unknown`.

Required change: make absence carry a validated reference to complete relevant
coverage, including its connection or root scope and freshness input. Reject an
absent observation when that proof is missing, partial, unknown, stale, or
scoped to another instance. Add tests for complete, partial, unknown, missing,
and cross-instance coverage.

### P1: Reconciliation can transition directly into mutation or terminal failure

Disposition: `current_blocker`.

The action transition table at `internal/domain/actions.go:98-100` permits
`reconciling -> running` and `reconciling -> failed`. The state contract at
`data-and-recovery.md:47-55` defines reconciliation as read-only evidence
collection: succeed when the effect is proven, return to `queued` only after
safe non-effect proof, otherwise remain unresolved or move to `needs_review`.
Direct `running` permits another mutation dispatch without the required proof.
Direct terminal failure can hide unresolved effect uncertainty and invite a new
plan to repeat the write.

Required change: remove direct `reconciling -> running` and
`reconciling -> failed` transitions. Preserve the explicit
`reconciling -> queued` path for a proven non-effect and test every legal edge,
especially terminal-state immutability and uncertain-write recovery.

### P1: Filesystem mutation ports discard the approved manifest

Disposition: `current_blocker`.

`FileMap` contains only source and destination paths, and Copy, Hardlink, Move,
Rename, and Restore receive only those maps at `internal/ports/ports.go:275-313`.
The port therefore loses the approved source type, identity, digest, size, and
observation time. It cannot distinguish a replaced source from the reviewed
object or reject a directory passed to hardlink from its request contract.
This conflicts with `data-and-recovery.md:104-133`: execution must recheck
identity, directory plans must bind exact children, and hardlinks require
regular files. It also leaves A-60's shared typed handler boundary unable to
apply the same manifest validation for standalone and workflow execution.

Required change: carry each approved `FileManifestEntry` with its exact
destination for every source mutation, including restore, or pass an immutable
manifest reference that the adapter must resolve before any effect. Add request
validation for empty lists, invalid targets, duplicate or overlapping entries,
and directory hardlink rejection. Keep methods typed and do not add an
arbitrary command or glob interface.

### P2: Candidate diff fails the recorded whitespace check

Disposition: `must_correct_with_C-03`.

`git diff --check 04368e0... b1be8aee...` exits 2 because
`docs/generated/environment.md:17` adds a blank line at EOF. The producer
handoff records the same check as passed. `./scripts/generate.sh --check`
passes because envdoc reproduces those bytes, so reproducibility and whitespace
cleanliness currently disagree.

Required change: make envdoc generation produce a single final newline, rerun
the candidate-range whitespace check, and update the handoff with the exact
result. Expand bootstrap tests to cover missing BFF values and malformed
listener, log-level, path, and URL inputs while touching this lane; current
tests cover defaults, credential conflict/redaction, one valid UI case, one
origin rejection, and one bounds floor.

## Checks that passed

- Runtime UUID and stable configuration ID types are separate. The corrected
  parser rejects whitespace aliases and preserves connection-scoped tracking.
- YAML and API source metadata encode editability and restart-only source
  policy. Snapshot validation rejects duplicate connection, root, and mapping
  IDs and unresolved mapping references.
- Root-relative file targets reject empty, absolute, backslash, traversal,
  repeated-separator, and noncanonical paths. Full no-follow and symlink safety
  remains correctly deferred to filesystem adapters.
- Capability, coverage, tracking, readiness, and normalized upstream error
  vocabularies include explicit `unknown` states. The absence-proof defect above
  remains blocking.
- Environment parsing uses `env.ParseAsWithOptions` through a map seam; `Load`
  reads the process environment once. Defaults, UI-only validation, mutually
  exclusive key sources, exact 32-byte base64 validation, and fixed worker,
  scan, stability, trash, and janitor bounds are present. Secret values do not
  appear in parser errors.
- Port inventory separates read-only discovery from optional writes. Seerr has
  read methods only; download-client removal has no payload-delete option; no
  arbitrary upstream command or shell port exists.
- Focused scan found no credentials, tracker URLs, private hosts, real media
  inventory, user email, or user-specific filesystem paths in the reviewed
  additions.

## Commands and direct results

| Command or inspection | Result |
| --- | --- |
| Git status, log, ancestry, changed paths, remotes, state, and handoff inspection | Passed after coordinator corrected the candidate SHA. Reviewed product commit is `b1be8aee...`; checkout was clean before receipt creation. |
| `GOWORK=off go test ./...` | Passed for bootstrap, domain, and ports. |
| `GOWORK=off go test -race ./internal/...` | Passed. |
| Focused bootstrap/domain tests with `-count=50` | Passed. |
| `GOWORK=off go vet ./...` | Passed. |
| `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOWORK=off go test -run '^$' -exec=true ./internal/...` | Passed. |
| `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 GOWORK=off go test -run '^$' -exec=true ./internal/...` | Passed. |
| `GOWORK=off go mod verify` in root, `ui/`, and `tools/` | Passed in all three modules. |
| `GOWORK=off go test ./...`, `go vet ./...`, and `go mod tidy -diff` in `tools/` | Passed; SQLite/migrate compatibility test remained green. |
| `GOWORK=off go list ./...` in `ui/` | Exit 0 with the expected no-packages warning. |
| `./scripts/generate.sh --check` | Passed and left tracked generated bytes unchanged. |
| `python3 scripts/check_planning.py --self-test` and `python3 scripts/check_planning.py` | Passed: 38 tasks, 60 acceptance cases, local links. |
| Root-to-UI/tools dependency scan and public-data scan | No forbidden imports or sensitive/private data found. |
| `git diff --check 04368e0ad286db4506717c121bb016c3f274e301 b1be8aee8b95be19e584c576d376ebfe74b00468` | Failed, exit 2: generated environment document adds a blank line at EOF. |

## Acceptance contribution assessment

- A-09: not accepted. Tracking records require a connection ID, but provenance
  can still carry a client item or hash without one, and absent tracking lacks a
  scope-checked coverage proof.
- A-38: partial. Source ownership, editability, duplicate resource IDs, and
  unresolved mapping references are typed. Full startup atomicity, retirement,
  mapping ambiguity, and managed mutation behavior remain D-03 work.
- A-40: partial. Bootstrap key selection, conflict handling, format validation,
  and secret-safe errors pass. Persistent key generation, permissions, restart,
  key-check, and no-fallback behavior remain D-02 work.
- A-45: not accepted. Independent tests, vet, module verification, cross-builds,
  and clean envdoc reproduction pass, but the required candidate whitespace
  check fails and its handoff result is inaccurate.
- A-60: not accepted. Action kinds and upstream ports are typed, but the
  filesystem action boundary drops approved manifest evidence and the unsafe
  reconciliation edges would affect standalone and workflow execution alike.

## Next review event

Correct the three P1 findings and generated-output evidence on the same C-03
target. Freeze a new exact implementation commit and request a fresh independent
review. Coordinator owns product fixes, `state.json`, and integration. This
review changed only this receipt and performed no live operation.
