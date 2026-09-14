# F-02 independent review, round three

## Decision

`changes_requested`.

The suffix parser fixes the reported short-title language defect and preserves
the dotted forced/SDH cases and complete IDX/SUB fixture. A-11 is still blocked:
the single-video fallback silently converts an unrelated subtitle into an exact
pair, and bracketed forced/SDH labels are not retained by the flag parser.

## Review identity and scope

- Reviewer: `/root/x05_reviewer`, independent of product author
  `/root/d02_implementer`.
- Reviewed product:
  `dcd3b8c9d93d3b0ea3d9a0592e857b36aa2c8294`; direct parent
  `f71bd49bc32d67650f820fe6cc46e4736bae7556`; tree
  `bb9b3b90e4695842bf454d492a3524ba60ef4f8f`.
- Reviewed handoff:
  `d32ef292d7c55dd0406ff99874fce37d0c5e3736`; direct product parent; tree
  `c74b8f3c7b7c958086a400c594364c5d7efd5833`.
- Prior review receipt:
  `ffe5b8a96634f0e4605977f20e82454949f13d03`.
- Product scope: `internal/discovery/` only.
- Scoped correction diff SHA-256:
  `436d74ae1d066eb53a07aaed38ec94575041aa68ee50b39a478155cc1f2df18a`.
- Acceptance reviewed: A-01, A-02, A-10, and A-11.

Executable review used a clean exact-handoff worktree and synthetic fixtures.
Adversarial tests were supplied with Go overlays, so no product file was edited.
No live service, credential, private endpoint, media, upstream mutation,
filesystem mutation, release, or deployment was used.

## Findings

### P1: a sole unrelated subtitle is silently scope-expanded into an exact pair

When filename matching finds no candidate, `subtitleFor` unconditionally adds
the only video at `internal/discovery/discovery.go:1031-1032`. The result is then
marked `exact` and `paired subtitle` at lines 1041-1044. `classifyFiles` treats
that reason as matched at lines 916-918, so it creates no
`CompanionUnmatchedSubtitle` evidence.

An independent overlay probe used one video and an unrelated subtitle. It
observed:

```text
unmatched relationship={FilePath:captions.en.srt VideoPaths:[Up.mkv]
Language: Confidence:exact Reason:paired subtitle}
unmatched retention subtitles=[{... VideoPaths:[Up.mkv] ...
Confidence:exact Reason:paired subtitle}] companions=[]
```

The new regression at `internal/discovery/discovery_test.go:535-537` checks only
that fallback language is empty. It does not assert candidate scope,
confidence, reason, or unmatched-companion retention. This contradicts A-11's
explicit unmatched-companion and no-scope-expansion result and the handoff's
claim that unmatched relationships remain unresolved. Required correction:
remove the fallback from exact matching or retain it only as an unresolved
suggestion with no selected `VideoPaths`; ensure classification emits the
unmatched companion.

Disposition: `current_blocker`.

### P1: bracketed forced and SDH labels lose their flags

The correction normalizes square and round brackets in `subtitleTokens` at
`internal/discovery/discovery.go:1087-1090`, so suffix-language parsing accepts
those common label shapes. Flag extraction still uses `containsToken` at lines
1106-1109, whose delimiter set includes only dot, underscore, and hyphen.
Consequently the language is retained while the adjacent forced/SDH label is
silently lost.

Independent probes observed:

```text
bracketed forced={FilePath:Up.[forced].en.srt Language:en Forced:false
Confidence:exact Reason:paired subtitle}
parenthesized sdh={FilePath:Up.(sdh).pt.srt Language:pt
HearingImpaired:false Confidence:exact Reason:paired subtitle}
```

A-11 requires forced/SDH labels to be preserved. Required correction: use the
same tokenization contract for label flags and suffix parsing, then add bracket
and parenthesis regressions for both flags.

Disposition: `current_blocker`.

## Confirmed corrected behavior

- Independent probes confirmed `Up.forced.en.srt` maps language `en` with the
  forced flag and `The.Movie.sdh.pt.srt` maps `pt` with the SDH flag. Short title
  words no longer become languages.
- Two matching videos leave language empty and relationship confidence
  ambiguous.
- A complete `The.Movie.sdh.pt.idx`/`.sub` pair retains both entries, shared pair
  ID, `pt`, SDH, and exact pairing without unmatched companions.
- The prior pagination bounds, incomplete-client readiness, connection-scoped
  evidence, root namespace/movie-year, and deep-clone regressions all pass.
- Discovery remains read-only and imports no adapter, storage, UI, or mutation
  owner.

## Independent checks

| Command or scenario | Result |
| --- | --- |
| Exact product/parent/tree, handoff/tree, clean status, scoped paths and diff hash | Passed. |
| Independent short-title, dotted-label, ambiguous, and complete IDX/SUB overlay probe | Passed. |
| Independent unmatched-single-video and bracketed-label overlay probe | Failed as the two findings record. |
| `GOWORK=off go test -count=50 ./internal/discovery` | Passed. |
| `GOWORK=off go test -race -count=10 ./internal/discovery` | Passed. |
| `GOWORK=off go vet ./internal/discovery` and `GOWORK=off go mod verify` | Passed; all modules verified. |
| `GOWORK=off go test -mod=readonly -count=1 ./...` and `GOWORK=off go vet -mod=readonly ./...` | Passed. |
| `./scripts/check-lint.sh` | Passed; zero lint issues and architecture boundaries passed. |
| `python3 scripts/check_planning.py --self-test` and `python3 scripts/check_planning.py` | Passed; 44 tasks and 60 acceptance cases. |
| `./scripts/generate.sh --check` and `./scripts/check-api.sh` | Passed; generation reproduced and Vacuum reported 100/100. |
| `./scripts/check-guardrails.sh --fast` | Passed. |

## Acceptance disposition

- A-01: accepted for the F-02 contribution; read-only discovery and historical
  orphan evidence remain intact.
- A-02: accepted for the F-02 contribution; bounded pagination and per-source
  incomplete coverage regressions remain intact.
- A-10: accepted for the F-02 contribution; conservative movie, anime,
  multi-episode, and season-pack suggestions remain intact.
- A-11: not accepted. Unrelated single-video subtitles are silently paired, and
  bracketed forced/SDH flags are discarded.

No product file, F-04 path, task definition, shared script, module manifest, or
`docs/execution/state.json` was modified by the reviewer.
