# D-02 correction round three handoff

## Assignment

- Task ID and title: D-02, credential encryption and key lifecycle, correction round three.
- Implementer: `/root/d02_implementer`.
- Independent reviewer: `/root/d02_reviewer` (pending re-review).
- Baseline product: `0ee6f0a0f1046d7db44e8bb3e59e46b005037ec5`.
- Review requesting correction: `412e014d02be2807f34f3762f7f21500f3560562`.
- Correction dispatch checkpoint: `75607fc674f449732ebbd62fb2b51e22be57eb15` (`docs(execution): assign D-02 descriptor read correction`).
- Checkout ownership: shared `main` checkout. Product ownership was limited to `internal/credentials/`; this handoff is the only other path changed by this correction.
- Product commit: `f931a1256bd56a9aa373e295bbbbb3b1eaa7adef` (`fix(credentials): bind explicit key reads to descriptors`).

## Finding addressed

The explicit key-file reader retains the path-level `os.Stat` check so a stable FIFO or other special file is rejected before `os.Open` can block. It then opens the selected path exactly once, runs `file.Stat()` on that descriptor, requires a regular file, and reads the bounded bytes from that same descriptor. Projected Secret symlinks continue to work because the path-level check follows the operator-selected link. Invalid, missing, directory, FIFO, oversized, and malformed sources retain sanitized errors and never trigger generated-key fallback.

`readBoundedFile` now delegates to the descriptor helper as well, so persistent-key reads receive the same regular-file and bounded-read invariant after opening. `readBoundedDescriptor` rejects a nil or non-regular descriptor, maps stat/read failures to sanitized errors, and limits reads to 4 KiB plus one byte for overflow detection.

`TestReadBoundedDescriptorUsesOpenedObject` opens a synthetic regular file, renames the original pathname, writes replacement bytes at the original pathname, and verifies the helper returns the original descriptor bytes. This deterministic regression proves path replacement after open cannot change the object consumed or bypass descriptor type validation.

The source documents the remaining hostile replacement window: a local replacement between the path-level `os.Stat` and `os.Open` can be followed by portable `os.Open` and may block if the replacement is a FIFO. The package does not claim a cross-platform no-follow/nonblocking primitive. Once `os.Open` succeeds, `file.Stat` and the bounded read use one descriptor and cannot drift apart.

## Changed paths

- `internal/credentials/credentials.go`
- `internal/credentials/credentials_unix_test.go`
- `docs/execution/handoffs/D-02-correction-round3.md`

No generated output, module manifest, root adapter, state file, live service, credential, private endpoint, media inventory, or upstream mutation was used or changed.

## Verification

| Command or scenario | Result | Evidence or limitation |
| --- | --- | --- |
| `GOWORK=off go test -count=100 ./internal/credentials` | Passed | Focused D-02 synthetic suite. |
| `GOWORK=off go test -race -count=20 ./internal/credentials` | Passed | Includes concurrent first-key initialization and descriptor regression. |
| `GOWORK=off go vet ./internal/credentials` | Passed | D-02 package. |
| `GOWORK=off go mod verify` | Passed | `all modules verified`. |
| `GOWORK=off go test ./...` | Passed | Root packages and synthetic compatibility tests. |
| `GOWORK=off go vet ./...` | Passed | Root packages. |
| `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOWORK=off go test -run '^$' -exec=true ./internal/credentials` | Passed | Linux amd64 compile-only check. |
| `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 GOWORK=off go test -run '^$' -exec=true ./internal/credentials` | Passed | Linux arm64 compile-only check. |
| `./scripts/generate.sh --check` | Passed | Generated output reproducibility check. |
| `python3 scripts/check-architecture.py` | Passed | Import-boundary check. |
| `python3 scripts/check_planning.py` | Passed | 43 tasks, 60 acceptance cases; links resolve. |
| `./scripts/check-lint.sh` | Passed | Root, UI, tools, and nested client modules. |
| `./scripts/check-guardrails.sh --fast` | Passed | API/Vacuum, generation, architecture, format, targeted root tests, and UI tests. |
| Versioned `.githooks/pre-commit` during product commit | Passed | The hook completed the fast deterministic suite before publishing `f931a1256bd56a9aa373e295bbbbb3b1eaa7adef`. |
| `./scripts/check-guardrails.sh --ci` | Passed | Full API/Vacuum, generation, architecture, lint, root/UI/tools/client tests, vet, and module verification suite. |

## Review and integration

- Product SHA: `f931a1256bd56a9aa373e295bbbbb3b1eaa7adef`.
- Handoff SHA: pending this commit.
- Reviewer decision: pending `/root/d02_reviewer` re-review of `f931a1256bd56a9aa373e295bbbbb3b1eaa7adef`.
- Coordinator owns `docs/execution/state.json`, review integration, and any later merge/publication bookkeeping.

## Resume checkpoint

- D-02 correction round three is complete and product checks pass.
- The next safe action is independent review of the product SHA, followed by coordinator state recording and integration after approval.
- The only documented limitation is the hostile path replacement window before `os.Open`; after successful open, validation and consumption are descriptor-bound.
