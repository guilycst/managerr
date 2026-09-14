# X-07 independent review, round two

## Decision

`changes_requested`.

Correction fully closes operation-specific capability gating. Relocation now
distinguishes final content path from qBittorrent's containing download
location, requires destination-vacancy evidence, and reads back every payload
path. One P1 safety defect remains: it dispatches `setLocation` when requested
final content basename cannot be produced by that operation. The write moves
payload to a different path than approved, then reports `outcome_unknown`.

## Review identity and scope

- Reviewer: `/root/x05_reviewer`, independent of correction author
  `/root/x05_implementer`.
- Reviewed correction product:
  `ac8d0a3b61d5ad6b3bb16e67d5bd1d25359859df`; direct parent
  `3e86f117155753e7165f38e0f9290bdcc78bc322`; product tree
  `e725cfa21f0ebc2cc62057062f7489287960ba83`.
- Reviewed handoff:
  `24c4b7e10cbb1b97237d40f8a7f9435e0daa65ef`; direct parent
  `66527605f437c97886c0234c71e423ef7575350b`; handoff tree
  `46c3bd718432531e687184126beecb153cafd265`.
- Round-one product:
  `a51e51945bde229265163707261483fa7edce06a`; round-one review receipt:
  `2c26b06fe083372432889901f84d98246ab761be`.
- Correction product scope: `internal/adapters/qbittorrent/control/control.go`
  and `internal/adapters/qbittorrent/control/control_test.go`.
- Correction commit changes only those two product files. Scoped correction
  diff SHA-256: `021e6169f96b691a232ef20a7e2810a5e6d355b733581d864907953db1468792`.
- Product-to-handoff chain also contains unrelated D-03 configuration and
  execution documentation. Those paths were excluded from this product review.
- Acceptance reviewed: A-28, A-29, A-30, A-31 and A-33.
- Review receipt checkpoint: commit containing this file; exact SHA is reported
  to coordinator after commit because a Git commit cannot embed its own SHA.

Review ran in clean detached worktree
`/Users/guilhermecastro/.codex/worktrees/managerr-x07-review-r2` at exact
handoff commit. Reviewer did not edit product paths, execution state, task
definitions, client modules, shared scripts or fixtures.

## Round-one finding closure

### Closed: independent control capability gates

`ControlCapabilities` now carries distinct Stop, Relocate, RenameFile,
RenameFolder and Remove evidence. Each control method passes its matching
capability to `writeAllowed`, and `Capabilities` reports five observations.
Synthetic tests show Stop-only evidence permits one Stop call while relocation,
both rename methods and removal remain blocked with zero native calls. This
closes round-one aggregate-gate finding.

### Partially closed: relocation path semantics and collision checks

`Relocate` now treats `domain.FileTarget` as final content path and sends
`path.Dir(desiredRemote)` to native `SetLocation`. Tests model qBittorrent by
preserving current content basename below supplied location. Both single-file
and multi-file cases assert containing-directory dispatch, exact content-path
read-back and every payload file. `DestinationChecker` rejects occupied targets
before dispatch and keeps relocation unsupported when vacancy evidence is
missing. These changes close round-one location-vs-content and vacancy defects
for destinations that preserve native content layout.

## Finding

### P1: relocation dispatches an unrepresentable final content path

`Relocate` maps approved final target to `desiredRemote`, then unconditionally
derives native location as `path.Dir(desiredRemote)` and dispatches
`SetLocation` (`control.go:310-361`). It never proves that qBittorrent can
produce requested final content path from current content layout. The synthetic
native model correctly preserves current basename:
`path.Join(location, path.Base(currentContentPath))`
(`control_test.go:96-108`). Existing success cases request same basename for
source and destination (`control_test.go:619-682`), so they do not cover this
boundary.

Independent probe used current content
`/downloads/Synthetic Film.mkv` and approved final target
`/downloads/relocated/Renamed Film.mkv`. Safe behavior is pre-write refusal
because `setLocation` can only produce
`/downloads/relocated/Synthetic Film.mkv`. Actual behavior returned
`outcome_unknown` after exactly one `SetLocation` call:

```text
--- FAIL: TestReviewRelocateRejectsBasenameChangeBeforeWrite (0.00s)
    control_test.go:828: setLocation calls = 1, want zero when final basename requires a separate rename
```

