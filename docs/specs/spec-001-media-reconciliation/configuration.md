# Configuration, credentials and containers

Future v0.0.1 contract. API/UI-managed configuration and startup-only YAML are
both supported. Their ownership is explicit; neither silently overrides the other.

## Resource ownership

Connections, storage roots and path mappings have stable IDs and source `api` or
`yaml`. A YAML source includes source document ID and revision, never secret bytes.
GET returns source, editable=false for YAML, effective revision and startup time.
API PATCH/DELETE of file-owned resources returns config_source_read_only. UI shows
read-only values and instructions to edit the file and restart. No write-back,
file watcher, reload endpoint, or automatic secret-file reload exists in v0.0.1.

At startup validate the entire YAML snapshot before activating it. Reject duplicate
IDs, collisions with API-owned IDs, unresolved references, ambiguous mappings,
invalid intervals and malformed credential references. Fail readiness instead of
partially applying it. Repeated YAML IDs from the previous snapshot update those
same YAML-owned records. Removed file resources become inactive historical
references; do not erase discoveries, plans, credentials audit or action history.
Dependent undispatched work pauses when its required config disappears.

API-owned records are runtime editable with ETags and explicit validation. Removing
one retires it; active work must first be cancelled/resolved. Reusing a deleted ID
for a different endpoint or media root is forbidden. Changes to paths, endpoint,
identity and other authority-bearing settings invalidate affected unapproved
plans and return undispatched approved work to review. Secret-only rotation can
preserve intent after successful target-identity verification.

Path mappings translate a root-relative path into one connection's namespace.
Select a unique most-specific mapping; equal-specificity ambiguity is an error.
Match path components, not string prefixes. Never accept `../`, absolute relative
paths, symlink escapes or an arbitrary path from an upstream response as authority.
One mapping does not prove two machines share the same bytes; compatibility tests
and observed file evidence establish that separately.

## Synthetic YAML example

The schema below is proposed and will be frozen with C-01/C-03. Example endpoints
are synthetic service names, not real deployment coordinates.

```yaml
version: 1
connections:
  - id: radarr-main
    kind: radarr
    label: Movies
    endpoint: http://radarr:7878
    credentials:
      apiKey:
        file: /run/secrets/radarr-api-key
  - id: qbt-main
    kind: qbittorrent
    label: Torrents
    endpoint: http://qbittorrent:8080
    credentials:
      username:
        env: QBT_USERNAME
      password:
        file: /run/secrets/qbt-password
storageRoots:
  - id: downloads
    path: /media/downloads
    purpose: download
    watch:
      enabled: true
      interval: 5m
  - id: movies
    path: /media/movies
    purpose: library
pathMappings:
  - id: radarr-downloads
    connectionId: radarr-main
    rootId: downloads
    relativePrefix: ""
    externalPrefix: /downloads
trash:
  retention: 720h
  janitorInterval: 1h
```

Resource IDs in configuration are stable constrained strings; runtime resource
IDs use UUIDs. C-01 must preserve this distinction in schemas. A YAML credential
leaf has exactly one of env or file; no inline secret values. Missing, empty or
unreadable references fail startup validation. Resolve references once at startup.
Credential file paths are explicitly configured server-side references; the API
cannot make the server read an arbitrary secret path as a credential lookup.

## Bootstrap environment

Parse once with caarlos0/env/v11 using typed structs. Generate this table's eventual
reference from code with g4s8/envdoc. These names are proposed until C-03 freezes
the bootstrap contract. Nested UI/tools modules are independently versioned.

| Variable | Default / meaning |
| --- | --- |
| MANAGERR_DATA_DIR | `/data`; persistent DB, generated key, descriptors and journals. |
| MANAGERR_CONFIG_FILE | Empty means API-managed config only; otherwise startup YAML file. |
| MANAGERR_LISTEN_ADDR | `:8080`; API listener in container. |
| MANAGERR_CREDENTIAL_KEY | Optional base64-encoded 32-byte credential encryption key. Secret, never logged. |
| MANAGERR_CREDENTIAL_KEY_FILE | Optional file containing the same base64 format. Mutually exclusive with key env. |
| MANAGERR_LOG_LEVEL | `info`; validated enum, sanitized structured logs. |
| MANAGERR_UI_API_URL | Required in BFF, e.g. `http://managerr-api:8080`. No embedded credentials. |
| MANAGERR_UI_LISTEN_ADDR | `:8081`; BFF listener. |
| MANAGERR_UI_PUBLIC_ORIGIN | Required absolute origin for canonicals, origin checks and metadata. Use configured HTTPS origin outside local development. |

