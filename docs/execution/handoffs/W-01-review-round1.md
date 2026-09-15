# W-01 independent review, round one

Decision: **changes_requested**. Three P1 and two P2 findings remain. Passing
guardrails do not clear these correctness findings.

## Review identity and scope

- Reviewer: `/root/x05_reviewer`; no authorship of the product batch.
- Exact product: `3c4067d4d2562fbe3740a252bfd14f411824ed77`.
- Product tree: `9e2eb1b66ca607a4249dae27cb95b8d52733000c`.
- Exact handoff: `df02e766ebe1d1919b5faa95a08b5f9da23738c3`.
- Handoff tree: `43d9068f8b19963e42ff00e7804e2335e3744d47`; direct parent is
  the reviewed product.
- Base: `bc69ea2ee21823c5a484f545356306bf5f1f5dc1`.
- Product scope: `internal/reconciliation/`, `internal/planning/` only.
- Scoped binary diff SHA-256:
  `3267464257b90a4295cb04f5d2bee9a542a4c5f1ff98239002ce2a1d6700ed09`.
- Review ran in an isolated detached checkout of the handoff. Product and
  execution-state files were untouched. Only this receipt is committed.
- Requirements: W-01, A-08/A-09/A-10/A-11/A-15/A-19 and specification invariants
  I-02/I-05. Evidence was inspected directly, including all four product files.

## Findings

### R1 — P1: blocking conflicts can be bypassed without changing the digest

Locations: `internal/planning/planning.go:1047`, `:1080`, `:1528`.

`Validate` trusts the mutable derived `Status` and `BlockingIssues` instead of
checking that they agree with authority-bearing `Conflicts`. Neither derived
field participates in the digest. A populated invalid plan returned with
`ErrPlanConflict` becomes approvable by setting `Status = StatusReady` and
`BlockingIssues = nil`; its original blocking conflict and digest remain intact.

Deterministic probe `TestReviewerBlockingConflictBypass` builds a copy plan with
`Conflicts = [{Code: "collision", Target: "Film.mkv", Blocking: true}]`, confirms
the original rejection, changes only those two derived fields, then calls
`ValidateApproval` with its unchanged ID/revision/digest and an in-window time.
The call returns nil. This defeats fail-closed conflict validation under A-15.

Required correction: derive blocking readiness from canonical conflicts during
self-validation/approval and reject inconsistent derived fields. Regression
coverage must retain the original digest while attempting the bypass.

### R2 — P1: import selections are not confined to the exact manifest

Locations: `internal/planning/planning.go:530`, `:851`, `:944`.

Manifest and desired-state validation run independently. Neither verifies that
selected import sources are enumerated in the approved manifest, nor that
subtitle video references name approved video members. Valid strings and
nonempty pair IDs are not sufficient evidence of an explicit relationship.

`TestReviewerImportOutsideManifest` builds an `ActionArrImport` plan whose only
manifest entry is `downloads/Film.mkv`, with strong digest and exact inode. Its
sole desired import selection is `downloads/unapproved/Other.mkv` mapped to
`episode-99`. `Build`, `Validate` through approval, and `ValidateApproval` succeed.
The selected import file has no identity/content evidence in the exact manifest.
This violates I-02 and the exact-scope contribution to A-10/A-11/A-15.

Required correction: cross-validate every selected source and association
against the flattened exact manifest and appropriate file role; reject missing,
contradictory or unresolved members before readiness. Add video/subtitle and
directory-child cases, including a nonempty but nonexistent video reference.

### R3 — P1: a targeted connection may have no configuration fence

Locations: `internal/planning/planning.go:90`, `:803`, `:1126`, `:1817`.

`Binding.Validate` validates entries supplied by the caller but does not require
a revision for connections used by desired predicates. `sameRevisionSubset`
accepts an empty expected map. Consequently a ready targeted registration can
omit its connection binding and remain current after that target is reconfigured.

`TestReviewerMissingConnectionFence` builds a registration targeting
`sonarr-main`, with `Binding` containing only source ID/revision. A current
snapshot supplies `ConnectionRevisions["sonarr-main"] = "changed-service"` and
otherwise matches. `ValidateCurrent` returns nil. The immutable plan never bound
the meaningful target configuration required by W-01, R-12 and A-15.