Failure mode: payload is moved to a path different from approved final target.
Exact read-back prevents a false success, but it happens after partial external
effect and cannot restore previous location. Same assumption is unsafe for any
native layout whose preserved content suffix is not exactly one basename.

Required change: prove destination is representable before vacancy check and
dispatch. Minimum safe contract rejects a final target whose basename differs
from observed content basename, requiring a separate reviewed rename action.
More complete support should derive preserved content suffix from observed
`SavePath` and `ContentPath`, compute native `location`, and reject any target
that cannot preserve that suffix. Add synthetic tests for changed basename and
a multi-file layout whose content suffix is not one path component. All
rejections must make zero native calls.

Disposition: `current_blocker`.

## Preserved behavior

- Stop, relocate, file rename, folder rename and remove use independent
  versioned capability evidence.
- Relocation requires a stopped, stable whole-torrent scope and explicit
  vacancy evidence. Occupied and unobservable destinations produce zero writes.
- Representable single-file and multi-file moves pass containing-directory and
  full-payload read-back.
- `stalledUP` with zero upload speed remains active. A lost Stop response gets
  detached bounded read-back; external resume blocks removal.
- Remove requires stopped state, sends literal `deleteFiles=false`, treats an
  absent record as already satisfied, and never claims payload deletion.
- File and folder rename retain exact observed scope and collision checks.
- Every dispatched write gets one read-back. Unknown effects remain unknown and
  no method blindly resubmits a mutation.
- Native DTOs and errors remain translated inside adapter boundary. Generated
  qBittorrent types do not reach Mastarr domain or ports.
- Nested qBittorrent client remains read-only. Runtime write capabilities remain
  blocked pending separate versioned contract, generated transport and review.

## Independent checks

| Check | Result |
| --- | --- |
| Exact product/handoff identity, ancestry, tree and correction-owned scope | Passed. |
| Correction diff and every new/changed control test | Inspected independently. |
| `GOWORK=off go test -mod=readonly -count=50 ./internal/adapters/qbittorrent/control` | Passed. |
| `GOWORK=off go test -race -mod=readonly -count=10 ./internal/adapters/qbittorrent/control` | Passed. |
| `GOWORK=off go vet -mod=readonly ./internal/adapters/qbittorrent/control` | Passed. |
| Focused pinned `golangci-lint` for control package | Passed; 0 issues. |
| Root `GOWORK=off go mod verify` | Passed. |
| Nested qBittorrent module verify, test, race test and vet with `GOWORK=off` | Passed. |
| Linux amd64 and arm64 control-package test compilation | Passed. |
| `python3 scripts/check-architecture.py` | Passed. |
| `./scripts/generate.sh --check` | Passed. |
| `gofmt -l` for correction-owned Go files and `git diff --check` | Passed. |
| `./scripts/check-guardrails.sh --fast` | Passed. |
| `./scripts/check-guardrails.sh --ci` | Blocked in unrelated integrated D-03 root module: `go: updates to go.mod needed; to update it: go mod tidy`. This is a checkpoint-wide blocker, not caused by X-07 product files. |
| Independent changed-basename synthetic probe from archive copy | Failed safely expected precondition: actual `outcome_unknown` and one `SetLocation` dispatch. |
| Live qBittorrent, credentials, private coordinates and media | Intentionally not used. |

## Acceptance disposition

- A-28: accepted for X-07 adapter contribution. Explicit state, post-write
  reconciliation, external-resume refusal and no blind Stop retry remain intact.
- A-29: accepted for adapter mechanics. Whole-torrent Stop and
  metadata-only removal behavior remain exact; capability evidence is now
  operation-specific.
- A-30: accepted. Missing torrent record remains distinct from payload state;
  no automatic re-add or resume exists.
- A-31: not accepted. Collision, exact read-back and scope checks pass for
  representable moves and renames, but relocation still dispatches when approved
  final path requires a separate basename/layout mutation.
- A-33: partially accepted. Read-back and zero blind resubmission pass, but
  unrepresentable relocation causes a wrong partial effect before returning
  unknown.

G-01 remains open. No Arr writes, runtime qBittorrent writes, release,
deployment or live media mutation are approved by this receipt.
