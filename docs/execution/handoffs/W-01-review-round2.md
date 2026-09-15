# W-01 independent review, round two

Decision: **changes_requested**. R1/R4/R5 are closed; R2/R3 retain P1 gaps.
A P2 deterministic-digest finding is also confirmed.

## Review identity

- Reviewer: `/root/x05_reviewer`, independent of product authorship.
- Exact source: `7a7ab9883b377ff2e3e7e8be6d399c40c2f633c9`.
- Source tree: `d6196a6c4693ef8194a213fbda6661b54a1afc9c`.
- Exact handoff: `57bc0c99a650987fe6db738c3ed0ba0036ff5dd6`.
- Handoff tree: `34345f80c8c67eabbc5d7b2259ce73f20a816d0e`; its direct parent
  is the exact product. Handoff is `docs/execution/handoffs/W-01.md`.
- Correction base: `572da6bf11e28611aedede7d10217983aa74fb1a`.
- Prior receipt: `ff16c554aa93459130ea88dd184fb2b80bdac382`.
- Scoped correction binary diff SHA-256:
  `ca54107095bb24a3f71517a3524331b76c1d114cab08fde91c62e9776547caf5`.
- Scope: `internal/reconciliation/`, `internal/planning/`, correction handoff,
  W-01 and A-08/A-09/A-10/A-11/A-15/A-19. Product evidence and tests were inspected
  directly; producer conclusions were not adopted.
- Review used a clean detached checkout of the exact handoff. Only this receipt
  is committed; no product, state or unrelated lane files were modified.

## Findings

### R2a — P1: subtitle role permits a directory root as an import file

Locations: `internal/planning/planning.go:1047`, `:1069`, `:1116`.

`isSubtitleManifestMember` accepts any entry with `RoleSubtitle`, including
`Type = ManifestDirectory`. The manifest validator allows that role/type
combination. Consequently the new recursive scope validation still lets a
directory root through as an exact subtitle file. This contradicts the handoff's
explicit claim that directory roots used as files are rejected.

Independent probe `TestReviewerSubtitleDirectoryAcceptedAsFile` constructs:

- An exact manifest directory `downloads/pack`, type directory, role subtitle,
  identity `dir-inode`, with explicit video child `downloads/pack/Film.mkv`.
- An import selection whose `Source` is the directory itself, `Subtitle = true`,
  language `en`, pair ID `pair`, and video reference to that child.
- Valid connection/mapping revisions and approval timestamps.

`Build` and `ValidateApproval` return nil. The selected source is a directory,
not the enumerated subtitle file promised by the action contract. R2's exact
file/role protection remains incomplete under I-02 and A-10/A-11/A-15.

Required correction: require a permitted nondirectory file type for subtitle
members, with consistent role/type semantics. Reject directory/companion
misclassification in both import and standalone subtitle predicates. Add the
directory-role regression while retaining explicit directory-child acceptance.

### R3a — P1: mapping fence accepts an unrelated mapping

Locations: `internal/planning/planning.go:990`, `:997`, `:1278`, `:2021`.

Target connection revisions are now mandatory, closing the original empty
connection-map reproduction. Mapping safety is weaker: any nonempty
`MappingRevisions` map satisfies the Arr-import requirement. The plan has no
selected mapping-to-target/root association from which to require the actual
relevant revision. Current-state subset comparison therefore cannot detect
changes to an omitted mapping that resolves an approved source for its target.

`TestReviewerUnrelatedMappingFence` builds an import of `downloads/Film.mkv`
targeting `sonarr-main`. Its only mapping fence is
`unrelated-jellyfin-library-mapping: map-1`. `Build` accepts it. A matching current
snapshot additionally reports
`actual-sonarr-download-mapping: changed-destination-namespace`; `ValidateCurrent`
returns nil. The arbitrary unrelated map is sufficient authority even though no
revision for the target/download-root mapping was bound.

This is the unclosed mapping part of R3: requiring a placeholder map is not an
explicit relevant-mapping fence under W-01/R-12/A-15. It also fails for imports
spanning multiple target connections or configured source roots when only one
irrelevant mapping is supplied.

Required correction: represent selected mappings with their relevant target
connection/root scope and bind each required mapping ID/revision. Validate that
every mapped source/target has the appropriate fence; reject unrelated-only,
missing and changed mapping evidence. Keep subset treatment for truly unrelated
configuration. A downstream caller convention alone does not close this typed
plan-validation gap.

### R6 — P2: equivalent conflict sets produce different plan digests

Locations: `internal/planning/planning.go:1972`, `:2056`.

