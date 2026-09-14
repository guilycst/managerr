# F-04 correction round one handoff

## Assignment

- Task ID and title: F-04 correction round one, close the copy and hardlink
  placement safety findings from the independent review.
- Owner/agent: `/root/x05_implementer`; independent reviewer: coordinator to
  assign `/root/f04_reviewer` after this checkpoint.
- Dispatch base: `29b4a48e38632c9056db08e9f525e40309ab73d8`.
- Shared-checkout ownership: `main`; the coordinator owns integration and
  `docs/execution/state.json`. This lane changed only
  `internal/filesystem/placement/` and this handoff.
- Reviewed source product: `1733a0998e86bf4c71250fab11e17b41857bbc85`.
  The original `F-04.md` recorded `1733a0903cd3d963b1cb312f7c5962af40954d1f`,
  which is not the product object; this correction handoff uses the exact
  reviewed SHA.
- Required acceptance cases: A-18, A-19, A-20, A-22 and A-59.

## Correction result

The correction closes the three P1 findings while preserving the frozen
copy/hardlink contract.

- `ReconcileHardlink` opens the current approved source before comparing
  objects. It requires a regular file and matches the approved source identity,
  size and mode. A replaced or unavailable source remains unresolved and
  cannot be reported as `already_satisfied`, even when the replacement is
  hardlinked to the destination.
- Copy stages are created exclusively and their descriptor identity is checked
  before publication. Every failure and cancellation cleanup closes the owned
  descriptor and leaves the stage pathname for the durable janitor; it never
  unlinks a mutable pathname after an identity check. A deterministic race
  replaces the stage after the check and proves the replacement remains intact.
- Linux publication is descriptor-bound: the operation-created stage descriptor
  is compared again and linked with `AT_EMPTY_PATH`, with a descriptor-bound
  `/proc/self/fd` fallback for kernels or permissions that reject the primary
  primitive. Darwin writes fail closed before filesystem mutation because no
  descriptor-bound hardlink primitive is available. This prevents pathname
  substitution between stage verification and publication.
- Every successful `mkdirat` synchronizes its containing parent before the
  traversal advances. The final destination parent is synchronized after file
  publication, and directory synchronization failures are classified as
  publication uncertainty before an applied journal result. Copy and hardlink
  tests assert nested parent ordering and injected synchronization failure.
- The existing exact-plan preflight, root confinement, no-follow traversal,
  exclusive staging, no-replace publication, source/destination read-back,
  per-file effects, cancellation semantics, journal uncertainty and no-copy
  hardlink rule remain in force. Move, rename, trash and delete remain outside
  this lane.

## Product commits

- `149ec74d674d542a93acfcf900bb7344b64675d2` — close source, stage ownership,
  descriptor publication, and directory synchronization gaps.
- `3e8f2f52a27b95c4f2cb4f6fd7abbf22496fe9bb` — classify final directory sync
  failure as `ErrPublicationUnknown`.

The review target for this correction is the second product commit,
`3e8f2f52a27b95c4f2cb4f6fd7abbf22496fe9bb`. The handoff commit SHA is recorded
after commit because Git cannot include its own object ID in its contents.

## Verification

| Command or scenario | Result | Evidence |
| --- | --- | --- |
| `GOWORK=off go test -mod=readonly -count=1 ./internal/filesystem/placement` | Passed on host, exit 0 | placement package tests |
| `GOWORK=off go test -mod=readonly -race -count=3 ./internal/filesystem/placement` | Passed on host, exit 0 | placement package tests |
| `GOWORK=off go vet -mod=readonly ./internal/filesystem/placement` | Passed on host, exit 0 | placement package |
| Linux container placement tests | Passed, exit 0 | `golang:1.27`, synthetic temporary roots |
| Linux container placement race tests | Passed, exit 0 | `-race -count=3` |
| Linux stage-substitution regressions | Passed, exit 0 | `TestCopy(PreservesStageReplacementBetweenCheckAndCleanup\|CleanupPreservesReplacementStage)` with repeated runs |
| Linux amd64 and arm64 CGO-free placement compile | Passed, exit 0 | `GOOS=linux GOARCH={amd64,arm64} CGO_ENABLED=0 ... -run '^$' -exec=true` |
| Darwin amd64 and arm64 CGO-free placement compile | Passed, exit 0 | `GOOS=darwin GOARCH={amd64,arm64} CGO_ENABLED=0 ... -run '^$' -exec=true` |
| Root, UI, tools, qBittorrent and NZBGet module tests | UI/tools/qBittorrent/NZBGet passed; root full run is currently blocked by concurrent unstaged discovery work at `internal/discovery/discovery.go:433` (`cloneCoverage` undefined) | module matrix |
| Root and nested module `go vet`, `go mod verify` | Nested vet and all module verification passed; root vet has the same concurrent discovery compile blocker | module matrix |
| `./scripts/generate.sh --check` and `./scripts/check-api.sh` | Passed before the concurrent guardrail run; the concurrent invocation contended on Git's temporary index lock | generation and API checks |
| `python3 scripts/check-architecture.py`, `./scripts/check-lint.sh --architecture-only`, `python3 scripts/check_planning.py` | Passed; planning reports 44 tasks and 60 acceptance cases | architecture, planning and static guardrails |
| `./scripts/check-lint.sh` and `./scripts/check-guardrails.sh --ci` | Blocked by the same concurrent unstaged discovery compile/format work; no placement finding | full lint and CI guardrail commands |
| `TestReconcileHardlinkRequiresApprovedSourceIdentity` | Passed | replacement source inode/size/mode regression |
| `TestCopyCleanupPreservesReplacementStage` | Passed on Linux; explicitly skips Darwin write path | cancellation substitution regression |
| `TestCopyPreservesStageReplacementBetweenCheckAndCleanup` | Passed on Linux; explicitly skips Darwin write path | check-to-cleanup substitution regression |
| `TestCopyAndHardlinkSyncCreatedDirectoryParents` | Passed | injected nested parent sync ordering |
| `TestCopyDirectorySyncFailureIsUncertainBeforeFilePublication` | Passed | injected sync failure and no destination publication |

All fixtures use synthetic bytes and temporary roots. No credentials, private
coordinates, live services or real media were accessed. A native crash,
disk-full or cross-device mount was not available; the implementation keeps
the explicit error and uncertainty paths. Darwin write support is intentionally
blocked until a descriptor-bound publication primitive is available.

## Review and resume

- Independent review receipt for the prior product: `337d61b73cc7331cec724a7b9cbcb425fc22e523`.
- Findings addressed: source identity binding, stage replacement safety,
  directory parent durability, and the prior handoff SHA typo.
- Final reviewer decision: pending independent review of product
  `3e8f2f52a27b95c4f2cb4f6fd7abbf22496fe9bb`.
- Coordinator action: record both product SHAs and this handoff SHA in
  `docs/execution/state.json`, then dispatch the independent reviewer. The
  current full-root blocker is unrelated concurrent discovery work and must be
  resolved by its owner before a clean repository guardrail run.
- No live mutation or deployment occurred. The only remaining F-04 design
  limitation is fail-closed Darwin writes and the lack of native crash,
  disk-full and cross-device fault injection in this environment.
