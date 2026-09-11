# Managerr

Review downloaded media and reconcile it with Radarr, Sonarr, Jellyfin, and Seerr.

Status: implementation in progress, targeting v0.0.1. The API contract and
module/tool baseline are complete; the runnable application is still being built.
[MIT licensed](LICENSE).

Managerr will discover video and subtitles in configured download directories,
including files whose download history has disappeared. It will show download
provenance, tracked media, and separate registration, import, and availability
states for every connected instance.

Review can register and import through Arr, place files directly in a library,
or organize and clean up files. Every action has an independent REST contract.
The dashboard composes durable workflows and shows approvals, partial failures,
retries, and cancellation. Unknown Arr titles require registration approval,
then a separate exact import approval.

The API uses Go, hexagonal ports, oapi-codegen, SQLite, sqlc, and golang-migrate.
A nested Go UI module consumes the API over HTTP and serves a Goshtoso dashboard.
A separate tools module owns generators. v0.0.1 has no application authentication;
basic auth and application OIDC are deferred to v0.0.2.

## Design and execution

| Document | Contents |
| --- | --- |
| [System specification](docs/specs/spec-001-media-reconciliation/spec-001-media-reconciliation.md) | Scope, requirements, workflows, invariants |
| [HTTP contract](docs/specs/spec-001-media-reconciliation/http-api.md) | Proposed REST resources and action contracts |
| [Connectors](docs/specs/spec-001-media-reconciliation/connectors.md) | Ports, capabilities, provenance, upstream constraints |
| [Data and recovery](docs/specs/spec-001-media-reconciliation/data-and-recovery.md) | SQLite, durable actions, filesystem recovery, trash |
| [Configuration](docs/specs/spec-001-media-reconciliation/configuration.md) | YAML ownership, API configuration, encryption, containers |
| [Dashboard](docs/specs/spec-001-media-reconciliation/dashboard.md) | BFF boundaries, pages, review behavior, browser gates |
| [Decisions](docs/architecture/decisions.md) | Confirmed choices, selected defaults, open compatibility gates |
| [Implementation lanes](docs/plans/implementation.md) | Ordered tasks, ownership, dependencies, dispatch rules |
| [Acceptance matrix](docs/verification/acceptance.md) | Required evidence for implementation completion |
| [Upstream evidence](docs/research/upstream-evidence.md) | Research sources and limits |
| [Interview record](docs/product/interview.md) | User decisions behind the specification |

Start interrupted work at [RESUME.md](docs/execution/RESUME.md).
Run `python3 scripts/check_planning.py` to validate planning links, task
references, dependencies, and execution-state structure. Execution progress and
review receipts live in [state.json](docs/execution/state.json).

Implementation is authorized for v0.0.1. Release and live operation require
their respective gates. Public examples and tests must use synthetic media and
endpoints.
