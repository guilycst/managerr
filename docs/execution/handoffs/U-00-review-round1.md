# U-00 independent review round one

## Decision

`changes_requested`.

Selected pins and public packages are compatible, but the frozen runtime and
provenance ledger contains two incorrect conclusions. One coordinator-owned
dispatch record also disagrees with the implementer handoff. U-00 should not
unblock U-01 until the ledger is corrected and reviewed again.

## Review identity and boundary

- Reviewer: `/root/u00_reviewer`, independent reviewer role.
- Implementation target:
  `ebcc76ceb337dee9619c653aee7e97cb55002922`.
- Coordinator metadata supplied for review:
  `3547594535aeda977c1431377e16de1c27922151`.
- Implementation commit changed only `docs/research/ui-components.md`,
  `ui/go.mod`, and `ui/go.sum`.
- Review inspected current public module and GitHub evidence. It did not inherit
  the implementer's conclusions.
- Review changed only this receipt. It did not change product files, execution
  state, dependencies, upstream repositories, or any live media system.

## Findings

### P1: Local-runtime contract incorrectly promises a dependency loader

`docs/research/ui-components.md:63` says
`head.Dependencies(head.WithLocalRuntime())` supplies the embedded dependency
loader. The selected Goshtoso `v0.3.0` public API says the opposite:
`WithLocalRuntime` emits direct version-matched local scripts and no fallback
loader. A synthetic render confirmed that the layout contains the local HTMX,
Alpine, first-party and shell script URLs and does not contain
`/assets/js/dependency-loader.js`.

This file is the frozen contract for U-01. The wrong rule can produce an
incorrect asset assertion or duplicated loader handling in the BFF. Replace the
claim with the actual direct local-script behavior. Keep the existing
`assets.Handler()` mount because it serves those local assets.

Evidence:

- `github.com/araihu/goshtoso@v0.3.0/components/head/types.go` documents that
  `WithLocalRuntime` emits no CDN requests or fallback loader.
- `github.com/araihu/goshtoso@v0.3.0/components/head/head.templ` renders
  `localDependencyScripts` when local runtime is selected and uses the loader
  only for the non-local path.
- Focused synthetic consumer test rendered `consoleshell.Layout` and
  `consoleshell.Fragment`, checked local asset URLs and both public handlers,
  and passed after supplying the shell's required matching navigation item.

### P1: Annotated tag identity is reported as a provenance mismatch

`docs/research/ui-components.md:37` and the U-00 handoff say a direct Git ref
query returned a different Goshtoso `v0.3.0` hash from the Go proxy origin and
therefore requires a later provenance recheck. The direct ref is an annotated
tag object:

- `refs/tags/v0.3.0` is tag object
  `6b6374f6f9112e98a3ccbdf9d7fbc7121f591a1e`.
- `refs/tags/v0.3.0^{}` peels to commit
  `2c4e86ab838868335e99f485bbab8caa88b54c8b`.
- Go proxy `Origin.Hash` is the same peeled commit
  `2c4e86ab838868335e99f485bbab8caa88b54c8b`.
- GitHub's tag-object API also reports that exact commit as the annotated tag's
  target.

There is no observed tag/proxy discrepancy. Correct the ledger and handoff to
record the tag object plus peeled commit relationship and remove the false
provenance uncertainty. Retain the module and `go.sum` identities.

### P2: Dispatch base differs between state and handoff

The implementer handoff records base
`39f5a5501b11a5af383b8e5c3ba5b720bd4ad456`, while coordinator metadata at
`3547594535aeda977c1431377e16de1c27922151` records
`5108e8b86000a8f916852f4614a9e4e10c868016`. Both are ancestors of the target;
`39f5a55` is the child commit named `chore: correct U-00 dispatch base`.

Coordinator should reconcile the recorded assignment base with the actual
dispatch evidence. This does not change the reviewed UI dependency result, but
durable progress must have one exact base.

## Independent verification