No login/bearer/session-secret environment variables are introduced for v0.0.1.
Timeouts, worker bounds and storage policies belong in typed effective configuration;
use a small validated default set rather than one environment variable per method.
Scan interval default is 5 minutes, minimum 30 seconds; manual scans coalesce rather
than defeating admission limits. File stability needs two observations at least
30 seconds apart. These are selected defaults, not proofs a file is unused.

## Encryption key lifecycle

Precedence is explicit selection, not silent fallback. If both key env and key
file are set, fail startup. Validate supplied material exactly. If neither is
provided, use `<data-dir>/keys/credentials.key`, default
`/data/keys/credentials.key`. Generate 32 random bytes with crypto/rand on a fresh
instance, serialize base64 with newline, directory mode 0700 and file mode 0600.
Write atomically/exclusively, sync file and parent, handle another initializer by
reading the completed file, and never truncate an existing key.

Use a versioned AES-256-GCM envelope through Go's standard crypto library, fresh
nonce per encryption, and associated data binding connection ID and secret field
name. Store key fingerprint/version, not raw key, in DB metadata. Verify a key
check envelope before serving ready. Redact credentials in reads, updates, logs,
errors and audit. UI shows configured/source status and permits replacement; it
does not reveal saved plaintext. Static referenced secrets need not be copied
into SQLite; directly entered API/UI secrets are encrypted at rest.

If encrypted records or an initialized key-check envelope exist, missing/wrong key
is a startup failure. Do not generate a replacement. An explicitly configured but
unreadable/invalid key never falls back to a generated key. Switching key sources
works only with the same key until a dedicated rotation/migration feature exists.
Do not claim key rotation in v0.0.1. Generated key and database must share a backup
and persistence policy; the key file is a required persistent artifact.

Descriptors can contain tracker credentials. Retained bytes live in restricted
private storage under the data directory, protected by volume permissions. The
v0.0.1 encryption guarantee covers stored integration credentials, not encryption
of all media/descriptor files. Descriptor access is explicit and no-store.

## Deployment boundary

Build separate API and BFF binaries/images. Baseline target platforms are Linux
amd64 and arm64 OCI containers; validate both images rather than claiming every
OS/engine. Package examples for generic container execution and Kubernetes with
synthetic ConfigMaps/Secret references. Kubernetes is optional, and no operator or
live homelab manifest change is part of this specification task.

API needs writable persistent data storage and mounts for every managed source,
library and trash location. BFF needs API network access and no media mounts.
Support configurable UID/GID through container execution and filesystem permissions;
no privileged container or automatic chmod/chown of user media. Read-only media
mounts permit inventory but expose write actions as unsupported. Hardlinks require
actual shared filesystem identity, not paths that merely look related.

SQLite database storage must support locking, sync and atomicity tested by the
selected driver. NAS media mounts are supported subject to action capabilities;
a network-mounted SQLite database and multi-replica writers are not the baseline.
Graceful shutdown stops new dispatch, persists checkpoints and marks unresolved
attempts for startup reconciliation. Readiness does not depend on every upstream
being online. Resource limits and queue bounds must prevent scans starving API
requests; performance thresholds are measured in V-02.

No application authentication exists in v0.0.1. Deployments must control who can
reach these mutation APIs. The user's OIDC ingress is one option, not an app
feature. BFF rejects cross-origin mutation submissions using configured origin,
CSRF protections and conservative cookie/request handling where applicable. API
CORS is disabled by default, accepts mutation JSON only, and rejects browser
cross-origin mutation requests. These controls do not authenticate direct clients.
Do not trust Host or forwarded headers to construct origin/redirect URLs. TLS for
upstreams verifies certificates by default; private CAs can be explicitly mounted.