Conflict sorting is not total over authority-bearing fields. Normalization
sorts code/field/target/message; digest sorting uses only code/field/target.
Neither breaks ties with evidence and blocking status. Distinct conflicts with
the same display/key fields therefore retain input order in the digest. This
gap existed in the initial batch and was confirmed during the requested
determinism check; it is not attributed as newly introduced by this correction.

`TestReviewerConflictDigestOrder` builds two otherwise identical copy requests
with conflicts having code `same-code`, target `same-target`, message
`same-display`, and evidence `a` versus `b`. Reversing only the conflict slice
changes the digest:

- Forward: `sha256:46bd3c7efad31981eb9f715218cd2773835b53b2e3e79af3f379b5bb8c31dcfa`.
- Reverse: `sha256:0c7c4b1cb6892f9c3c76f5366da43796244b64ac00b1a7ea1bec08579464fa6d`.

The semantic conflict set and all plan inputs otherwise match. This undermines
the advertised deterministic immutable-intent digest and can spuriously demand
a new approval after observation order changes.

Required correction: use a total canonical ordering over conflict authority
fields, independent of display prose, consistently in normalization, derived
blocking comparison and digest payloads. Add reversed-order and display-edit
cases for multiple conflicts sharing code/field/target.

## Closed items and retained evidence

- **R1 closed:** direct inspection confirms readiness/blocking issues are derived
  from canonical conflicts and checked during validation. Independent
  `TestReviewerOriginalBlockingBypassNowRejected` repeats the original mutation
  of only status/issues with the unchanged digest; approval rejects it.
- **R2 partially corrected:** out-of-manifest import sources and missing/wrong
  video references are rejected; explicit recursive directory children pass.
  Product scope regressions were inspected and passed. R2a remains above.
- **R3 partially corrected:** every predicate-target connection now requires a
  revision, and missing/changed current target bindings conflict. The inspected
  connection regression passed. R3a remains above.
- **R4 closed:** aggregation uses its current clock; explicit-clock filters
  re-age proofs; convenience filters use the UTC clock. Stale absent evidence
  becomes unknown while raw proof remains visible. The fresh-then-expired
  regression and unknown/missing evidence tests passed.
- **R5 closed:** constructors/normalization clone all registration boolean and
  episode-number pointers, including revisions. The inspected mutation tests
  pass for caller booleans/numbers, plan build and distinct revision pointers.
  Coverage start/completion timestamp pointers are also cloned.
- Instance-scoped identities, separate tracking dimensions, deterministic
  connection/media ordering, raw contradictory evidence, explicit ambiguous
  mapping rejection, selected episode/subtitle labels, bounded manifest checks,
  unequal-content copy conflicts and unequal-inode hardlink conflicts remain
  covered by passing focused tests. No upstream/generated DTO leakage or
  adapter/storage dependency appears in either package.

## Verification

Independent probes ran in an archive of the exact product, outside the review
checkout. The three finding probes deliberately assert unexpected behavior;
their pass is reproduction evidence, not product acceptance. All fixtures are
synthetic, with no live service or filesystem mutation.

| Check | Result |
| --- | --- |
| Four independent `TestReviewer*` probes, `GOWORK=off`, readonly modules, verbose, race, count 3, timeout 120s | Exit 0; three findings reproduced and unchanged-digest bypass rejected each time |
| `GOWORK=off go test -mod=readonly ./internal/reconciliation ./internal/planning -race -count=3 -timeout=180s` on exact handoff | Exit 0 |
| `GOWORK=off go vet -mod=readonly ./internal/reconciliation ./internal/planning` | Exit 0 |
| CGO-free scoped Linux amd64/arm64 compile, `-run '^$' -exec=true`, `GOWORK=off`, readonly modules | Exit 0 both targets |
| `./scripts/check-guardrails.sh --ci` | Exit 0; generation/staged generation, bundled API/Vacuum, architecture, lint, root/nested tests/vet and module verification passed |
| Bundled Vacuum | 100/100; API runner's zero-warning/error gate passed |
| `python3 scripts/check_planning.py` | Exit 0; 44 tasks, 60 acceptance cases; local links resolve |
| `git diff --check`, review-start Git status | Exit 0; clean exact checkout |

A-15 remains blocked by R2a/R3a/R6; the exact-file contribution to A-10/A-11
remains blocked by R2a. A-08/A-09/A-19's assigned focused cases pass; full
cross-system acceptance remains a downstream gate. G-01 stays open; no native
Arr write capability is enabled by this review.

The receipt commit SHA is reported after commit. Integration, published CI,
release, deployment and verified live behavior are separate gates.
