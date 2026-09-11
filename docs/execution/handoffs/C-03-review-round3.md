# C-03 independent review, round three

## Decision

`changes_requested`.

The exact implementation commit
`f06110968e9c86c336acbc7bf7dfe21af7137218` fixes coverage freshness,
connection-scoped client provenance, recursive manifest evidence, and the
missing bootstrap rejection tests. Two filesystem validation defects remain:
a directory can target its own descendant, and non-copy actions inherit copy's
mandatory digest rule.

## Review identity

- Reviewer: `/root/c03_reviewer`, independent of the coordinator author.
- Dispatch base: `04368e0ad286db4506717c121bb016c3f274e301`.
- Reviewed implementation commit:
  `f06110968e9c86c336acbc7bf7dfe21af7137218`.
- Coordinator/state metadata commit:
  `4734ceae36ab060ae50e48916643cb4098748c78`.
- Earlier receipts: round one
  `4b64712419406ca5a243a2fa108969dd3240dd6d`; round two
  `1b4111b76acee7fc7bd7971cf2e322933caf19fb`.
- Boundary: C-03 domain, ports, bootstrap, generated environment document,
  generation script, and coordinator-owned execution evidence.
- No live media service, credential, private inventory, upstream write,
  filesystem mutation, release, or deployment was used.

## Corrected round-two findings

### Fresh complete coverage for absence

Resolved. `TrackingObservation.Validate` requires a positive freshness bound,
complete and completed embedded coverage, matching runtime coverage ID and
connection ID, correct timestamp order, and age no greater than the bound. The
candidate test rejects stale coverage. The temporary boundary test accepted
coverage exactly at the bound and rejected coverage one nanosecond beyond it.

### Recursive manifest evidence

Partly resolved. Filesystem action entries now require nonblank file identity.
Copy requires a syntactically valid SHA-256 digest for each selected non-directory
entry. Directory entries require recursively validated, root-confined children,
and hardlink rejects directories. Empty, duplicate, overlapping, identity-free,
and weak copy manifests are rejected. The remaining same-mapping path ambiguity
is a P1 finding below.

### Connection-scoped client provenance

Resolved. Client item IDs and hashes require a valid connection ID. Candidate
and temporary tests reject unscoped identifiers and accept the same identifier
when it is bound to a valid connection.

### Bootstrap negative validation evidence

Resolved. The map seam now tests missing BFF API URL/public origin, malformed API
and UI listeners, unsupported log level, relative data/config paths, non-HTTP API
URL, and public-origin path/query inputs. Existing cases retain malformed-key
redaction, key-source conflict, defaults, and bounds-floor evidence.

## Findings

### P1: A directory action can target its own descendant

Disposition: `current_blocker`.

`validateFileMaps` compares sources with other sources and destinations with
other destinations, but never compares a mapping's source with its own
destination. A same-root request mapping directory `Film` to `Film/copy`
therefore validates. Copying there changes the selected source tree while it is
being traversed and can create recursion outside the approved child manifest.
The same missing check permits equal or ancestor/descendant source/destination
relationships for move, rename, and restore.

The temporary overlay test against the exact candidate failed with:

```text
directory copy into its own descendant accepted
```

This violates the path-prefix ambiguity and unreviewed-recursion requirements in
`spec-001-media-reconciliation.md:169-177`, I-02 and I-07, and
`data-and-recovery.md:104-133`.

Required change: validate each mapping's source and destination together. Reject
equal and ancestor/descendant relationships within the same configured root
where they can alias or expand the operation. Add directory descendant and
ancestor cases to the permanent port tests.

### P2: Hardlink, move, and rename inherit copy's digest prerequisite

Disposition: `must_correct_with_C-03`.

