# F-02 independent review, round four

## Decision

`changes_requested`.

Both round-three findings are closed. `captions.en.srt` beside `Up.mkv`
remains unresolved with no selected video or language and is retained as an
explicit unmatched companion. Bracketed and parenthesized forced/SDH labels
survive tokenization, suffix language remains correct, root grouping stays
together, and complete bracketed IDX/SUB pairs retain their pair identity.

One related A-11 blocker remains. Label extraction still scans the entire
subtitle basename instead of the suffix after the matched video stem. Media
titles that are themselves `Hi` or `Forced` are therefore promoted into false
subtitle flags while the relationship is recorded as exact.

## Review identity and scope

- Reviewer: `/root/d02_reviewer`, independent of product author
  `/root/d02_implementer`.
- Reviewed product:
  `05dcd107d83b77632a0212d5faec31c65e3d4e13`; direct parent
  `69e686cdc7bf13618042f0a04327e3f9e00f126f`; tree
  `b7f311865fb20ac600d94d410b0c56f3551ed111`.
- Reviewed handoff:
  `da632a65257c6b5e477c006f78be3d6526cfc8ef`; direct parent is the product;
  tree `5d487a06827e03d2eda27e8065261da3fe3df1dc`.
- Prior review receipt:
  `e451a682bc1c7457a489d8328b3b76d0755ae993` at
  `docs/execution/handoffs/F-02-review-round3.md`.
- Product scope: `internal/discovery/` only.
- Scoped product diff SHA-256:
  `1750f152f24c7aaa812c9a6055c7421b6cc418ed2e7f83379a9a7339df1bd3c1`.
- Acceptance reviewed: A-01, A-02, A-10 and A-11.
- Review receipt checkpoint: commit containing this file; exact SHA is reported
  after commit because a Git commit cannot embed its own SHA.

Review used clean detached worktree
`/Users/guilhermecastro/.codex/worktrees/mastarr-f02-review-round4` at exact
product SHA. Independent probe-only tests ran in separate reviewer worktree
`/Users/guilhermecastro/.codex/worktrees/mastarr-f02-probes-round4`; those files
are not part of product or receipt. Reviewer changed no product, F-04, state,
task, module, generated, adapter, UI or shared fixture path.

## Finding

### P1: exact subtitle flags still consume label-shaped media-title tokens

`subtitleFor` derives candidates by comparing normalized video and subtitle
stems (`discovery.go:1020-1030`). It then calculates `Forced` and
`HearingImpaired` from the complete subtitle basename
(`discovery.go:1031-1032`). The correction makes `containsToken` punctuation
aware (`discovery.go:1103-1122`), but does not bind those flag tokens to the
suffix after the selected video stem. `subtitleLanguage` already performs that
binding (`discovery.go:1052-1081`).

Independent synthetic probes observed:

```text
Hi.en.srt + Hi.mkv:
Language:en Forced:false HearingImpaired:true Confidence:exact

Forced.en.srt + Forced.mkv:
Language:en Forced:true HearingImpaired:false Confidence:exact
```

Neither filename has a subtitle label suffix. `Hi` and `Forced` are the exact
video-title stems. Promoting them to flags creates structured metadata that was
not present in the subtitle suffix, and exact confidence hides the ambiguity.
This is the same title-versus-suffix boundary that previously caused language
misclassification.

Required change: for one exact video candidate, derive forced/SDH/HI flags from
the token suffix remaining after that video stem, as language parsing already
does. Keep unmatched or ambiguous label evidence conservative. Add `Hi.en.srt`
and `Forced.en.srt` regressions while retaining bracketed/parenthesized suffix
and IDX/SUB cases.

Disposition: `current_blocker`.

## Round-three correction disposition

- Unrelated sole subtitle: closed. Independent end-to-end grouping probe used
  `Movie/Up.mkv` plus `Movie/captions.en.srt`. One discovery retained the video,
  unresolved subtitle, zero selected video paths, empty language, and one
  `CompanionUnmatchedSubtitle` with zero video paths.
- Bracketed/parenthesized labels: closed for actual suffix labels.
  `Up.[forced].en.srt` remains grouped with `Up.mkv`, language is `en`, forced
  is true and confidence is exact. `Up.(sdh).pt.srt` preserves `pt` and SDH.
- IDX/SUB preservation: closed. Independent bracketed pair probe used
  `Up.[forced].en.idx` and `.sub`; both retained exact association, language
  `en`, forced flag and shared pair ID `Up.[forced].en`, with no unmatched
  companion.
- Prior F-02 behavior remains intact: bounded filesystem/client pagination,
  incomplete-client readiness, connection-scoped evidence, conservative
  movie/anime/episode/season grouping, root namespaces and deep-cloned stored
  observations all pass existing regressions.

## Independent checks

| Check | Result |
| --- | --- |
| Exact product/parent/tree, handoff/parent/tree, clean status, scoped paths and diff hash | Passed. |
| Full correction diff and subtitle grouping/classification call graph | Reviewed independently. |
| Unrelated subtitle grouping probe, `-count=100` | Passed. |
| Bracketed forced, parenthesized SDH and bracketed IDX/SUB probe, `-count=100` | Passed. |
| Requested probes with `-race -count=10` | Passed; no Go data race reported. |
| Label-shaped video-title probe | Failed semantically for both `Hi` and `Forced`, as finding records. |
| `GOWORK=off go test -mod=readonly ./internal/discovery -count=50` | Passed. |
| `GOWORK=off go test -race -mod=readonly ./internal/discovery -count=10` | Passed. |
| Focused vet, formatting and scoped `git diff --check` | Passed. |
| Root `GOWORK=off go test -mod=readonly ./... -count=1`, vet and module verification | Passed. |
| Linux amd64/arm64 CGO-free discovery compile | Passed. |
| `./scripts/check-guardrails.sh --ci` | Passed; generation, API/Vacuum 100/100, lint, architecture, tests, vet and all module verification passed. |

## Acceptance disposition

- A-01: accepted for F-02 contribution. Discovery remains read-only and keeps
  historical orphan/unknown evidence.
- A-02: accepted for F-02 contribution. Enumeration is bounded and incomplete
  filesystem/client evidence remains partial or unknown.
- A-10: accepted for F-02 contribution. Movie, anime absolute, multi-episode
  and season-pack suggestions remain conservative and editable.
- A-11: not accepted. The two requested unmatched/bracketed cases now pass and
  IDX/SUB pairing remains correct, but exact associations can still invent
  forced/HI flags from a label-shaped video title.

No live service, credential, private coordinate, real media, upstream write,
live-media filesystem mutation, release or deployment was used. This receipt
does not approve downstream API/SQLite projection work.
