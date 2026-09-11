# Decisions and unresolved gates

Status: planning baseline for v0.0.1. No application code is implemented.
User requirements take precedence over engineering defaults. Changing a default
requires updating this record, contracts, acceptance cases and dependent tasks.

## Confirmed user decisions

| ID | Decision | Contract |
| --- | --- | --- |
| D-01 | Public guilycst/mastarr, MIT license. Specification and task lanes before implementation. | [README](../../README.md), [execution](../plans/implementation.md) |
| D-02 | Go API first, oapi-codegen REST, hexagonal ports; nested HTTP-only UI/BFF; separate tools module. | [System](../specs/spec-001-media-reconciliation/spec-001-media-reconciliation.md) |
| D-03 | Directory discovery plus download provenance, original descriptors when available, full Arr inventory, multiple instances. | [Connectors](../specs/spec-001-media-reconciliation/connectors.md) |
| D-04 | Independent actions; Arr registration/import and direct library placement; two approvals for missing Arr titles. | [HTTP](../specs/spec-001-media-reconciliation/http-api.md) |
| D-05 | Durable API-owned workflows, idempotent desired-state checks, cancellation, bounded attempts, wait through temporary outages until cancelled by default. | [Recovery](../specs/spec-001-media-reconciliation/data-and-recovery.md) |
| D-06 | Extensible file/directory actions; trash 30 days by default; janitor; direct API hard delete, UI trash then purge. | [System](../specs/spec-001-media-reconciliation/spec-001-media-reconciliation.md) |
| D-07 | Show seeding; native qBittorrent moves/renames where supported; stop during trash, remove torrent record at permanent deletion. | [Connectors](../specs/spec-001-media-reconciliation/connectors.md) |
| D-08 | Seerr reads only; no broad automatic repair across managers. | [Connectors](../specs/spec-001-media-reconciliation/connectors.md) |
| D-09 | Movies, episodes, packs, anime, subtitles; editable mappings, subtitles follow by default; new Arr registration unmonitored by default. | [Dashboard](../specs/spec-001-media-reconciliation/dashboard.md) |
| D-10 | No silent hardlink-to-copy fallback. Conflicts require review. | [Recovery](../specs/spec-001-media-reconciliation/data-and-recovery.md) |
| D-11 | Portable containers and mounted managed directories; API/UI config plus startup-only read-only YAML with secret references. | [Configuration](../specs/spec-001-media-reconciliation/configuration.md) |
| D-12 | Encrypt managed credentials; supplied env/file key or generate once at a persistent documented path. | [Configuration](../specs/spec-001-media-reconciliation/configuration.md) |
| D-13 | No application authentication in v0.0.1; basic auth and app OIDC in v0.0.2. | [HTTP](../specs/spec-001-media-reconciliation/http-api.md) |
| D-14 | Latest released Goshtoso with supported public components. | [Dashboard](../specs/spec-001-media-reconciliation/dashboard.md) |

## Selected engineering defaults

These implement confirmed requirements but are not attributed to a user answer.

| ID | Default | Reason / amendment gate |
| --- | --- | --- |
| E-01 | Ordered persisted steps plus approval gates; action table is SQL queue. No DAG library or duplicate outbox. | Covers current workflows. Add branching/broker only with a concrete use case. |
| E-02 | One active API/executor per SQLite database. | Avoid unsupported multi-writer/HA coordination; measure before expanding. |
| E-03 | Generated key at /data/keys/credentials.key, AES-256-GCM envelope, 32-byte base64 key; fail on missing/wrong existing key. | Standard-library encryption with explicit recovery requirement. |
| E-04 | No-follow root-confined filesystem operations, exact recursive manifests, content verification and no-replace publication. | Prevent traversal, overwrite and unreviewed file scope. |
| E-05 | Restore leaves clients stopped. Partial torrent deletion removes its record but retains unselected payload. | No automatic seeding/re-add or implicit broader deletion. |
| E-06 | Descriptor bytes survive payload purge; separate explicit deletion. Audit/idempotency metadata retained in initial release. | Avoid losing provenance and replay protection. Report storage growth. |
| E-07 | Conflict resolution chooses another destination or explicitly trashes the conflicting file before placement. | No opaque in-place replacement. Native Arr overwrite behavior stays gated. |
| E-08 | Default scan 5m, file stability observations 30s apart, preview expiry 15m, HTTP attempt 30s, retry 5s to 15m with jitter, janitor 1h. | Bounded configurable defaults; accepted workflow is not expired just because preview age increases. |
| E-09 | Static config ID collisions fail startup, removals retire records, material config changes invalidate dependent intent. | GitOps ownership stays visible; no silent override. |
| E-10 | Linux amd64/arm64 OCI API and BFF images; no privileged container, BFF has no media mount. | Concrete portable baseline without claiming every platform. |
| E-11 | Generic private-dashboard metadata, noindex/no-store, local UI runtime assets. | Initial HTML remains complete without leaking inventory. |

## Open compatibility and deployment gates

| ID | Question | Owner/task | Required resolution |
| --- | --- | --- | --- |
| G-01 | Can supported native Arr import satisfy no-overwrite and exact-file promises despite external races? | C-00, X-05, reviewer | Disposable upstream tests, documented coordination/atomicity limits; block affected writes if not established. A weaker promise needs a product decision. |
| G-02 | Which upstream versions/fields support descriptor export, payload moves, subtitles and anime mapping? | C-00, X-01..X-05 | Pin test versions and per-action capabilities. Unsupported actions explain why. |
| G-03 | SQLite driver/migrate compatibility and filesystem no-replace/durability on target images? | C-02, D-01, F-01, V-02 | Repeatable Linux tests including interrupted operations and restore. |
| G-04 | Latest Goshtoso and compatible App Shell/runtime pair at implementation start? | U-00 | Public API discovery and explicit module pins. Planning observed Goshtoso v0.3.0. |
| G-05 | Actual deployment origin, volumes, credentials, profiles, backup destination and ingress? | Live deployment, separate authorization | Never infer private coordinates or claim live validation from synthetic tests. |
| G-06 | Basic auth/OIDC account and authorization model? | v0.0.2 design | Separate design; no placeholder authentication requirements in v0.0.1. |

An open gate is not an implementation failure and not permission to silently cut
scope. Record evidence and blocked capability explicitly. The coordinator can
advance independent work; a worker cannot override a safety invariant to finish.
