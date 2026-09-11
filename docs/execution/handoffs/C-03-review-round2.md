# C-03 independent review, round two

## Decision

`changes_requested`.

Round-two candidate `cc0d3501e8bc62378596ecca14763cc65b311a46`
fixes reconciliation transitions and generated-document whitespace. Filesystem
manifest binding is only partly fixed, and absence freshness remains
unenforced. One instance-scoping defect from A-09 also remains.

## Review identity

- Reviewer: `/root/c03_reviewer`, independent of the coordinator author.
- Dispatch base: `04368e0ad286db4506717c121bb016c3f274e301`.
- Reviewed implementation commit:
  `cc0d3501e8bc62378596ecca14763cc65b311a46`.
- Round-one receipt: `4b64712419406ca5a243a2fa108969dd3240dd6d`.
- Boundary: C-03 domain, ports, bootstrap, generated environment document,
  generation script, and coordinator-owned state and handoff evidence.
- No live media service, credential, private inventory, upstream write,
  release, or deployment was used.

## Resolved findings

### Reconciliation transitions

Resolved. `ActionReconciling` no longer transitions directly to
`ActionRunning` or `ActionFailed`. It can return to queued for proven
non-effect, succeed from read-back evidence, move to review, or stop through
cancellation/deadline. This matches `data-and-recovery.md:47-55`.

### Generated environment whitespace

Resolved. Direct `go generate ./internal/bootstrap` and
`./scripts/generate.sh --check` both pass. The tracked document ends with one
newline. Candidate-range `git diff --check` exits 0.

### Filesystem manifest carriage

Partly resolved. `FileMap.Source` now carries `FileManifestEntry`; empty,
duplicate, overlapping, and invalid maps are rejected. Hardlink has a distinct
request type and rejects directory entries. Remaining evidence validation is a
P1 finding below.

## Findings

### P1: Stale coverage still proves an absent tracking value

Disposition: `current_blocker`.

`TrackingObservation.Validate` now requires complete, completed coverage with a
matching coverage ID and connection. It checks timestamp order, but it has no
freshness bound. A complete coverage observation from one year ago validates as
authority for an `absent` observation created now. This still violates
`spec-001-media-reconciliation.md:75-85,201`, where stale evidence must not
become a fresh absence or safety claim.

An overlay test against the exact candidate failed with:

```text
one-year-old coverage accepted as fresh absence authority
```

Required change: carry a positive freshness bound or explicit validity time,
validate it against the coverage observation/completion time, and test stale
coverage at the boundary. Do not use wall-clock reads inside the value object;
bind the evaluation time in the observation or validator input.

### P1: Filesystem requests accept identity-free and unbounded directory sources

Disposition: `current_blocker`.

The corrected port carries manifest fields but never requires them.
`FileManifestEntry.Validate` accepts an entry with empty `FileIdentity` and
`Digest`, and every filesystem request reuses that validation. Copy therefore
accepts a source it cannot distinguish from a replacement. Copy also accepts a
directory entry without an exact child manifest. Since the overlap validator
rejects parent and child entries together, the request cannot encode the exact
recursive scope required by `data-and-recovery.md:104-133`.

An overlay test against the exact candidate failed with both errors:

```text
copy accepted source without identity or digest evidence
copy accepted directory without an exact child manifest
```

Required change: validate action-specific evidence. Copy needs source identity
and a strong digest; hardlink, move, rename, trash, restore, and delete need the
identity required for their read-back predicate. Reject directory mutations
until the request binds every affected child, or add a typed exact-directory
manifest that represents parent and children without treating them as illegal
overlap. Keep hardlink directory rejection.

### P1: Client provenance can still omit its instance

Disposition: `current_blocker`.

`Provenance.Validate` accepts `ClientItemID` or `Hash` while `ConnectionID` is
empty at `internal/domain/observations.go:324-346`. Repeated numeric IDs or
hashes from two download clients can therefore enter the model without their
instance scope. This contradicts A-09 and the product requirement that observed
relationships retain their source connection.

Required change: require a valid connection ID whenever any client-specific
provenance field is present. Represent an unknown client by omitting provenance
or by an explicit unknown observation, not by an unscoped external ID. Add the
two-instance collision case.

### P2: Bootstrap validation cases requested in round one remain untested

Disposition: `must_correct_with_C-03`.

`internal/bootstrap/environment_test.go` is unchanged. It still lacks missing
BFF variable cases and malformed listener, log-level, data/config path, API URL,
and public-origin path/query cases. The parser code is typed and secret-safe in
the inspected paths, but C-03 does not leave the required regression evidence
from the managing-go-environment contract.

Required change: add table-driven cases through the existing map seam. Keep one
focused test table; no new test framework or fixtures are needed.

## Checks and direct results

| Command or inspection | Result |
| --- | --- |
| Git identity, ancestry, state, handoff, changed paths, and clean-start inspection | Passed. Candidate descends from the dispatch base and state names the exact SHA. |
| `GOWORK=off go test ./...` | Passed for bootstrap, domain, and ports. |
| `GOWORK=off go test -race ./internal/...` | Passed. |
| `GOWORK=off go vet ./...` | Passed. |
| Linux amd64 and arm64, `CGO_ENABLED=0 GOWORK=off go test -run '^$' -exec=true ./internal/...` | Passed for both architectures. |
| `GOWORK=off go mod verify` in root, `ui/`, and `tools/` | Passed in all modules. |
| Tools `go test ./...`, `go vet ./...`, and `go mod tidy -diff` | Passed. |
| `GOWORK=off go generate ./internal/bootstrap` | Passed; generated document retained one final newline. |
| `./scripts/generate.sh --check` | Passed. |
| `git diff --check 04368e0ad286db4506717c121bb016c3f274e301 cc0d3501e8bc62378596ecca14763cc65b311a46` | Passed. |
| `python3 scripts/check_planning.py --self-test` and `python3 scripts/check_planning.py` | Passed: 38 tasks, 60 acceptance cases, local links. |
| Root dependency and public-data scans | No UI/tools internal import, credential, private endpoint, real inventory, user email, or user-specific path found. |
| Temporary overlay test for one-year-old complete coverage | Failed as expected, proving stale coverage is accepted. Temporary files removed. |
| Temporary overlay test for copy without identity/digest and directory without children | Failed both subtests as expected. Temporary files removed. |

## Acceptance contribution assessment

- A-09: not accepted. Tracking coverage is instance-scoped, but client
  provenance still permits unscoped external IDs.
- A-38: C-03 contribution accepted. Source ownership, editability, duplicate ID
  checks, and mapping references are typed. Startup loading, retirement, and
  ambiguity behavior remain D-03 gates.
- A-40: C-03 contribution accepted. Bootstrap source selection, conflict,
  exact key format, and secret-safe errors are present. Persistent lifecycle
  behavior remains D-02.
- A-45: generation and module contribution accepted. Envdoc reproduction,
  whitespace, module verification, vet, race, and both cross-builds pass. The
  missing bootstrap negative tests remain a C-03 evidence correction.
- A-60: not accepted. Typed request variants improved, but identity-free and
  unbounded directory inputs cannot share safe standalone/workflow execution.

## Next review event

Fix the three P1 findings and bootstrap test evidence, freeze a new exact
implementation commit, and request another independent review. Coordinator owns
product files, state, and integration. This review changed only this receipt.
