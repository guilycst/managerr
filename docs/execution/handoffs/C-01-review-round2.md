# C-01 independent review, round two

## Decision

- Decision: `approved`
- Release stage: `pre-v0.1.0`
- Current milestone: freeze the v0.0.1 OpenAPI resource, action, error,
  pagination, configuration-source and generated-consumer contracts before C-02
- Reviewed implementation/evidence commit: `bc0ef3584ff650792ba6bc18df559f640eb72c7b`
- Coordinator state checkpoint and review HEAD:
  `cd9f1e451b0421dc75a19fc717de365467515cc0`
- Implementation base: `f39f864915800f8b28284c394a53d602e7220b33`
- Prior review receipt: `0854c5876872ef0212e85ffbbe99b9f90fedd7d1`
- Initial correction named for round two:
  `78c0d45778350e98c3af55ff5dd9a64544b207b7`
- Final stable review identity:
  `05adcd72bbd2f04518b3ea80500850980461a8db4d1507be4f2e301892fd3c1e`
- Reviewer: `/root/c01_reviewer_round2`, independent of coordinator author
- Boundary: C-01 OpenAPI and generation configs, retained HTTP/configuration/
  recovery requirements, implementation plan, execution state, prior review and
  producer handoff

No unresolved P0, P1 or P2 finding remains. No live media stack, upstream write,
credential, real inventory, tracker data or private runtime coordinate was used.

## Prior finding closure

### Action discriminators

`ActionInput` has one explicit mapping for each of the 13 wire values at
`api/openapi.yaml:2527`. Each mapped variant keeps its matching singleton `kind`
enum. Fresh oapi-codegen v2.8.0 output for both root and UI packages decoded all
13 JSON inputs, selected the correct concrete value, reconstructed the union with
its generated `From*` helper, and marshalled the same wire discriminator.

Result: closed.

### Execution states, retries, deadlines and approval authority

Separate action, workflow, step and attempt enums at
`api/openapi.yaml:2515-2525` match the retained state machines. `ActionRun`
includes retry policy, retry reason, next attempt and optional deadline fields.
`WorkflowRun.deadlineAt` is required and uses the named nullable timestamp, so a
workflow without a configured deadline is represented as JSON `null` and as a
nil pointer in both generated Go packages.

Workflow creation accepts only step ID plus saved action-plan ID. It has no
client-supplied approval flag. `ActionPlan.requiredApproval` permits only
`review|irreversible`; the duplicate plan `kind` is gone. The decision request
cannot set authoritative actor, while the response actor remains the singleton
`unauthenticated` value.

Result: closed.

### Configuration identities and typed variants

Runtime UUID and stable `ConfigId` schemas are distinct at
`api/openapi.yaml:1535-1542`. Connections, storage roots and path mappings use
`ConfigId`; runtime jobs and records retain UUIDs. Source metadata requires
`yaml|api`, editable state, document ID, revision, startup time and reload policy.
Individual configuration reads expose ETag. Storage roots require effective path
and watch settings.

Credential input is a closed `api_key|username_password|token` union. Connection
options are a closed `arr|download_client|jellyfin|seerr` union. Secret fields are
write-only.

Result: closed.

### HTTP conflicts, async identity and cache behavior

All 16 cursor operations declare `409 ProblemSnapshotExpired`. All 20
Idempotency-Key operations declare 409. All six If-Match operations declare 428.
Capacity-limited operations declare 429 with Retry-After. Immediate dependency
reads/checks declare 503. Every 202 response has Location; cancellation,
reconciliation and retry subresources require parentId. Every non-health success
response carrying content declares the `no-store` Cache-Control header.

Result: closed.

### Authority-bearing action fields

Registration requires a nonempty `fields` object. Import requires transfer mode.
Move and rename require the selected executor. Jellyfin refresh is a closed
library variant without itemId or item variant with required itemId. ActionPlan
has no second kind field.

Result: closed.

### Validation, constraints and reproducible generation

The metadata-candidate query length constraint now lives in its schema. File
target patterns use syntax accepted by the selected Go validator; component,
root and symlink confinement remains explicit server validation. kin-openapi and
Redocly accept the contract. Both oapi-codegen configurations generate from an
isolated working directory and leave no output in the repository.

Result: closed.

## Acceptance contribution assessment

- A-12: C-01 contribution accepted. Registration and import are distinct typed
  plans and workflow creation cannot reuse or forge approval authority.
- A-15: C-01 contribution accepted. Decisions bind plan ID, revision and digest;
  stale/conflicting requests have typed problem responses and no direct execution
  route.
- A-45: C-01 contribution accepted. Root strict std-http server and UI client
  generate and compile in an isolated module. C-02 still owns pinned tools,
  committed modules and clean regeneration.
- A-46: C-01 contribution accepted. No auth scheme or Authorization parameter
  exists, and authoritative actor remains `unauthenticated`.
- A-56: C-01 contribution accepted. Idempotency conflicts, missing preconditions,
  bounded action-plan requests, unknown-field closure and capacity/dependency
  statuses are represented.