| Command or scenario | Result |
| --- | --- |
| `git show --stat --oneline ebcc76ceb337dee9619c653aee7e97cb55002922` | PASS; target changes the component ledger and UI module pins only. |
| `git diff --check ebcc76ceb337dee9619c653aee7e97cb55002922^..ebcc76ceb337dee9619c653aee7e97cb55002922` | PASS. |
| `git diff --quiet ebcc76ceb337dee9619c653aee7e97cb55002922..HEAD -- ui/go.mod ui/go.sum docs/research/ui-components.md` | PASS; reviewed U-00 paths are unchanged at the review checkout. |
| `GOWORK=off go list -m all` and selected `go list -m -json` calls in `ui/` | PASS; Goshtoso `v0.3.0`, App Shells `v0.1.9-0.20260910224508-5b2222e54637`, and templ `v0.3.1020` resolve with the recorded sums and origins. No replacement appears. |
| `GOWORK=off go list -m -versions` for Goshtoso, App Shells and templ | PASS; public indexes list Goshtoso through `v0.3.0`, App Shells through `v0.1.8`, and templ through `v0.3.1020`. |
| GitHub release API for Goshtoso and App Shells | PASS; Goshtoso latest release is `v0.3.0`, published `2026-09-11T02:04:47Z`; App Shells latest published release is `v0.1.4`. |
| GitHub commit API for App Shells `5b2222e54637f895897fa6dfd494b8c24b2097d9` | PASS; public commit exists and matches the selected pseudo-version origin. |
| Compare App Shells `v0.1.8` and selected pseudo-version `consoleshell/assets/shell.js` | PASS; `v0.1.8` uses HTMX 2 lifecycle names, while the selected commit uses `htmx:before:swap` and `htmx:after:settle`. |
| `GOWORK=off GOFLAGS=-mod=readonly go build` for all ledger-selected Goshtoso component packages, `themes`, `consoleshell`, and both asset packages | PASS. |
| Temporary synthetic consumer render of `consoleshell.Layout` and `consoleshell.Fragment`, plus requests to Goshtoso and shell asset handlers | PASS; full document, fragment, metadata and local assets work together. Temporary source removed. |
| `GOWORK=off go mod verify` in `ui/` | PASS; `all modules verified`. |
| `GOWORK=off go test ./...` in `ui/` | PASS with expected `no packages to test`; U-01 has not created a UI package. |
| `GOWORK=off go vet ./...` in `ui/` | PASS with expected `no packages to vet`. |
| `GOWORK=off go mod tidy -diff` in empty `ui/` module | Expected nonzero diff removing all frozen requirements and sums. The handoff reports this accurately and did not rewrite the module. U-01 must rerun after generated and authored packages consume the pins. |
| Public package lookup for every surface named in the reuse ledger | PASS; selected package paths and named entry points exist, including `components/select` with package name `selectfield` and `github.com/araihu/goshtoso/themes`. |
| Scan target diff and UI module for local replacement, root internal import, user path, private host, credential or real inventory data | PASS; no prohibited public-repository data found. Synthetic values use `Example Film`, `Example Series`, and `example.test`. |
| `python3 scripts/check_planning.py` | PASS; 38 tasks, 60 acceptance cases, local links resolve. |

## Acceptance contribution assessment

- A-45: not accepted for U-00 while the frozen runtime statement is wrong.
  Module separation, exact dependency pins, read-only builds and GOWORK-off
  verification pass. Templ generation and a clean tidy remain U-01 work.
- A-50: U-00 component and theme selection is usable. Full accessibility and
  browser evidence remains U-05 work and is not claimed here.
- A-51: U-00 selected a public metadata API and synthetic private-safe inputs.
  Initial HTML across routes, 404 behavior and preview asset loading remain
  U-01/U-05 work and are not claimed here.

## Next review event

Correct the two P1 ledger statements, reconcile the coordinator-owned base
record, freeze a new exact documentation commit, and request a second
independent review. Preserve the selected module pins unless new evidence
changes compatibility.
