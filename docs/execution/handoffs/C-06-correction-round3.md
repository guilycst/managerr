# C-06 correction round three: fresh-cache aggregate verification

## Assignment

- Task ID and title: C-06 correction round three, verify the staged-contract
  guardrail with a fresh Go build cache and isolate the storage race timing.
- Owner and independent reviewer: `/root/x05_implementer`; independent
  reviewer `/root/x05_reviewer`.
- Base commit: `8ef25aec0b601256242b10e67cfd4bca36816b63`.
- Prior independent review receipt:
  `a6c5eabb396794d8c8a69555686fbbdeb106f5f7`.
- Branch/worktree: shared checkout on `main`; the coordinator owns integration
  and `docs/execution/state.json`.
- Owned path: `docs/execution/handoffs/C-06-correction-round3.md` only.
  No product script, contract, generated output or state file was changed.
- Verification target: distinguish the prior clean-cache aggregate timeout from
  the root storage race without weakening the aggregate timeout or guardrails.

## Verification result

The fresh-cache aggregate did not complete within the required 360-second
bound. It exited 124 at 360.0 seconds. This is not a storage test failure:
the aggregate log records `internal/storage` completing successfully in
201.281 seconds, followed by the remaining root packages and the beginning of
the tools module checks. The log ended during the tools-module continuation,
before the aggregate emitted its final module-verification and cross-build
results. The exact later subcommand is not isolated because the aggregate
script does not print phase markers around `go vet` and `go mod verify`; the
safe evidence is that the timeout occurred after storage completed and before
the full suite completed.

The independent direct storage run passed with a separate fresh Go cache in
209.2 seconds elapsed; the package reported 196.341 seconds. No storage code
or aggregate timeout was changed in response. Verification remains pending
until the fresh-cache full aggregate completes within its prescribed bound or
an explicitly owned follow-up isolates the later module phase.

## Exact checks

All commands used synthetic repository code and local module inputs. No live
service, credential, private coordinate, inventory or media data was used.
Temporary cache and log paths are intentionally omitted from this public
handoff.

| Command or scenario | Result / exit status | Evidence |
| --- | --- | --- |
| `GOCACHE=$(mktemp -d) GOWORK=off ./scripts/check-guardrails.sh --ci`, bounded by a 360-second process-group timeout | **Timed out**, exit 124 at 360.0s; `internal/storage` completed with `ok ... 201.281s`; later tools-module output was the last captured phase; no final aggregate success claimed | fresh-cache aggregate log |
| `GOCACHE=$(mktemp -d) GOWORK=off go test -race -count=1 -mod=readonly ./internal/storage`, bounded by 360 seconds | Passed, exit 0 in 209.2s elapsed; package result `ok ... 196.341s` | independent uncached storage race log |
| Product-tree status and diff checks | Passed; checkout remained clean before and after verification, with no product or state edits | Git |

The preceding product and handoff already record the staged-only invalid
contract rejection, six-document staged Vacuum validation, explicit 18-pair
matrix, generation, lint, architecture, planning and warmed-cache aggregate
checks. This verification-only round did not rerun or modify those paths.

## Review and integration

- Reviewed product remains
  `5e53134e40322b512efef1865d78da2da6368e5b`; no product correction was
  needed because the timeout did not identify a C-06 script defect.
- The prior review’s staged-candidate finding is addressed by the product
  already recorded above. This round addresses only the requested fresh-cache
  evidence and records the remaining timeout as a blocker.
- Coordinator must record this handoff SHA and the verification result in
  `docs/execution/state.json`; this worker did not edit that file.
- Final reviewer decision: pending fresh-cache aggregate completion.

## Resume checkpoint

- Current state: staged contract validation remains implemented and previously
  passes its negative candidate regression; direct uncached storage race passes
  independently. The required fresh-cache aggregate remains incomplete after
  a 360-second timeout in a post-storage module phase.
- Active process or agent ownership, sanitized: no aggregate or storage process
  remains; the temporary verification processes were terminated at the bound.
- Next safe action: investigate or separately bound the later tools/module
  phase, then rerun the exact fresh-cache aggregate without weakening its
  timeout or skipping checks.
- Blocker and exact evidence needed: a successful
  `GOCACHE=$(mktemp -d) GOWORK=off ./scripts/check-guardrails.sh --ci` within
  360 seconds, or an explicitly reviewed phase-level diagnosis and correction
  outside this verification-only lane.
- No conflicting writes or unknown files removed: only this handoff is owned;
  product scripts, contracts, generated output and coordinator state remain
  untouched.