- A-60: C-01 contribution accepted. Standalone and workflow routes share the
  same typed action union, saved-plan approval boundary and execution resources.

These are C-01 contract contributions. Downstream implementation and final
cross-system acceptance remain assigned to later tasks.

## Commands and results

Public-repository commands below normalize reviewer and temporary paths through
environment variables. Versions, Git identities and arguments match the executed
checks.

### Stable Git snapshot

```sh
REVIEW_WORKTREE=$(git rev-parse --show-toplevel)
python3 "$CODEX_HOME/skills/adversarial-artifact-review/scripts/snapshot_git_review.py" \
  --worktree "$REVIEW_WORKTREE" \
  --base f39f864915800f8b28284c394a53d602e7220b33
```

Result before and after final read-only pass: exit 0; HEAD
`cd9f1e451b0421dc75a19fc717de365467515cc0`; clean status; review identity
`05adcd72bbd2f04518b3ea80500850980461a8db4d1507be4f2e301892fd3c1e`.

### YAML, reference, route and contract assertions

```sh
python3 - <<'PY'
import re, yaml

d = yaml.safe_load(open("api/openapi.yaml"))
s = d["components"]["schemas"]
methods = {"get", "post", "put", "patch", "delete", "head", "options", "trace"}

def resolve(ref):
    value = d
    for part in ref[2:].split("/"):
        value = value[part.replace("~1", "/").replace("~0", "~")]
    return value

def deref(value):
    return resolve(value["$ref"]) if isinstance(value, dict) and "$ref" in value else value

refs = []
def walk(value):
    if isinstance(value, dict):
        for key, child in value.items():
            if key == "$ref":
                refs.append(child)
                resolve(child)
            walk(child)
    elif isinstance(value, list):
        for child in value:
            walk(child)
walk(d)

operations = []
for path, item in d["paths"].items():
    for method, operation in item.items():
        if method not in methods:
            continue
        parameters = [deref(x) for x in item.get("parameters", []) + operation.get("parameters", [])]
        assert set(re.findall(r"{([^}]+)}", path)) == {
            x["name"] for x in parameters if x.get("in") == "path"
        }
        assert all(x["required"] for x in parameters if x.get("in") == "path")
        operations.append((path, operation, parameters))

assert (len(d["paths"]), len(operations), len(refs)) == (42, 55, 580)
assert len({x[1]["operationId"] for x in operations}) == 55
assert "securitySchemes" not in d["components"]
assert all("security" not in x[1] for x in operations)

expected = {
    "arr.registration", "arr.import", "fs.copy", "fs.hardlink", "fs.move",
    "fs.rename", "client.stop", "client.remove", "fs.trash", "fs.restore",
    "fs.delete", "descriptor.delete", "jellyfin.refresh",
}
assert set(s["ActionInput"]["discriminator"]["mapping"]) == expected
assert len(s["ActionInput"]["oneOf"]) == 13
assert s["ActionState"]["enum"] == ["queued", "running", "waiting_dependency", "reconciling", "needs_review", "succeeded", "failed", "cancelled", "deadline_exceeded"]
assert s["WorkflowState"]["enum"] == ["awaiting_approval", "running", "waiting_dependency", "needs_review", "succeeded", "failed", "cancelled", "deadline_exceeded"]
assert s["StepState"]["enum"] == ["queued", "running", "succeeded", "failed", "blocked", "skipped", "cancelled"]
assert s["AttemptState"]["enum"] == ["running", "reconciling", "succeeded", "failed", "cancelled"]
assert s["NullableTimestamp"]["nullable"] is True
assert not {"approvalRequired", "approvalGate"} & s["WorkflowRunCreate"]["properties"]["steps"]["items"]["properties"].keys()
assert s["ActionPlan"]["properties"]["requiredApproval"]["enum"] == ["review", "irreversible"]
assert "kind" not in s["ActionPlan"]["properties"]
assert "actor" not in s["ReviewDecisionCreate"]["properties"]
assert s["ReviewDecision"]["properties"]["actor"]["enum"] == ["unauthenticated"]
assert s["RuntimeId"]["format"] == "uuid"
assert all(s[x]["properties"]["id"]["$ref"].endswith("/ConfigId") for x in ["Connection", "StorageRoot", "PathMapping"])
assert s["SourceMetadata"]["properties"]["source"]["enum"] == ["yaml", "api"]
assert {"path", "watch"} <= set(s["StorageRoot"]["required"])
assert set(s["CredentialInput"]["discriminator"]["mapping"]) == {"api_key", "username_password", "token"}
assert set(s["ConnectionOptions"]["properties"]["options"]["discriminator"]["mapping"]) == {"arr", "download_client", "jellyfin", "seerr"}

for path, operation, parameters in operations:
    responses = operation["responses"]
    if any(x.get("name") == "cursor" for x in parameters):
        assert responses["409"]["$ref"].endswith("/ProblemSnapshotExpired")
    if any(x.get("name") == "Idempotency-Key" for x in parameters):
        assert "409" in responses
    if any(x.get("name") == "If-Match" for x in parameters):
        assert "428" in responses
    for status, response in responses.items():
        response = deref(response)
        if status.startswith("2") and response.get("content") and path not in {"/health/live", "/health/ready"}:
            assert "Cache-Control" in response.get("headers", {})
    if "202" in responses:
        response = deref(responses["202"])
        assert "Location" in response["headers"]
        if re.search(r"/{[^}]+}/(cancellations|reconciliations|retry-requests)$", path):
            assert "parentId" in deref(response["content"]["application/json"]["schema"])["required"]

assert d["components"]["responses"]["Problem429"]["headers"]["Retry-After"]
assert s["ArrRegistrationInput"]["properties"]["fields"]["minProperties"] == 1
assert "transfer" in s["ArrImportInput"]["required"]
assert all("executor" in s[x]["required"] for x in ["FsMoveInput", "FsRenameInput"])
library, item = (deref(x) for x in s["JellyfinRefreshInput"]["oneOf"])
assert "itemId" not in library["properties"] and "itemId" in item["required"]
assert d["paths"]["/api/v1/metadata-candidates"]["get"]["parameters"][2]["schema"]["minLength"] == 1
assert all(s[x]["properties"]["relativePath"]["pattern"] == r"^[^/\x00].*$" for x in ["FileTarget", "FileManifestEntry"])
PY
```

