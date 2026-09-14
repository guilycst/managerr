# F-04 correction round two handoff

## Assignment

- Task ID and title: F-04 correction round two, close the remaining placement
  lifecycle and publication findings from the second independent review.
- Owner/agent: `/root/x05_implementer`; independent reviewer:
  `/root/x05_reviewer`.
- State checkpoint: `f71bd49bc32d67650f820fe6cc46e4736bae7556`.
- Product base: `3e8f2f52a27b95c4f2cb4f6fd7abbf22496fe9bb`.
- Shared-checkout ownership: `main`; the coordinator owns integration and
  `docs/execution/state.json`. This lane changed only
  `internal/filesystem/placement/` and this handoff.
- Required acceptance cases: A-18, A-19, A-20, A-22 and A-59.
- Review source: `docs/execution/handoffs/F-04-review-round2.md`, receipt
  `9c39e1d7e024b06548c81ab994af608327463afc`.

## Correction result

The product closes the three Linux P1s from round two and repairs the deferred
fallback build defect.

- Linux copy staging now uses `O_TMPFILE` in the already-open destination
  directory. The stage has no directory entry while being populated, and
  `AT_EMPTY_PATH` publishes that exact descriptor with no replace. Closing the
  descriptor after publication leaves only the approved destination link;
  closing it before publication reclaims the anonymous inode. An unsupported
  or unavailable anonymous-staging primitive returns `ErrUnsupported` before
  the copy can report `applied`; there is no named-stage fallback.
- Copy cancellation and publication-collision paths close only the anonymous
  staging descriptor. An unrelated file using the old stage-name convention is
  never inspected, removed or treated as operation-owned. The context is
  checked again after the test seam and before publication.
- Created destination directories are preserved on every error and
  cancellation path. The old pathname-based empty-directory cleanup was
  removed because no portable inode-conditional directory unlink exists. The
  root-relative candidates remain available to the future durable janitor;
  this lane does not claim to wire that executor lifecycle.
- Hardlink publication now uses the already-open, manifest-validated source
  descriptor with Linux `linkat(AT_EMPTY_PATH)`. A source pathname exchange
  after validation therefore links the approved inode, while the subsequent
  source read-back reports the exchange as uncertain. Targets without a
  descriptor-bound primitive fail closed, and no hardlink error falls back to
  copy.
- `fs_other.go` no longer contains a shadowed stage variable or pathname-based
  write helper. Non-Linux/non-Darwin placement writes remain unsupported, but
  the fallback package compiles cleanly for its target platforms.
- Existing exact-plan preflight, root confinement, no-follow traversal,
  digest/source stability checks, no-replace publication, parent fsync ordering,
  read-back, per-file effects, journal uncertainty and cancellation behavior
  remain in force. Move, rename, trash and delete remain outside F-04.

## Product checkpoint

- Product commit:
  `2621343dcd3b80c259f5cec808dadb7bfbb0434d` (`fix(filesystem): close
  placement cleanup races`).
- The product commit includes the anonymous staging implementation, descriptor
  bound hardlink publication, replacement-safe directory handling, fallback
  compilation repair and synthetic regressions. The handoff commit SHA is
  recorded after commit because Git cannot include its own object ID in its
  contents.

## Verification

