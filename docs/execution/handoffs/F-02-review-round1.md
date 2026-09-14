# F-02 independent review, round one

## Decision

`changes_requested`.

The package preserves basic orphan evidence, filesystem stability, unsupported
children, and trash/staging exclusion without importing a mutation port.
Five current blockers remain. Filesystem pagination can run without an
internal bound, incomplete or unknown client evidence can produce a ready
group, aggregate completion loses connection-scoped identity, grouping and
filename classification misstate required media/subtitle cases, and the
reference store exposes mutable pointers into persisted state.

## Review identity and scope

- Reviewer: `/root/d02_reviewer`, independent of product author
  `/root/d02_implementer`.
- Reviewed product:
  `12dfe8c0b39acd00f6f3cb72d3e0ff4ded140ab0`; direct parent
  `ad9ad727b78acc7c7222e7b720a1b158914662c2`; tree
  `2dd8555ead67b73b5a4e8195a3a28cb7527b747d`.
- Reviewed handoff:
  `a37d7f5f26ba6e3dc62f6daff5d9ac37f81d6785`; product parent; tree
  `963634808afdf17ddeda2a78f08c801e2ab04128`.
- Product scope: `internal/discovery/` only.
- Scoped product diff SHA-256:
  `44927b4422dd63ca9afcb049af75f83f57eabc79682ce07cbcc2e81b28523c89`.
- Acceptance reviewed: A-01, A-02, A-10, and A-11.
- Release stage: pre-v0.1.0, target v0.0.1.

Executable review ran in clean reviewer-owned Git worktrees at the exact
product. Adversarial probe files remain untracked in a separate probe worktree.
No live service, credential, private endpoint, media, upstream mutation,
filesystem mutation, release, or deployment was used.

## Findings

### P1: filesystem pagination is not internally bounded

`Options` bounds entries, depth, and client pages at
`internal/discovery/discovery.go:261-280`, but it has no filesystem page bound.
`enumerateDirectory` at lines 486-574 rejects only
`page.NextCursor == cursor`. A non-adjacent cycle such as `a -> b -> a` with
empty pages never reaches `MaxEntries` and never terminates without caller
cancellation.

An independent read-port probe returned complete empty pages with that
two-token cycle. With default scanner options, it issued 97,839 page reads in
25 milliseconds and stopped only when the external context deadline expired:

```text
non-adjacent cursor cycle ran until caller deadline after 97839 pages
```

This contradicts the bounded-scanner contract and the handoff claim that
repeated cursors fail safely. Required correction: keep a per-directory set of
seen opaque cursors or enforce a filesystem page limit. On repetition or limit,
stop and persist partial coverage with a stable reason. Include empty-page
cycles and longer cycles in tests.

Disposition: `current_blocker`.

### P1: incomplete and unknown client evidence can become ready

`clientEvidence` records a global incomplete flag and a review reason at
`internal/discovery/discovery.go:598-683`, but `readinessFor` at lines
1190-1209 considers neither that flag nor `ClientCoverage`. A matched complete
item therefore makes a stable group ready even when its source page is
partial. A two-scan probe produced:

```text
partial client inventory produced ready discovery:
coverage="partial" reasons=[client_inventory_incomplete ambiguous_association]
```

The aggregate merge at lines 1101-1130 also gives unknown state lower priority
than complete. A second probe correlated one unknown and one complete client
item to the same stable group and observed:

```text
completion="complete" readiness="ready" ids=[unknown complete]
```

Finally, reaching `MaxClientItems` at lines 633-640 sets only the global flag.
It does not mark the current source coverage partial or add a reason there. A
probe truncated a two-item complete page at one item and the persisted
per-source coverage still said `complete` with no reason.

These states violate A-02 and the invariant that incomplete evidence is
unknown, never a proof of completion. Required correction: make any relevant
partial/unknown client coverage and any correlated unknown item prevent a
complete aggregate/readiness claim. Mark item-limit truncation on each affected
source coverage. Tests must combine stable filesystem evidence with partial,
failed, truncated, unknown, processing, and complete client evidence.

