# X-14 correction round 1: qBittorrent control client

## Assignment

- Task ID and title: X-14, close qBittorrent control-client review findings.
- Owner: `/root/x05_implementer`.
- Independent reviewer: `/root/x05_reviewer`.
- Correction base product: `3fce4c79f4e52ba3147f300e8a460e9e44c228bc`.
- Review receipt: `0243a396e44a99a0c36f1d2c04721c8bc522564f`.
- Coordinator path-migration prerequisite: `47eac36`.
- Branch/worktree ownership: shared checkout on `main`; coordinator owns
  integration and `docs/execution/state.json`.
- Owned product paths: `clients/qbittorrent/`.
- Owned handoff path: `docs/execution/handoffs/X-14-correction-round1.md`.
- Acceptance contributions: A-28, A-29, A-30, A-31, A-33 and A-45.

## Product result

Product commit: `08cbb20b68aeb284673e0d19c40c0ea9e5f4f3c4`
(`fix(qbittorrent): close control client safety gaps`).

The correction closes all four findings in review receipt
`0243a396e44a99a0c36f1d2c04721c8bc522564f`.

1. Handwritten control wrappers now require one exact qBittorrent v1/v2
   identity: 40 or 64 hexadecimal characters. Empty, malformed,
   pipe-separated and case-insensitive `all` values return typed invalid input
   before authentication or network dispatch. The rule is shared by Stop,
   SetLocation, RenameFile, RenameFolder and Delete.
2. The OpenAPI control responses now model qBittorrent's documented HTTP 200
   status-only response with no body. `postControl` accepts an empty body and
   keeps an unexpected nonempty body as sanitized unknown. Login retains its
   separate exact `Ok.` body and SID-cookie contract.
3. Raw oapi-codegen output is now under
   `clients/qbittorrent/internal/generated/`. The coordinator's prerequisite
   commit `47eac36` captured the generated-file move and aggregate generator
   path support; this product correction updates the module config, handwritten
   import, local reproducibility script and regenerated output. Go's internal
   package rule prevents external module consumers and the root adapter from
   importing raw generated control builders. Public callers have only the
   guarded handwritten methods, so generated builders cannot be used to emit
   `hashes=all` or `deleteFiles=true`.
4. SetLocation HTTP 403 is normalized as a nonretryable typed conflict for
   target-directory permission failure and leaves the SID session installed.
   Stop, rename and metadata-removal 403 responses retain authentication
   handling and invalidate the selected SID. The OpenAPI contract has a
   route-specific `PermissionDenied` response for SetLocation.

Control success fixtures now return an explicit empty HTTP 200 response. The
negative unexpected-body fixture remains to prevent silently accepting an
unmodeled response. All control methods continue to use the module-owned
cookie session, origin/referer headers, context deadlines, bounded response
reads, status normalization, redirect refusal and sanitized upstream errors.
No root package, domain type, port, adapter, storage or workflow package was
added to the nested module, and root runtime writes remain disabled.

No live qBittorrent service, credentials, tracker coordinate, private
inventory, media payload or upstream mutation was used.

## Synthetic coverage

`clients/qbittorrent/client_test.go` adds or updates module-owned `httptest`
coverage for:

- all five control wrappers with empty status-only HTTP 200 responses;
- an unexpected nonempty control body remaining typed unknown;
- empty, lowercase/uppercase/mixed-case `all`, pipe-separated and malformed
  hashes for every handwritten wrapper, with zero login/control requests;
- metadata-only `Delete(..., false)` form encoding and
  `Delete(..., true)` rejection before any request;
- SetLocation 403 mapping to conflict without SID invalidation, followed by a
  successful authenticated Stop with no second login;
- existing cookie authentication, URL/form encoding, 401/403/409 sanitation,
  timeout normalization and no blind mutation retry.

The generated control package remains present only for module-internal client
implementation and reproducible contract output. A synthetic external module
import probe fails with Go's expected `use of internal package ... not allowed`
diagnostic.

## Verification

Commands below were run against product commit
`08cbb20b68aeb284673e0d19c40c0ea9e5f4f3c4`. Unrelated coordinator state and
other lane files were not changed by this correction.