Result: exit 0. Counts and every listed invariant passed.

### OpenAPI validators

```sh
go run github.com/getkin/kin-openapi/cmd/validate@v0.133.0 api/openapi.yaml
npx --yes @redocly/cli@1.34.3 lint api/openapi.yaml \
  --skip-rule=operation-summary \
  --skip-rule=security-defined \
  --max-problems=200
```

Result: both exit 0. kin-openapi emitted no findings. Redocly reported ten
non-blocking recommended-rule warnings: missing descriptions for seven tags,
missing 4xx responses for two health operations, and missing 4xx for the
read-only effective-configuration operation.

### Isolated generation and generated package tests

```sh
repo=$(git rev-parse --show-toplevel)
tmp="$repo/.review-tmp"
mkdir -p "$tmp"
(
  cd "$tmp"
  go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 \
    -config "$repo/api/oapi-codegen.yaml" "$repo/api/openapi.yaml"
  go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 \
    -config "$repo/ui/oapi-codegen.yaml" "$repo/api/openapi.yaml"
)
```

Reviewer then added a temporary `go.mod` with module `review.local/c01` and one
package-local test file beside each generated package. These disposable files
were created with `apply_patch`. Test command:

```sh
cd "$tmp"
GOWORK=off go mod tidy
GOWORK=off go test ./... -count=1
```

Generated-package tests covered all 13 discriminator values in both packages.
Each test decoded JSON, called
`ValueByDiscriminator`, rebuilt through the matching generated `From*` method,
marshalled the union, and compared the resulting `kind`. Both packages also
decoded a required `deadlineAt: null` workflow field to nil. Root test compiled
both Go-compatible path patterns with `regexp.Compile`.

Result: both generators exit 0. Root generator emitted only its expected warning
that no go.mod/tools.mod existed beside disposable output. `GOWORK=off go test
./... -count=1` exited 0 for root and UI generated packages. Temporary module
resolved `github.com/oapi-codegen/runtime v1.7.0` and
`github.com/getkin/kin-openapi v0.149.0`; no local replace or go.work was used.

### Planning, whitespace and public-data checks

```sh
python3 scripts/check_planning.py --self-test
python3 scripts/check_planning.py
git diff --check f39f864915800f8b28284c394a53d602e7220b33..cd9f1e451b0421dc75a19fc717de365467515cc0
git diff --no-color f39f864915800f8b28284c394a53d602e7220b33..cd9f1e451b0421dc75a19fc717de365467515cc0 \
  -- api/openapi.yaml docs/specs/spec-001-media-reconciliation/http-api.md \
     docs/execution/handoffs/C-01.md \
  | rg -n '(/Users/[^/]+/|https?://[^[:space:]]+:[^[:space:]@]+@|passkey=|announce=)'
```

Result: both planning commands exit 0 with 38 tasks, 60 acceptance cases and
resolved local links. `git diff --check` exits 0. Focused public-data scan exits
1 with no matches, the expected no-match result.

## Non-blocking follow-up

- C-02 must pin generators in the tools module, create root/UI modules, generate
  with `GOWORK=off`, and enforce clean regeneration.
- C-04 must wire request validation, stable problem codes, ETags, idempotency and
  response headers. Contract presence does not prove runtime behavior.
- Final acceptance still requires synthetic adapter, fault, browser and container
  evidence from the tasks named in the implementation plan.

Redocly documentation warnings do not change wire behavior and do not block this
pre-v0.1.0 contract milestone.

## Boundary receipt

`transcript_not_read`, `no_polling`, `git_native`, `producer_worktree_not_modified`.
Reviewer used directed checkpoints only. No product file or
`docs/execution/state.json` was changed by this review.