Disposition: `current_blocker`.

### P1: aggregate client completion is not connection-scoped

`ClientCompletionObservation` has one `ConnectionID` and a raw
`[]string ClientItemIDs` at `internal/discovery/discovery.go:150-159`.
`mergeCompletion` retains the first connection and deduplicates item IDs without
their connection at lines 1101-1108. This cannot represent a group correlated
to multiple download-client instances, even though the provenance slice at
lines 1025-1067 correctly keys evidence by connection, external ID, and hash.

An independent probe correlated `same-id` from `qbt-one` and `qbt-two`.
Provenance retained two records, while completion retained only:

```text
connection="qbt-one" ids=[same-id]
```

Source ordering can therefore change the displayed connection, and identical
external IDs from different instances collapse. This contradicts the type's
own instance-scoped contract and blocks the downstream observation/API mapping.
Required correction: represent completion as connection-scoped item references
or per-item/per-source observations. Aggregate state may be derived from that
lossless evidence, but cannot replace it.

Disposition: `current_blocker`.

### P1: grouping and classification misstate assigned media cases

Several deterministic rules directly contradict the F-02 grouping and
A-10/A-11 behavior:

- `groupKey` at `internal/discovery/discovery.go:752-767` uses a root filename
  stem and a top-level directory name in the same namespace. `Movie.mkv` and
  `Movie/Other.mkv` collapse into one discovery, expanding review scope across
  unrelated filesystem shapes.
- `classifyFiles` at lines 822-839 changes any homogeneous multi-video group
  into `season_pack`. Two ambiguous movie files therefore become a season pack
  instead of a mixed/ambiguous group.
- `animeAbsoluteNumber` at lines 950-967 accepts a bracketed four-digit token.
  The common movie filename `Example Film (2024).mkv` becomes unresolved anime
  rather than an ambiguous movie.
- `subtitleFor` at lines 970-1005 uses the first two- or three-letter token as
  language without excluding label tokens. `Movie.sdh.en.srt` preserves the
  hearing-impaired flag but reports language `sdh`, losing `en`.

All four behaviors were reproduced under the race detector. The outputs were:

```text
root filename stem collided with top-level directory: groups=1
two ambiguous movies classified as "season_pack", want mixed
parenthesized movie year classified as kind="anime" confidence="unresolved"
subtitle labels misclassified: language="sdh" hearingImpaired=true
```

Required correction: give root-file groups and actual directories distinct
stable keys; call a group a season pack only from episode/season evidence;
exclude ordinary year syntax from anime absolute-number inference; and parse
forced/SDH/HI independently from a supported language token. Keep uncertain
cases ambiguous or unresolved and add the exact counterexamples to regression
tests.

Disposition: `current_blocker`.

### P1: MemoryStore returns aliases into persisted state

`MemoryStore` claims concurrency-safe durable-store-shaped semantics at
`internal/discovery/discovery.go:1260-1275`, but its clone helpers are
incomplete. `cloneDiscovery` copies the `Videos` slice at lines 1662-1667
without copying `SeasonNumber` or `AbsoluteNumber`. `cloneDirectoryObservation`
at lines 1655-1659 copies coverage reason codes but leaves `StartedAt` and
`CompletedAt` pointers shared. These helpers are used both on commit and list
at lines 1310-1329 and 1346-1374.

An independent probe mutated `SeasonNumber` through the `ScanResult` returned
after a successful commit. A fresh store read returned the caller's value:

```text
caller mutation changed stored season number to 99
```

Coverage time pointers have the same alias by inspection. A caller can corrupt
history without `Commit`, and concurrent mutation can race with a read despite
the store mutex. Required correction: deep-clone every pointer-bearing field on
input and output, including both association number pointers and coverage time
pointers. Add mutation-after-commit and mutation-after-list tests under `-race`.

Disposition: `current_blocker`.

## Behavior that passed

- The scanner depends only on `ports.FilesystemReadPort`,
  `ports.DownloadInventoryPort`, and its local observation store. No filesystem
  action, upstream write, storage implementation, root adapter, or UI package is
  imported.
