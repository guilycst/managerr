# W-01 independent review, round three

Decision: **approved** for the assigned W-01 package contribution. R2a/R3a/R6
are closed; the prior R1-R5 corrections remain intact. No unresolved finding
was identified in this scoped correction review.

## Review identity and scope

- Reviewer: `/root/x05_reviewer`; independent of product authorship.
- Exact product: `6af605497e62ac182d0d4d4e222cac81aa4232e1`.
- Product tree: `af5fc0139de00107d849573b76ab87e0ec76865f`.
- Exact handoff: `d84a4cdbadd5144f2c5cf2b8a1a21c1d7c0248b8`.
- Handoff tree: `1f746dea71e9cc6ccd3957e13a0e310acc787a2c`; direct parent
  is the exact product. Handoff: `docs/execution/handoffs/W-01.md`.
- Correction base: `9fd30f5be980b6e1ca7b84ea5b7d6a4003734e8a`.
- Prior receipt: `df31bc8e2faf9c505bb45c92204585817f147c1d`.
- Scoped binary correction diff SHA-256:
  `351a192e83a1bc0674b275c1852ed41e6669f0bf949cd93d5a4dca06b81a8def`.
- Reviewed product paths: `internal/reconciliation/`, `internal/planning/`.
  Requirements: W-01, A-08/A-09/A-10/A-11/A-15/A-19, prior receipts and correction
  handoff. Code, test assertions and claims were inspected directly.
- Review ran from a clean detached checkout of the exact handoff. Only this
  receipt is committed. Product, state and unrelated lane files were untouched.

## Direct evidence and finding disposition

| Item | Evidence and result |
| --- | --- |
| R2a: directory subtitle accepted as file | `isSubtitleManifestMember` now requires a nondirectory permitted type with consistent role. Independent `TestReviewerSubtitleRoleAndChildMatrix` rejects a subtitle-role directory and companion, accepts explicit regular/subtitle file children, and retains the exact video reference. Product regression also checks standalone subtitle directory refusal. Closed. |
| R3a: arbitrary mapping accepted as fence | `MappingScopes` names mapping ID, target connection and source root; import validation derives all exact connection/root needs and requires one scoped revision per need. Independent `TestReviewerMappingScopeMatrix` covers two target instances and two source roots, requiring three distinct scope fences. Missing scope/revision, changed revision, replacement ID, wrong root/target, ambiguous same-scope mapping and duplicate scope all reject the old plan. Unrelated current mappings are accepted without weakening required fences. Closed. |
| R6: conflict digest depends on observation order | Canonical conflict keys serialize all authority fields, including sorted evidence and blocking status. Independent `TestReviewerConflictAuthorityCanonicalization` reverses conflicts sharing code/field/target but differing in evidence/blocking; digests match. Display-only edits to an existing plan preserve self-validation. Closed. |
| R1: unchanged-digest blocking bypass | The same independent probe changes only `Status` and `BlockingIssues` on the original blocking plan and attempts approval with its unchanged digest. Approval rejects it. Canonical conflict-derived readiness remains enforced. Closed. |
| R2: exact recursive source/video/subtitle membership | Inspected product regressions reject out-of-manifest sources and nonexistent video references while allowing enumerated directory children. Role/type checks retain separate video/subtitle constraints. All focused regressions pass. Closed for assigned contribution. |
| R3: target connection revision omitted | Every desired predicate target requires its connection revision. Missing/changed current required target revisions conflict. Explicit mapping scope/revision now participates in the plan digest and current-state checks. Closed for assigned contribution. |
| R4: stale absence appears confirmed absent | Aggregation ages coverage at `Input.Now`; later filters recheck freshness using the explicit or current clock. Product fresh-then-expired regression verifies unknown/stale reason, raw proof retention and exclusion from the untracked filter. Missing/unknown evidence remains unknown. Closed. |
| R5: mutable scalar pointers alter earlier plans | Constructor/normalization paths clone registration booleans and episode-number pointers. Inspected mutation/revision tests verify independent values and prior-plan validity. Independent scope matrix also changes caller mapping-scope storage after build and verifies prior-plan validity. Coverage timestamp pointers remain cloned. Closed. |

Instance-scoped observations retain separate registration, import, availability
and request dimensions. Provider/kind identity grouping and deterministic
instance/media ordering remain covered by passing focused tests. Ambiguous
mapping confidence rejects approval-ready intent; selected episode IDs and
subtitle pair/language/forced/SDH labels remain explicit. Exact manifest
validation retains duplicate/overlap, identity/digest and size/entry/depth/child
limits. No truncation is used as a successful exact manifest.

Content predicates still reject same-size unequal bytes and remain unknown
without a digest. Hardlink predicates require the approved source/destination
object identity; equal bytes on a different inode are a conflict. Exact import
read-back preserves per-file associations rather than interpreting command
acceptance as file import. Existing idempotency/predicate regressions pass.

Both reviewed packages retain standard-library/domain/ports imports only, with
no generated upstream DTO, adapter, storage or workflow dependency. The
correction changes planning validation and tests, not an external mutation path.

## Verification

Independent probes ran in a disposable archive of the exact product, outside
the review checkout; no probe source was committed. Synthetic fixtures only;
no live services, credentials or media operations were involved.

| Check | Result |
| --- | --- |
| Three independent `TestReviewer*` matrices, `GOWORK=off`, readonly modules, verbose, race, count 3, timeout 120s | Exit 0; all positive/refusal cases passed |
| `GOWORK=off go test -mod=readonly ./internal/reconciliation ./internal/planning -race -count=3 -timeout=180s` on exact handoff | Exit 0 |
| `GOWORK=off go vet -mod=readonly ./internal/reconciliation ./internal/planning` | Exit 0 |
| CGO-free scoped Linux amd64/arm64 compile, `-run '^$' -exec=true`, `GOWORK=off`, readonly modules | Exit 0 both targets |
| `./scripts/check-guardrails.sh --ci` | Exit 0; committed generation/staged generation, bundled API/Vacuum, architecture, lint, root/nested tests/vet and module verification passed |
| Bundled Vacuum | 100/100; API zero-warning/error gate passed |
| `python3 scripts/check_planning.py` | Exit 0; 44 tasks, 60 acceptance cases; local links resolve |
| Review-start status, scoped SHA identity | Clean exact handoff; direct product parent confirmed |

The assigned W-01 contributions to A-08/A-09/A-10/A-11/A-15/A-19 pass this review.
Full API/storage/executor wiring and cross-system acceptance remain downstream
work, not evidence supplied by these package tests. G-01 stays open: native Arr
no-overwrite guarantees and affected write capabilities remain blocked. This
approval does not enable any Arr write.

The receipt commit SHA is reported after commit. Local review, coordinator
integration, published CI, release, deployment and verified live behavior remain
separate gates.