Required correction: require bindings for every relevant target connection,
and explicitly represent/check required mapping revisions for path-bearing
actions. Do not rely on callers to remember optional safety fields. Preserve
subset behavior for genuinely unrelated configuration entries.

### R4 — P2: old absence evidence is presented as confirmed absent

Locations: `internal/reconciliation/reconciliation.go:178`, `:188`, `:405`.

`Input.Now` becomes the result timestamp but is never used to age tracking
coverage. Domain observation validation checks coverage freshness at the old
observation time. `ConfirmedAbsent` checks values and conflicts but does not age
their proof at aggregation/filter time.

`TestReviewerStaleAbsence` supplies complete absent registration observed on
2026-01-01 with `CoverageMaxAge = 1h`, and aggregates at `Now = observation + 24h`.
The result remains known absent and `ConfirmedAbsent` returns true. This
contradicts the handoff claim that stale observations remain unknown and violates
I-05. An old cache can thus enter the filtered untracked view.

Required correction: evaluate absence authority against the current clock,
retain aged raw evidence for display, and return unknown/false when it expires.
Test aggregation and later filtering across the freshness boundary.

### R5 — P2: plan constructors retain mutable scalar pointers

Locations: `internal/planning/planning.go:1598`, `:1641`.

Normalization copies registration/episode structs but retains nested scalar
pointers (`Monitored`, `SeasonFolder`, `SeasonNumber`, `AbsoluteNumber`). This
breaks the advertised copying guarantee for `NewDesiredState`, `Build` and
`NewRevision` even when callers never mutate the returned plan itself.

`TestReviewerDeepCopyAliasing` passes `Monitored: &monitored` with false through
`NewDesiredState` and `Build`. Changing the caller-owned boolean to true changes
the previously built plan. Its retained digest then fails validation. This is
fail-closed at approval but corrupts an immutable prior revision and can change
in-memory desired-state evaluation.

Required correction: deep-copy every scalar pointer in predicate construction/
normalization. Test caller mutation and revision-to-revision independence for
all pointer fields, rather than only maps and slices.

## Checks and direct evidence

Reviewer probes used an archive of the exact product in a disposable directory.
They deliberately assert the unexpected behavior above, so their pass means
the defect was reproduced; they are not success tests for the implementation.
No probe modified the isolated product checkout or used live data.

| Command/evidence | Result |
| --- | --- |
| Five `TestReviewer*` probes, focused packages, verbose, count 1, timeout 90s | Exit 0; all five reproductions confirmed |
| Same reviewer probes with `-race -count=3 -timeout=120s` | Exit 0; all reproduced, no race report |
| `GOWORK=off go test -mod=readonly ./internal/reconciliation ./internal/planning -race -count=3 -timeout=180s` on exact handoff | Exit 0 |
| `GOWORK=off go vet -mod=readonly ./internal/reconciliation ./internal/planning` | Exit 0 |
| Scoped compile, `CGO_ENABLED=0`, Linux amd64 and arm64, `-run '^$' -exec=true`, `GOWORK=off`, readonly modules | Exit 0 both targets |
| `./scripts/check-guardrails.sh --ci` | Exit 0; committed generation/staged generation, bundle/Vacuum, architecture, lint, tests/vet and standalone module verification passed |
| Bundled Vacuum | 100/100; API runner passed its zero-warning/error gate |
| `python3 scripts/check_planning.py` | Exit 0; 44 tasks, 60 acceptance cases, local links resolve |
| `git diff --check`, review-start status | Exit 0; clean exact checkout before review |

Existing tests directly establish per-instance observations and dimensions,
provider/kind identity grouping, explicit unknown handling, raw conflicting
evidence retention, rejection of explicitly ambiguous confidence, exact import
read-back comparison, unequal-content copy conflicts and different-inode
hardlink conflicts. Those contributions pass. Architecture inspection confirms
the two packages import only standard library and root domain/ports, with no
upstream/generated DTO or storage/adapter dependency.

A-15 remains blocked by R1/R2/R3; A-10/A-11 exact relationship/scope contributions
remain blocked by R2. Fresh absence claims remain blocked by R4. Immutable
constructor independence remains blocked by R5. A-19's assigned content/inode
predicate cases pass. Full cross-system acceptance is not inferred from these
package tests. G-01 remains open; this review enables no Arr capability or write.

The receipt commit SHA is reported to the coordinator after commit. Integration,
CI publication, release, deployment and verified live behavior are separate gates.
