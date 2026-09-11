# C-01 independent review

## Review identity

- Decision: `changes_requested`
- Reviewed implementation commit: `934485c619f44ec2f0d858e83eb724821987d39c`
- Implementation parent/base: `f39f864915800f8b28284c394a53d602e7220b33`
- Coordinator checkpoint and review HEAD: `a2d40797e5660452b75d73c059f61bbf0d99fd81`
- Checkpoint parent: reviewed implementation commit
- Reviewer: `/root/c01_reviewer`, independent of coordinator author
- Boundary: `api/openapi.yaml`, both oapi-codegen configs, HTTP reference,
  C-01 handoff, and C-01 identity in `state.json`
- No live media stack, upstream write, credential, real inventory, tracker data,
  or private runtime coordinate was used.

## Findings

### P1: Generated consumers reject every documented action discriminator

Disposition: `current_blocker`.

`ActionInput` declares a discriminator without an explicit mapping at
`api/openapi.yaml:2076-2092`. Its variants use wire values such as
`arr.registration`, while their schema names are `ArrRegistrationInput`,
`ArrImportInput`, and similar. oapi-codegen therefore generated
`ValueByDiscriminator` cases for schema names. A valid request containing
`"kind":"arr.registration"` unmarshalled, then failed with
`unknown discriminator value: arr.registration`. Generated `From*` helpers also
write schema-name discriminator values outside each variant's enum.

Required change: add an explicit mapping for all 13 wire values and prove both
generated packages decode and construct each documented value. Keep the closed
variant schemas and unique kind enums.

### P1: Execution and approval schemas cannot represent required safe states

Disposition: `current_blocker`.

The shared `JobState` at `api/openapi.yaml:2071-2073` replaces the specified
action states `waiting_dependency`, `reconciling`, and `needs_review` with
`waiting`; it also lacks workflow `awaiting_approval` and step `blocked` and
`skipped`. This contradicts `data-and-recovery.md:43-60`. `ActionRun` omits the
documented retry policy/reason, and `WorkflowRun` omits its deadline timestamp.

Approval authority is also exposed to clients. `ActionPlan.requiredApproval`
permits `none` at `api/openapi.yaml:1996-1999`, while every v0.0.1 action is a
mutation that the HTTP reference routes through a review decision. Workflow
creation requires a client-provided `approvalRequired` boolean at
`api/openapi.yaml:2497-2511`; a forged workflow can therefore request an
unapproved destructive step at the wire level. This conflicts with
`http-api.md:68,76-79,115-120` and A-12/A-15.

Required change: use separate action, workflow, and step state enums; expose
retry/deadline state required for restart recovery; derive approval gates from
saved plans/recipes instead of accepting client authority; remove the
unreviewed-action state.

### P1: Configuration contract contradicts retained configuration specification

Disposition: `current_blocker`.

One UUID `Id` schema is used for runtime IDs and configuration connection/root/
mapping IDs at `api/openapi.yaml:1272-1274,1405-1410,1520-1525,1579-1588`. Static
examples use stable names such as `radarr-main`, and `configuration.md:82-83`
explicitly requires separate stable constrained configuration IDs and runtime
UUIDs. `SourceMetadata.source` is `static_file|api|default`, not the retained
`yaml|api` contract, and lacks the required editable flag and source-document
identity. Individual configuration GET responses omit ETag, so a client cannot
reliably obtain the token required by PATCH/DELETE. `StorageRoot` also omits its
effective path and watch settings, while `ConnectionOptions.options` is an open
`map[string]interface{}` despite the route promising typed roots, profiles,
series types, seasons, and episodes. Generic credentials require both `username`
and `secret`, so common API-key connections cannot supply their native credential
shape without inventing a username.

Required change: freeze distinct config/runtime ID schemas, align source metadata
with `configuration.md`, expose effective nonsecret resource fields and ETags,
and type supported credential and connection-option variants.

### P1: HTTP concurrency, idempotency, async, and error responses are incomplete

Disposition: `current_blocker`.

The HTTP reference requires `409 snapshot_expired` for expired list cursors,
`428 precondition_required` for a missing If-Match, `409 idempotency_conflict`
for any changed request under a reused key, and `Location` plus parent identity
for every async subresource. The OpenAPI contract declares no 428 response, no
cursor 409 on any list, and no 409 on connection-check creation or scan/action/
workflow cancellation and reconciliation. The 202 responses for scan/action/
workflow cancellations, reconciliation, and retry omit `Location`; cancellation
responses have no parent ID. The defined 429 response is unused, and immediate
dependency operations do not model 503. Required `Cache-Control: no-store`
headers are absent, including descriptor content.

Required change: attach the documented problem+json statuses to every applicable
operation, add missing ETag/Location/Retry-After/cache headers, and make async
resource identity and parent identity explicit. Prove every Idempotency-Key route
has its conflict response and every cursor route has snapshot-expiry behavior.

### P1: Several typed action inputs omit authority-bearing required fields

Disposition: `current_blocker`.

