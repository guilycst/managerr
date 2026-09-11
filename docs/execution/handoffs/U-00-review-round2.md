# U-00 independent review round two

## Decision

`approved`.

Round-one findings are closed. No P0, P1 or P2 findings remain for U-00. This
decision accepts the pinned public UI baseline and U-00's bounded acceptance
contributions. It does not claim browser, route, generated-template, container,
release, or deployment completion.

## Review identity and boundary

- Reviewer: `/root/u00_reviewer`, independent reviewer role.
- Corrected implementation target:
  `54d2feba360616f8f96c43ae73caadf802b28722`.
- Coordinator metadata supplied for review:
  `0d432bbc21dd32dbd2fe170ca3540a3827ad6d38`.
- Round-one receipt:
  `382bcab86b01e3fdf10e3d1de94d788d1f6e4cb2`.
- Recorded assignment base:
  `5108e8b86000a8f916852f4614a9e4e10c868016`.
- Review changed only this receipt. It did not change product files, state,
  dependencies, upstream repositories, or any live media system.

## Round-one finding closure

### Local runtime description

Closed. `docs/research/ui-components.md` and the U-00 handoff now state that
`head.Dependencies(head.WithLocalRuntime())` emits direct, version-matched local
scripts and no dependency loader or fallback CDN path.

Selected Goshtoso `v0.3.0` source confirms this behavior. An independent
synthetic consumer rendered `consoleshell.Layout` and
`consoleshell.Fragment`, found the local HTMX, Alpine, Goshtoso and App Shell
asset URLs, found no `/assets/js/dependency-loader.js`, and received HTTP 200
from both public asset handlers.

### Annotated tag provenance

Closed. The ledger now records the full annotated-tag relationship:

- tag object: `6b6374f6f9112e98a3ccbdf9d7fbc7121f591a1e`;
- peeled commit: `2c4e86ab838868335e99f485bbab8caa88b54c8b`;
- Go proxy `Origin.Hash`:
  `2c4e86ab838868335e99f485bbab8caa88b54c8b`.

`git ls-remote`, GitHub's tag-object API and `go list -m -json` agree. The
ledger correctly reports no tag/proxy mismatch and retains the module checksum.

### Dispatch base

Closed. Both the U-00 handoff and coordinator state record
`5108e8b86000a8f916852f4614a9e4e10c868016`. Git confirms that commit is an
ancestor of the corrected target.

## Independent verification

| Command or scenario | Result |
| --- | --- |
| `git diff --check 382bcab86b01e3fdf10e3d1de94d788d1f6e4cb2..54d2feba360616f8f96c43ae73caadf802b28722` | PASS. |
| Review corrected target diff | PASS; only `docs/research/ui-components.md` and `docs/execution/handoffs/U-00.md` changed. |
| Compare `ui/go.mod` and `ui/go.sum` from `ebcc76ceb337dee9619c653aee7e97cb55002922` through corrected target | PASS; dependency pins and sums are byte-for-byte unchanged. |
| `GOWORK=off go list -m -json` for Goshtoso `v0.3.0`, App Shells `v0.1.9-0.20260910224508-5b2222e54637`, and templ `v0.3.1020` | PASS; versions, sums and public origins match the ledger. No replacement appears. |
| `git ls-remote --tags` plus GitHub tag-object API for Goshtoso `v0.3.0` | PASS; annotated tag peels to the Go proxy origin commit. |
| GitHub commit API for App Shells `5b2222e54637f895897fa6dfd494b8c24b2097d9` | PASS; selected pseudo-version commit remains public and matches module origin. |
| Inspect selected App Shell `shell.js` | PASS; it uses HTMX 4 lifecycle names `htmx:before:swap` and `htmx:after:settle`. |
| `GOWORK=off GOFLAGS=-mod=readonly go build` for all ledger-selected components, themes, `consoleshell`, and both asset packages | PASS. |
| Temporary synthetic consumer render and public asset-handler probe | PASS; full document, fragment, metadata, direct local runtime and both handlers work together. Temporary source and directory removed. |
| `GOWORK=off go mod verify` in `ui/` | PASS; `all modules verified`. |
| `GOWORK=off go test ./...` and `GOWORK=off go vet ./...` in empty `ui/` module after removing the temporary consumer | PASS with expected `no packages to test` and `no packages to vet`. |
| `GOWORK=off go mod tidy -diff` in empty `ui/` module | Expected diff removing frozen unused pins. U-01 must rerun after generated and authored packages consume them; no module rewrite performed. |
| `python3 scripts/check_planning.py` | PASS; 38 tasks, 60 acceptance cases, local links resolve. |
| Scan corrected public files for local replacements, root-internal coupling, credentials, private hosts, user paths or real inventory | PASS; no prohibited data found. |
| Check coordinator metadata at `0d432bbc21dd32dbd2fe170ca3540a3827ad6d38` | PASS; round 2 targets the corrected commit, preserves round-one history and records the same base as the handoff. |

## Acceptance contribution assessment

- A-45: U-00 contribution accepted. Explicit pins, public packages, HTTP-only
  module boundary, GOWORK-off checks and the synthetic consumer pass. Full
  templ generation and clean module tidy remain U-01 work.
- A-50: U-00 contribution accepted. Public component and Goshtoso/Minimal theme
  selections exist and compile. Browser accessibility, viewport, color-mode and
  interaction evidence remains U-05 work.
- A-51: U-00 contribution accepted. The selected shell and metadata API render
  synthetic private-safe metadata. All-route initial HTML, useful 404 and real
  preview-asset checks remain U-01/U-05 work.

## Integration note

Coordinator may mark U-00 done and unblock U-01. Keep the current pseudo-version
caveat visible until App Shells publishes a tested stable HTMX 4 release. The
approved baseline remains local implementation evidence, not release or live
deployment evidence.