| Command | Result | Evidence |
| --- | --- | --- |
| `GOWORK=off go test -mod=readonly -count=1 ./...` from `clients/qbittorrent` | Passed, exit 0 | Standalone client and `internal/generated` package. |
| `GOWORK=off go test -mod=readonly -race -count=1 ./...` from `clients/qbittorrent` | Passed, exit 0 | Control validation, session preservation and timeout tests under race. |
| `GOWORK=off go vet -mod=readonly ./...` from `clients/qbittorrent` | Passed, exit 0 | Nested qBittorrent module vet. |
| `GOWORK=off go mod verify` from `clients/qbittorrent` | Passed: `all modules verified` | Nested module checksums. |
| `GOWORK=off go generate ./...` from `clients/qbittorrent` | Passed, no generated drift | Pinned oapi-codegen v2.8.0. |
| `./check-generation.sh` from `clients/qbittorrent` | Passed: `qBittorrent generation checks passed` | Internal generated output reproduced byte-for-byte. |
| qBittorrent golangci-lint with `GOWORK=off` and pinned tools module | Passed: `0 issues.` | Standalone module lint. |
| synthetic external import probe for `clients/qbittorrent/internal/generated` | Passed expected rejection | Go internal-package boundary blocked external import. |
| `GOWORK=off go test -mod=readonly -count=1 ./internal/adapters/qbittorrent/control` | Passed, exit 0 | Root control adapter seam. |
| `GOWORK=off go test -mod=readonly -race -count=1 ./internal/adapters/qbittorrent/control` | Passed, exit 0 | Root control safety tests under race. |
| `GOWORK=off go vet -mod=readonly ./internal/adapters/qbittorrent/control` | Passed, exit 0 | Focused root adapter vet. |
| `GOOS=linux GOARCH=amd64 GOWORK=off go test -mod=readonly -run '^$' -c` from `clients/qbittorrent` | Passed | Linux amd64 test binary produced. |
| `GOOS=linux GOARCH=arm64 GOWORK=off go test -mod=readonly -run '^$' -c` from `clients/qbittorrent` | Passed | Linux arm64 test binary produced. |
| `./scripts/check-api.sh` | Passed; Vacuum quality 100/100 | Aggregate API generation and zero-warning Vacuum gate. |
| `./scripts/check-lint.sh` | Passed: `0 issues.` | Root, UI, tools and nested-client lint matrix. |
| `./scripts/check-architecture.py` | Passed | Root import direction. |
| `python3 scripts/check_planning.py --self-test` | Passed; 44 tasks, 60 acceptance cases | Planning links and self-checks. |
| `./scripts/check-guardrails.sh --fast` | Passed: `guardrail checks passed (fast)` | Pre-commit generation, API, architecture, format and focused tests. |
| `./scripts/check-guardrails.sh --ci` | Passed: `guardrail checks passed (ci)` | Full module tests, lint, vet, generation and module verification. |
| `git diff --cached --check` before product commit | Passed, exit 0 | Owned correction product whitespace. |

## Review and integration

- Correction product commit:
  `08cbb20b68aeb284673e0d19c40c0ea9e5f4f3c4`.
- Correction handoff commit: pending; this file is intentionally committed
  separately.
- Independent review receipt and decision: pending round-two review.
- Integrated commit and execution-state update: pending; coordinator owns
  `docs/execution/state.json`.
- The root generator support and generated-file relocation are in coordinator
  commit `47eac36`; no local `replace` directive or `go.work` dependency was
  added by this correction.
- G-01/no-overwrite, live qBittorrent product-version evidence and runtime
  capability approval remain open. This correction does not enable root
  mutations, Arr writes, release or deployment.

## Resume checkpoint

- Product correction is committed at
  `08cbb20b68aeb284673e0d19c40c0ea9e5f4f3c4`.
- Raw generated control transport is module-internal and handwritten control
  wrappers fail closed for broad hash scope, payload deletion and documented
  permission failures.
- Next safe action: commit this handoff separately, obtain independent review,
  then let the coordinator record exact correction SHAs and integrate only the
  approved product commit.