- Basic orphan groups retain unknown provenance and nullable client completion
  time while filesystem observation, first-seen, last-seen, and modified times
  remain distinct.
- Two exact observations with sufficient spacing are required for stable file
  evidence. Changed identity/content/size and removed files prevent stability.
- Partial/unknown filesystem coverage does not retire unseen groups. Complete
  absence retires the current group while retaining its history.
- Unsupported child evidence is path-scoped, persisted, and makes filesystem
  coverage partial. Supported subtitle containers and complete IDX/SUB pairs
  remain visible.
- Default component-aware `.mastarr-trash` and `.mastarr-staging` prefixes are
  excluded from observations and grouping.
- Successful repeated scans preserve group ID, first-seen time, and manifest
  revision through the observation-store seam.

## Independent checks

| Command or scenario | Result |
| --- | --- |
| Exact product, parent, tree, handoff ancestry, clean status, scoped diff and hash | Passed. |
| Existing discovery tests, `GOWORK=off go test -count=50 ./internal/discovery` | Passed. |
| Existing discovery tests, `GOWORK=off go test -race -count=10 ./internal/discovery` | Passed. |
| Filesystem two-token empty-page cursor-cycle probe | Failed as finding one records; 97,839 reads before external deadline. |
| Partial/truncated/unknown client completion and readiness probes | Failed as finding two records. |
| Duplicate external ID across two client connections probe | Failed as finding three records. |
| Root file/directory collision, mixed movies, movie year, and subtitle-label probes | Failed as finding four records. |
| Store mutation-after-commit clone probe | Failed as finding five records. |
| `GOWORK=off go test -race ./...` | Passed, including storage in 204.475 seconds. |
| `GOWORK=off go test -mod=readonly ./... -count=1`, `GOWORK=off go vet ./...`, and `GOWORK=off go mod verify` | Passed; all modules verified. |
| Linux amd64 and arm64 CGO-free internal compile checks with `-mod=readonly` | Passed. |
| `./scripts/check-guardrails.sh --ci` | Passed; generation, Vacuum 100/100, architecture, format, lint, root/UI/tools/client tests, vet, and module verification passed. |
| `./scripts/generate.sh --check` and `./scripts/check-api.sh` | Passed; generated output reproducible and Vacuum reported 100/100. |
| `./scripts/check-lint.sh`, architecture, and planning self/full checks | Passed; 0 lint issues and 44 tasks/60 acceptance cases. |
| Focused public-data and mutation-port scan | Passed; no private data, filesystem/upstream write port, storage implementation, or UI dependency found. |

## Non-blocking follow-up register

- `v0.1.0_candidate`: define whether aggregate `CompletedAt` for a group means
  earliest or latest completion across several correlated items. Individual
  provenance timestamps are currently preserved, so this does not need to
  delay the scoped correction.
- `v0.1.0_candidate`: strengthen `Discovery.Validate` so association paths,
  manifest revisions, numeric episode fields, and timestamp ordering are
  checked before the downstream SQLite adapter relies on this boundary.
- `post_v0.1.0`: add an explicit bounded retention policy for raw in-memory or
  SQLite directory-observation history when the metadata janitor lane is
  implemented.

## Acceptance disposition

- A-01: F-02 contribution accepted for read-only scanning, historical orphan
  visibility, unknown client/date behavior, and separate filesystem times.
- A-02: not accepted. Non-adjacent filesystem cursor cycles are unbounded,
  client item truncation records false complete per-source coverage, and
  partial/unknown client evidence can produce ready state.
- A-10: not accepted. Root group identity can merge unrelated shapes, multiple
  ambiguous movies become a season pack, and a common parenthesized movie year
  becomes anime.
- A-11: not accepted. IDX/SUB and common suffixes remain visible, but SDH/HI
  label placement can be persisted as the language and lose the actual
  language token.

No product file, task definition, shared script, module manifest,
`docs/execution/state.json`, or F-04 path was modified by the reviewer.
