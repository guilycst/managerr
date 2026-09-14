# X-14 qBittorrent control client correction, round two

## Assignment

- Task ID and title: X-14, close the generated-output path review finding.
- Coordinator owner: `/root`.
- Independent reviewer: `/root/x05_reviewer`.
- Prior correction product: `08cbb20b68aeb284673e0d19c40c0ea9e5f4f3c4`.
- Prior review receipt: `4208c1c5ce32699e5fa78ce8fd33d4f5d639c329`.
- Product and handoff paths remain unchanged. This coordinator correction owns
  `scripts/generate.sh` and this handoff only.

## Correction

The aggregate generator now always writes qBittorrent's raw oapi-codegen
output to `clients/qbittorrent/internal/generated/client.gen.go`. It no longer
selects the output from the existence of an artifact, so a missing generated
file cannot recreate the old public `clients/qbittorrent/generated` package.
The module-local config and check already use the same internal path.

## Verification

- Moved the internal generated file aside, ran `./scripts/generate.sh --write`,
  and verified the internal artifact was recreated while the public generated
  path remained absent.
- The coordinator fix passed the versioned pre-commit fast guardrail checks.
- No generated file was hand-edited, and no root adapter or runtime qBittorrent
  write capability was enabled.

## Review and integration

- Coordinator correction commit: `0d3cc2ead00c26237d8b24f0c8f269da0a974bfa`.
- Independent review of this correction is pending at
  `docs/execution/handoffs/X-14-review-round3.md`.
- G-01, live qBittorrent product evidence and runtime write enablement remain
  open.

## Resume checkpoint

The generated output path is now fixed by the authoritative aggregate
generator. The next safe action is an independent review that repeats the
missing-artifact write probe and the full generation/guardrail checks.