| Command or scenario | Result | Evidence |
| --- | --- | --- |
| `GOWORK=off go test -mod=readonly -count=1 ./internal/filesystem/placement` | Passed on host, exit 0 | placement tests; Darwin write tests skip at the explicit capability gate |
| `GOWORK=off go test -mod=readonly -race -count=3 ./internal/filesystem/placement` | Passed on host, exit 0 | placement race tests |
| `GOWORK=off go vet -mod=readonly ./internal/filesystem/placement` | Passed, exit 0 | placement package |
| Linux container placement tests | Passed, exit 0 | `golang:1.27`, synthetic temporary roots |
| Linux container placement race tests | Passed, exit 0 | `-race -count=3` |
| Linux container placement vet | Passed, exit 0 | `golang:1.27` |
| `GOWORK=off go test -mod=readonly -count=1 ./...` in root | Passed, exit 0 | root module matrix |
| Root `go vet` and `go mod verify` | Passed, exit 0 | root module matrix |
| UI, tools, qBittorrent and NZBGet tests | Passed in each module, exit 0 | standalone nested-module matrix |
| UI, tools, qBittorrent and NZBGet vet plus `go mod verify` | Passed in each module, exit 0 | standalone nested-module matrix |
| Linux amd64/arm64 CGO-free placement compile | Passed, exit 0 | `GOOS=linux GOARCH={amd64,arm64} ... -run '^$' -exec=true` |
| Darwin amd64/arm64 CGO-free placement compile | Passed, exit 0 | `GOOS=darwin GOARCH={amd64,arm64} ... -run '^$' -exec=true` |
| Windows amd64 and FreeBSD amd64 CGO-free placement compile | Passed, exit 0 | `GOOS={windows,freebsd} GOARCH=amd64 ... -run '^$' -exec=true` |
| `./scripts/generate.sh --check` | Passed; generated output is reproducible | generation check |
| `./scripts/check-api.sh` | Passed; Vacuum quality score 100/100 with zero warnings/errors | bundled API and Vacuum check |
| `./scripts/check-lint.sh` | Passed; lint and import boundaries clean | full lint/architecture check |
| `python3 scripts/check-architecture.py` | Passed | architecture check |
| `python3 scripts/check_planning.py` | Passed; 44 tasks, 60 acceptance cases and local links resolve | planning check |
| `./scripts/check-guardrails.sh --ci` | Passed; generation, staged generation, API/Vacuum, lint, tests, vet and module verification passed | CI-equivalent guardrail check |
| pre-commit hook on product commit | Passed; fast deterministic guardrails passed | `.githooks/pre-commit` |
| `TestCopyPublishesExactDigestAndRecordsEachFile` | Passed on Linux; final stage-name alias is absent after applied journal result | `placement_test.go` |
| `TestCopyLeavesNoNamedStagingAlias` | Passed on Linux; an unrelated old-convention name remains untouched and is not aliased to the destination | `placement_test.go` |
| `TestCopyCancellationReclaimsAnonymousStage` | Passed on Linux; cancellation closes anonymous staging and preserves an unrelated stage-name file | `placement_test.go` |
| `TestCopyDoesNotTouchUnrelatedStagingNameOnPublicationCollision` | Passed on Linux; destination collision and unrelated stage-name file are preserved | `placement_test.go` |
| `TestCreatedDirectoryCleanupPreservesReplacement` | Passed on Linux; a replacement empty directory survives injected sync failure and error cleanup | `placement_test.go` |
| `TestHardlinkPublishesApprovedSourceDescriptorAfterPathExchange` | Passed on Linux; destination names the original approved inode and source exchange is reported as `ErrSourceChanged`/uncertain | `placement_test.go` |

The fixtures use synthetic bytes and temporary roots only. No credentials,
private coordinates, live services, real media or destructive external action
was used. Native crash, disk-full and cross-device faults remain unavailable in
this environment; explicit uncertainty, `EXDEV` and unsupported paths remain
fail-closed.

## Review and resume

- Round-two blockers addressed: successful-copy stage alias, replacement-safe
  created-directory cleanup, hardlink source descriptor race and the fallback
  compile defect.
- Independent review decision for this product: pending
  `/root/x05_reviewer` review from product
  `2621343dcd3b80c259f5cec808dadb7bfbb0434d`.
- Coordinator action: record the product and handoff SHAs in
  `docs/execution/state.json`, then dispatch the independent review. Do not
  enable a durable janitor or broaden filesystem actions from this lane without
  a separately reviewed executor contract.
- Remaining F-04 boundary: Linux anonymous staging and descriptor-bound
  publication are evidence-backed. Darwin and other target writes remain
  explicitly unsupported. A future janitor must own any durable cleanup of
  created directories and operation recovery; no pathname-based cleanup is
  performed here.