`FilesystemHardlinkRequest.Validate` passes `requireDigest=true`, while `Move`
and `Rename` accept `FilesystemCopyRequest`, whose validator also requires a
digest. An identity-bound regular-file hardlink without a digest is rejected as
`filesystem copy requires a strong content digest`. The contract requires a
strong digest for copy because it proves equal bytes across different objects;
hardlink and same-filesystem move/rename use object identity and their own
read-back predicates. See `spec-001-media-reconciliation.md:128-136` and
`data-and-recovery.md:115-125`.

Required change: use action-specific request validation. Copy requires identity
and a strong digest. Hardlink, move, and rename require identity; hardlink keeps
its directory rejection. Give move and rename typed request contracts rather
than routing their validation through a copy-named request.

## Checks and direct results

| Command or inspection | Result |
| --- | --- |
| Git identity and ancestry inspection | Passed. The candidate is a commit descending from the dispatch base; HEAD metadata names the exact SHA. |
| `GOWORK=off go test -count=1 ./...` | Passed for bootstrap, domain, and ports. |
| `GOWORK=off go test -count=1 -race ./internal/...` | Passed. |
| `GOWORK=off go vet ./...` | Passed. |
| `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOWORK=off go test -count=1 -run '^$' -exec=true ./internal/...` | Passed. |
| `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 GOWORK=off go test -count=1 -run '^$' -exec=true ./internal/...` | Passed. |
| `GOWORK=off go mod verify` in root, `ui/`, and `tools/` | Passed in all three modules. |
| Tools `GOWORK=off go test -count=1 ./...`, `GOWORK=off go vet ./...`, and `GOWORK=off go mod tidy -diff` | Passed. |
| `GOWORK=off go generate ./internal/bootstrap` | Passed; tracked envdoc content remained reproducible with one final newline. |
| `./scripts/generate.sh --check` | Passed: `generation checks passed`. |
| `python3 scripts/check_planning.py --self-test` | Passed: cycle, dependency, and completion self-checks; 38 tasks and 60 acceptance cases valid. |
| `python3 scripts/check_planning.py` | Passed: 38 tasks, 60 acceptance cases, local links resolved. |
| `git diff --check 04368e0ad286db4506717c121bb016c3f274e301..f06110968e9c86c336acbc7bf7dfe21af7137218` | Passed. |
| Root-to-UI/tools import scan | Passed: no UI/tools imports of root internal packages. |
| Candidate public-data scan | Passed: no user paths, private IPv4 addresses, PEM keys, or assigned credential-like values in reviewed product paths. |
| `GOWORK=off go test -count=1 -overlay=<temporary-overlay> ./internal/domain ./internal/ports` | Domain freshness/provenance boundaries passed. Ports failed the identity-only hardlink and same-root descendant-copy cases quoted above. Temporary files were outside the repository and removed automatically. |

Concurrent U-00 edits to `ui/go.mod` and `ui/go.sum` appeared after the C-03
generation checks. They are outside the reviewed candidate diff and C-03 owned
paths. This review did not modify, revert, stage, or otherwise incorporate them.

## Acceptance contribution assessment

- A-09: C-03 contribution accepted. Tracking and client provenance retain their
  connection scope; same external identifiers can remain distinct per instance.
- A-38: C-03 contribution accepted. Source ownership, editability, duplicate ID
  checks, and mapping references are typed. Startup loading and retirement remain
  downstream configuration gates.
- A-40: C-03 contribution accepted. Bootstrap key-source conflict, exact key
  format, persistent-source selection, and secret-safe errors are represented;
  persistent key lifecycle remains downstream.
- A-45: C-03 contribution accepted. Envdoc reproduction, candidate whitespace,
  GOWORK-off module checks, tests, vet, race, and both Linux architectures pass.
- A-60: not accepted. The common filesystem boundary still accepts an unsafe
  source/destination relationship and does not preserve action-specific evidence
  requirements.

## Next review event

Correct the two filesystem findings, freeze a new exact implementation commit,
and request another independent review. Coordinator owns product files, state,
and integration. This review changed only this receipt.
