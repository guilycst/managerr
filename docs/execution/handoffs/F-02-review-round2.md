# F-02 independent review, round two

## Decision

`changes_requested`.

The correction closes the filesystem pagination, incomplete-client readiness,
connection-scoped completion, root grouping/movie-year, and deep-clone defects
from round one. One A-11 blocker remains in the same classification finding:
subtitle language extraction treats short words from the media title as the
language and records the result with exact pairing confidence.

## Review identity and scope

- Reviewer: `/root/x05_reviewer`, independent of product author
  `/root/d02_implementer`.
- Reviewed product:
  `949f4af065aeb8e2dcf13d3ddd7a21ccb593f2ec`; direct parent
  `9840b49d0eb25cdd2fc8c109abf538c2d22420c7`; tree
  `a2d6920168933609275fd764f77eff318c4d7411`.
- Reviewed handoff:
  `75faf5a048859aea02c63c3d2535e74786f92351`; tree
  `2df1ae9e1850973922d9a005c85de78611f09e72`.
- Prior review receipt:
  `30bca5041495eed51ea150b809332403d5c17eb3`.
- Product scope: `internal/discovery/` only.
- Scoped correction diff SHA-256:
  `6e5c0d074c8f3d926efdc32d959957ce6a39915301e22c2e769fdf88e643af5e`.
- Acceptance reviewed: A-01, A-02, A-10, and A-11.

Executable review used the exact handoff checkout and synthetic fixtures only.
The adversarial test was supplied through Go's overlay mechanism, so no product
file was created or edited. No live service, credential, private endpoint,
media, upstream mutation, filesystem mutation, release, or deployment was used.

## Finding

### P1: subtitle language parsing still consumes short title words

`subtitleFor` iterates every token in the complete subtitle basename from left
to right at `internal/discovery/discovery.go:1036-1043`. It excludes the known
labels `forced`, `sdh`, and `hi`, but it does not separate the paired video stem
from suffix metadata. Any two- or three-letter word in the media title therefore
wins before the actual language suffix. When there is one video, the fallback at
lines 1031-1032 pairs it and lines 1048-1051 label the association exact, so the
incorrect structured language is not marked unresolved.

An independent overlay test exercised two common shapes and failed with:

```text
subtitle "Up.forced.en.srt" language="up" want="en"; parsed title token instead of suffix
subtitle "The.Movie.sdh.pt.srt" language="the" want="pt"; parsed title token instead of suffix
```

The committed regression only uses `Movie.sdh.en.srt` at
`internal/discovery/discovery_test.go:500-508`; `Movie` is too long to expose
the basename-prefix error. A-11 requires language and labels to be preserved,
and ambiguity may not be recorded as an exact association. Required correction:
derive subtitle metadata from the suffix remaining after a matched video stem,
or keep the language empty/unresolved when that relationship is ambiguous.
Add short two- and three-letter title regressions, including forced/SDH labels.

Disposition: `current_blocker`.

## Prior finding dispositions

- Filesystem pagination: resolved. Per-directory seen-cursor sets stop
  non-adjacent cycles, and a scan-wide page budget returns partial coverage with
  stable reason codes.
- Incomplete/unknown client evidence: resolved. Per-connection coverage records
  failures and truncation; incomplete coverage and correlated unknown items
  cannot produce complete readiness.
- Connection-scoped completion: resolved. Authoritative item evidence retains
  connection, item ID, hash, state, and completion time. Equal IDs from two
  instances remain distinct.
- Grouping/classification: partially resolved. Root file and directory keys are
  distinct; multiple ambiguous movies remain mixed; ordinary four-digit movie
  years do not become anime. Subtitle label exclusion works only when the title
  contains no earlier language-shaped token, so A-11 remains open.
- MemoryStore aliasing: resolved. Association, completion, provenance,
  stability, and coverage pointers are cloned on commit, scan result, and list.

## Independent checks

| Command or scenario | Result |
| --- | --- |
| Exact product/parent/tree, handoff/tree, clean status, scoped diff and SHA-256 | Passed. |
| `GOWORK=off go test -count=50 ./internal/discovery` | Passed. |
| `GOWORK=off go test -race -count=1 ./internal/discovery` | Passed. |
| `GOWORK=off go vet ./internal/discovery` and `GOWORK=off go mod verify` | Passed; all modules verified. |
| Overlay probe for `Up.forced.en.srt` and `The.Movie.sdh.pt.srt` | Failed as recorded above; both title prefixes were persisted as languages. |
| `GOWORK=off go test -mod=readonly -count=1 ./...` and `GOWORK=off go vet -mod=readonly ./...` | Passed. |
| `./scripts/check-lint.sh` | Passed; zero lint issues and architecture boundaries passed. |
| `python3 scripts/check_planning.py --self-test` and `python3 scripts/check_planning.py` | Passed; 44 tasks and 60 acceptance cases. |
| `./scripts/generate.sh --check` and `./scripts/check-api.sh` | Passed; generated output reproducible and Vacuum reported 100/100. |
| `./scripts/check-guardrails.sh --fast` | Passed. |
| Focused dependency/mutation-port scan | Passed; discovery imports no adapter, storage, UI, or mutation owner. |

## Acceptance disposition

- A-01: accepted for the F-02 contribution. Discovery remains read-only;
  historical orphan and separate filesystem/client observations are retained.
- A-02: accepted for the F-02 contribution. Filesystem/client pagination is
  bounded and incomplete per-source evidence remains partial or unknown.
- A-10: accepted for the F-02 contribution. Ambiguous movie, anime absolute,
  multi-episode, and season-pack suggestions remain conservative and editable.
- A-11: not accepted. Forced/SDH flags and IDX/SUB pairing remain visible, but
  short title words can replace the actual language in an exact association.

No product file, F-04 path, task definition, shared script, module manifest, or
`docs/execution/state.json` was modified by the reviewer.