All 13 named action kinds exist, but their request constraints do not match
`http-api.md:126-137`. `arr.registration.fields` is optional and may be empty;
`arr.import.transfer` is optional; `fs.move` and `fs.rename` have no selected
executor; and Jellyfin item refresh does not require `itemId` or prohibit it for
library scope. These omissions let two different intents share the same accepted
shape or defer an illegal combination beyond the frozen contract. The open
`ActionPlan.kind` string can also disagree with `action.kind`.

Required change: require every authority-bearing field, model conditional
Jellyfin scopes as closed variants, and remove or constrain duplicate kind data.

### P1: OpenAPI validation fails in both structural and generated-server tooling

Disposition: `current_blocker`.

Redocly reports `api/openapi.yaml:606` because `minLength` is attached to the
Parameter Object instead of its schema. kin-openapi validation fails because the
FileTarget negative-lookahead pattern at `api/openapi.yaml:1709-1712` uses regexp
syntax unsupported by Go: `invalid or unsupported Perl syntax: (?!`. The same
pattern also accepts `.` as a relative path even though root targets are forbidden.
oapi-codegen emission alone did not validate these semantics.

Required change: move the query constraint into its schema and use a pattern
compatible with the selected OpenAPI/runtime validator, with component-aware
server validation for traversal and root rejection. Run a full OpenAPI validation
before generation.

### P2: Producer handoff records non-reproducible command and stale counts

Disposition: `must_correct_with_C-01`.

Both configs hard-code `output`. Running the handoff's claimed `-o <temp-file>`
command ignored that flag and wrote the configured repository paths. Isolated
generation succeeded only after running from a temporary working directory with
absolute config/spec paths. The handoff also records 453 local references; direct
parsing and `rg` both count 454. The two accidental generated files from review
were removed immediately, and repository status was clean before this handoff.

Required change: record the actual isolated command and current count, or adjust
the config/command so the output override works as stated.

## Checks that passed

- Route inventory matches the HTTP reference: 42 paths and 55 operations.
- All 454 local references resolve; operation IDs are unique; path parameter
  names match their templates.
- All 13 requested action kind values are present in closed request variants.
- No security scheme or Authorization parameter exists. Decision and audit actor
  values are server-owned `unauthenticated`; caller label remains unverified.
- No Seerr mutation route or action exists. Jellyfin refresh remains a separate
  action from availability observations, subject to the input correction above.
- Focused inspection found no credential value, tracker URL, private host, real
  inventory, or user-specific path in reviewed committed files.
- State identity correctly records C-01 `in_review`, implementation commit,
  reviewer, and next action. No integration is claimed.

## Acceptance contribution assessment

- A-12: not accepted. Registration/import have distinct kinds and decisions bind
  plan/revision/digest, but client-controlled approval gates and `none` leave an
  approval bypass in the contract.
- A-15: partial only. Plan binding and conflict responses exist, but required
  execution/review states and authority-bearing inputs are incomplete.
- A-45: not accepted. Both generators emit compilable packages in isolation, but
  the OpenAPI validator and documented action discriminator fail.
- A-46: accepted for C-01. No auth scheme exists and authoritative actor is fixed
  to `unauthenticated`.
- A-56: not accepted. Required 428/409/413/429/503 operation coverage and one
  query constraint are incomplete or invalid.
- A-60: not accepted. Standalone/workflow routes exist, but generated typed action
  dispatch and uniform approval/state semantics do not work.

## Commands and evidence

| Command or inspection | Result |
| --- | --- |
| Git status, log, parent, worktree, remote, and agent inspection | Clean shared `main`; checkpoint is direct child of implementation; reviewer ownership matches state. |
| Read-only YAML parser with reference, operation, path-parameter, action-kind, and security assertions | Passed: 42 paths, 55 operations, 454 references, 13 kinds, unique operation IDs, matching path parameters, no security schemes. |
| `npx --yes @redocly/cli@1.34.3 lint api/openapi.yaml --skip-rule=operation-summary --skip-rule=security-defined --max-problems=200` | Exit 1: structural `minLength` error; remaining output was style warnings, including expected no-auth policy warnings. |
| `go run github.com/getkin/kin-openapi/cmd/validate@v0.133.0 api/openapi.yaml` | Exit 1: unsupported negative lookahead regexp. |
| Run oapi-codegen v2.8.0 from separate temporary working directories with absolute config/spec paths | Both root strict std-http server and UI client generated; no generated output remained in repository. Root emitted expected no-module warning. |
| Temporary module `go test ./...` for generated UI client | Exit 0. |
| Temporary generated-server test decoding documented `arr.registration` then calling `ValueByDiscriminator` | Exit 1: `unknown discriminator value: arr.registration`. Package compiled before the failing assertion. |
| `python3 scripts/check_planning.py --self-test` and `python3 scripts/check_planning.py` | Passed: self-checks, 38 tasks, 60 acceptance cases, local links. |
| `git diff --check f39f864 934485c`, `git diff --check`, focused secret/private-data scan | Passed. |

## Decision and next event

`changes_requested`. Correct all P1 findings on the same C-01 target, update the
producer handoff evidence, freeze a new implementation commit, and request a new
independent review before C-02. Coordinator owns `state.json` and integration.
No live operation, product file, or execution state was changed by this review.
