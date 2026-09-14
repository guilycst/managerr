# F-02 independent review, round five

## Decision

`approved`.

The correction closes the remaining A-11 title-versus-label defect. Forced,
SDH, and HI flags are derived only from the suffix after one exact matched video
stem. `Hi.en.srt` beside `Hi.mkv` and `Forced.en.srt` beside `Forced.mkv` retain
language `en` without invented flags. No regression was found in the prior F-02
pagination, readiness, grouping, subtitle, or persistence behavior.

## Review identity and scope

- Reviewer: `/root/x05_reviewer`, independent of product author
  `/root/d02_implementer`.
- Reviewed product:
  `1a44ea97962f40820b042320970dc2f5d1571206`; direct parent
  `4899e645fd456676d9bf6cb0a4a29ef6e2e75c59`; tree
  `544a90e42a986468a80550bdddbbcd25b69431a0`.
- Reviewed handoff:
  `67e5e2d5fb5a86a02fb21009f8b25d301a707b19`; direct product parent; tree
  `87905c6c2493ca54a23e3f09ab5c4a5d09d116a0`.
- Prior review receipt:
  `49a2e72430acba544d77ecb34121624cbb73bb62`, integrated by
  `2963240b6a833e60c8a42c7a8025d60492b02fd9`.
- Product scope: `internal/discovery/` only.
- Scoped correction diff SHA-256, from prior product
  `05dcd107d83b77632a0212d5faec31c65e3d4e13`:
  `99ed1ae373b66c00330a2a3e10a9eac602378348bd6fa94056fd81feac5c0e96`.
- Acceptance reviewed: A-01, A-02, A-10, and A-11.

Review used a clean exact-handoff worktree and synthetic fixtures only. The
independent adversarial table was supplied through a Go overlay, so no product
file was created or edited. No live service, credential, private endpoint,
media, upstream mutation, filesystem mutation, release, or deployment was used.

## Finding disposition

### Label-shaped media titles: closed

`subtitleFor` first establishes candidate videos at
`internal/discovery/discovery.go:1020-1030`. It obtains the suffix only when
there is exactly one candidate at lines 1031-1034, then derives forced and
hearing-impaired flags exclusively from that suffix at lines 1035-1036.
Language already uses the same exact stem/suffix relationship at lines
1056-1085. Token comparison remains punctuation-aware at lines 1088-1125.

Independent probes confirmed:

```text
Hi.en.srt + Hi.mkv       -> language=en, forced=false, hearingImpaired=false
Forced.en.srt + Forced.mkv -> language=en, forced=false, hearingImpaired=false
```

Actual suffix labels still behave as intended:

```text
Up.forced.en.srt         -> language=en, forced=true
The.Movie.sdh.pt.srt     -> language=pt, hearingImpaired=true
Up.[forced].en.srt       -> language=en, forced=true
Up.(sdh).pt.srt          -> language=pt, hearingImpaired=true
```

The producer regressions at `internal/discovery/discovery_test.go:511-570` bind
the same cases to exact relationship state. The independent probe additionally
combined a label-bearing ambiguous relationship and a label-bearing unmatched
subtitle; both retained empty language/flags until an exact video relationship
exists.

Disposition: `closed`.

## Confirmed retained behavior

- Two matching videos remain ambiguous with no selected suffix metadata.
- An unrelated sole subtitle remains unresolved with no selected `VideoPaths`,
  no inferred flags/language, and an explicit `CompanionUnmatchedSubtitle`.
- Complete bracketed IDX/SUB files remain present with one shared pair ID,
  exact matched video, explicit language, and forced label.
- Root video/subtitle grouping remains one review unit without scope expansion.
- Non-adjacent filesystem/client cursor cycles and page/item limits remain
  bounded; incomplete evidence cannot claim readiness.
- Connection-scoped client item identity, conservative movie/anime/episode and
  season-pack suggestions, and deep-cloned observation/store evidence retain
  their earlier regression coverage.
- Discovery remains read-only and imports no adapter, storage, UI, or mutation
  owner.

## Independent checks

| Check | Result |
| --- | --- |
| Exact product/parent/tree, handoff ancestry/tree, clean status, scoped paths and diff hash | Passed. |
| Independent title-shaped, true suffix, bracketed/parenthesized, ambiguous, unmatched, IDX/SUB and root-group table, `-race -count=10` | Passed. |
| `GOWORK=off go test -mod=readonly -count=50 ./internal/discovery` | Passed. |
| `GOWORK=off go test -mod=readonly -race -count=10 ./internal/discovery` | Passed. |
| Focused `go vet` and `go mod verify` | Passed; all modules verified. |
| Root `GOWORK=off go test -mod=readonly -count=1 ./...` and `go vet` | Passed. |
| Linux amd64 and arm64 CGO-free discovery compile | Passed. |
| Lint, import-boundary and planning self/full checks | Passed; zero lint issues and 44 tasks/60 acceptance cases. |
| `./scripts/check-guardrails.sh --ci` | Passed; generation, Vacuum 100/100, lint, architecture, all module tests/vet and verification passed. |

## Acceptance disposition

- A-01: accepted for F-02. Read-only directory/client discovery, historical
  orphan visibility, and separate filesystem/client timestamps remain intact.
- A-02: accepted for F-02. Pagination is bounded and incomplete per-source
  evidence remains partial or unknown.
- A-10: accepted for F-02. Ambiguous movie, anime absolute, multi-episode, and
  season-pack suggestions remain conservative and editable.
- A-11: accepted for F-02. Exact suffix languages and labels, ambiguity,
  unmatched companions, and IDX/SUB pair identity are preserved without title
  tokens becoming flags or unrelated scope being selected.

This approval covers only the reviewed F-02 package contribution. Downstream
API and SQLite projections remain separately owned and reviewed. No product
file, F-05 path, task definition, shared script, module manifest, or
`docs/execution/state.json` was modified by the reviewer.
