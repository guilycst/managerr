# Acceptance matrix

These are required future implementation checks, not passing test results.
Use synthetic media such as Example Film and Example Series and disposable
upstream instances. Never download/seed real content to produce fixtures.

Each completed task records test command, exit status, tested commit, fixture or
upstream version, evidence path and independent review. A skipped or unavailable
check is not a pass. Fault injection must count actual effects, not only assert
a mocked method was called. Test native adapters in isolation after unit contracts.

| ID | Requirement | Area | Scenario | Required result |
| --- | --- | --- | --- | --- |
| A-01 | R-01 | Discovery | Read-only directory and client scans, including a historical orphan. | Zero upstream writes; orphan visible with unknown client/date, filesystem dates separate. |
| A-02 | R-01 | Discovery | Scan multiple roots, one unreadable, with interrupted pagination. | Per-source partial coverage; inaccessible data is unknown, not absent. |
| A-03 | R-01 | Discovery | Scheduled/manual overlapping triggers, restart and large directory. | One scan per root, bounded follow-up, persistent progress, no duplicate discoveries. |
| A-04 | R-02 | Provenance | qBittorrent hash variants, duplicate IDs in two instances, history removed. | Connection-scoped identity; retained evidence distinct from current observations. |
| A-05 | R-02 | Provenance | NZBGet queue/unpack/history with NZBID, history ID and drone parameter differing. | Ready only after processing; correct Arr correlation and mapped final path. |
| A-06 | R-02 | Descriptors | Original available, unavailable, then client history/payload purged. | Exact original digest retained where available; unavailable remains honest; no plaintext in ordinary responses/logs. |
| A-07 | R-03 | Inventory | Arr catalog exceeds one page and includes unmonitored/missing titles. | All records represented; no dependence on Managerr action history. |
| A-08 | R-03 | Inventory | Arr import succeeds while Jellyfin/Seerr are stale/offline. | Separate registration/import/availability/request dimensions and timestamps; no false availability. |
| A-09 | R-12 | Inventory | Same provider and numeric IDs across multiple Arr/Seerr/client instances. | No identity collision; explicit target selection and per-instance observations. |
| A-10 | R-04 | Matching | Ambiguous movie, anime absolute numbering, multi-episode file and season pack. | Suggestions editable; exact selected movie/episode IDs reach preview; ambiguity blocks approval. |
| A-11 | R-04 | Subtitles | Video plus forced/SDH/language subtitles, IDX/SUB pair and unmatched companion. | Editable explicit associations; labels/pairs preserved; no silent discard or scope expansion. |
| A-12 | R-06 | Approval | Register an unknown title and attempt to reuse first approval for import. | First approval permits registration only; exact later import requires a new decision. |
| A-13 | R-05 | Registration | New registration defaults and existing title with unspecified settings. | New unmonitored, no search; existing unspecified fields preserved; monitoring opt-in explicit. |
| A-14 | R-08 | Idempotency | Equivalent desired state submitted with same and different HTTP keys. | Same key returns same resource; fresh key observes already_satisfied with zero extra effects. |
| A-15 | R-06 | Approval | Expired preview, changed source/config/episode mapping, forged digest. | Conflict/validation response; zero dispatch; changed intent needs new plan. |
| A-16 | R-05 | Arr import | Native preview rejects or destination exists; command route bypasses native rejections. | Managerr refuses unsafe import; overwrite race guarantee tested or capability blocked. |
| A-17 | R-05 | Arr import | Command accepted/completed but only part of pack imported. | Per-file evidence; missing mappings unresolved; no aggregate false success. |
| A-18 | R-05 | Placement | Library-only copy/hardlink with no Arr registration. | Verified file placement and optional refresh; zero Arr registration writes. |
| A-19 | R-08 | Filesystem | Existing same size/name but different bytes; identical bytes different inode. | Copy conflicts on unequal content; hardlink not already_satisfied for a different inode. |
| A-20 | R-09 | Filesystem | Hardlink on different filesystem or unsupported mount. | No fallback; new copy approval includes storage cost; existing effects retained. |
| A-21 | R-09 | Filesystem | Traversal, symlink swap, special file, root deletion or path-prefix ambiguity. | Fail confined operation; zero writes outside selected configured roots. |
| A-22 | R-09 | Filesystem | Destination appears during copy/publish; source changes mid-copy; disk full. | No overwrite; incomplete staging identifiable; source preserved; actionable failure. |
| A-23 | R-09 | Filesystem | Directory gains/replaces child after exact manifest approval. | No unreviewed recursion; changed manifest needs review; no directory hardlink. |
| A-24 | R-09 | Filesystem | Cross-device move or trash and crash after copied destination. | Explicit copy-verify-delete steps; never destroy only verified copy; resumable per-file evidence. |
| A-25 | R-10 | Trash | Trash with default/custom retention and early janitor tick. | 30-day default; no purge before recorded expiry; startup/periodic eventual janitor. |
| A-26 | R-10 | Trash | UI permanent-delete attempt outside trash and direct API hard-delete plan. | UI enforces two-step flow; API supports explicit exact permanent action without waiting. |
| A-27 | R-10 | Trash | Concurrent restore/purge, collision, changed trash file and partial failure. | Only one claim; conflicting/replaced objects held; per-item remaining state recoverable. |
| A-28 | R-11 | Client control | Seeding torrent, zero upload speed, stop response lost and externally resumed state. | Do not equate zero speed/200 with stopped; reconcile or pause before payload mutation. |
| A-29 | R-11 | Client control | Partial multi-file torrent trash and later purge. | Stop whole torrent; retain entry in trash; remove with deleteFiles=false; unselected bytes remain. |
| A-30 | R-11 | Client control | Restore after partial purge removed a shared torrent record. | Restore remaining files safely, preserve association evidence; no auto re-add or resume. |
| A-31 | R-11 | Client control | Native qBittorrent file/folder rename and relocation versus display rename. | Read-back exact payload paths; reject native scope expansion/collision/unsupported behavior. |
| A-32 | R-07 | Durability | Crash at approval/queue transaction boundary. | Decision and queued run both exist or neither; dedupe prevents duplicates. |
| A-33 | R-08 | Durability | Crash/timeout after upstream accepted write before local result. | Restart in reconciliation; zero blind resubmission; read-back resolves or needs intervention. |
| A-34 | R-07 | Retries | Temporary outage lasts past preview expiry; restart during backoff. | Approved intent retained; bounded attempts and persisted backoff; no overall deadline unless configured. |
| A-35 | R-07 | Cancellation | Cancel/deadline before dispatch, during transfer and after upstream acceptance. | No later dispatch; cooperative stop; late effects and uncertainty visible without rollback claim. |
| A-36 | R-07 | Concurrency | Two actions overlap paths, hardlink objects, directory descendants or torrent IDs. | Local reservation/CAS prevents conflicting execution; external races remain separately gated. |
| A-37 | R-07 | Workflow | Close browser/BFF after registration; later step fails. | API continues until approval gate; completed effects retained and failed/blocked distinguished. |
| A-38 | R-13 | Configuration | API edit/delete static YAML resource, duplicate IDs and removed startup config. | Read-only errors; collisions fail startup; removed resources retire without deleting history. |
| A-39 | R-13 | Configuration | Change YAML and secret file without restart; update API-owned resource with stale ETag. | Static snapshot unchanged; restart applies atomically; stale managed write rejected. |
| A-40 | R-13 | Secrets | Generate first key, restart, explicit key env/file, both set and malformed key. | Stable path/mode/format; valid precedence rules; invalid configured key never falls back. |
| A-41 | R-13 | Secrets | Encrypted DB with missing/wrong key and edited ciphertext/associated connection. | Readiness fails; no replacement key; authenticated decryption rejects tampering. |
| A-42 | R-13 | Secrets | Credential save/read/error/log/HTML and malicious secret path reference. | Only encrypted managed credential storage; no plaintext leak or arbitrary secret-file read. |
| A-43 | R-14 | Database | Fresh migration, supported schema upgrade, interrupted migration and second executor. | Consistent schema/sqlc queries; failure prevents workers; second active process rejected. |
| A-44 | R-14 | Recovery | Consistent DB/key/descriptors/trash backup restored to isolated fixture instance. | Decrypts; resumes safe work; preserves uncertainty and idempotency; trash restore works. |
| A-45 | R-14 | Contracts | Regenerate strict API/client, sqlc, env docs and templ then check all modules. | Clean regeneration; independent GOWORK=off checks; no root tools/UI internal coupling. |
| A-46 | R-15 | No auth | Call API/BFF without credentials; forge actor/proxy header. | No login requirement; attribution remains unauthenticated; no trusted user invented. |
| A-47 | R-14 | Browser security | Cross-origin form/JSON mutation, forged Host/forwarded origin and redirect. | Conservative origin/CSRF/CORS policy; configured origin controls links; direct clients remain unauthenticated. |
| A-48 | R-07 | UI recovery | Transport failure, stale approval, repeated click, reload and back/forward. | Draft/context/focus preserved; same idempotency key; actual effect count stays correct. |
| A-49 | R-04 | UI review | Correct identity, episodes/subtitles; switch between similar media deep links. | URL, selection, displayed identity and executed payload agree; no stale detail reuse. |
| A-50 | R-14 | Accessibility | 390/1440 widths, themes/light/dark, keyboard, dialog escape, contrast and zoom. | Operable controls, labels, focus restoration, announced errors; no color-only states. |
| A-51 | R-14 | Metadata | Initial HTML on all routes, unknown ID, asset/preview URL and fixture origin. | Titles/descriptions/canonicals/OG/X present; generic private-safe image loads; useful shell 404. |
| A-52 | R-14 | Container | Linux amd64/arm64 API/BFF images, nonroot UID, read-only media and graceful shutdown. | No Kubernetes dependency or BFF media mount; honest action capabilities and durable recovery. |
| A-53 | R-14 | Capacity | Synthetic 100k file observations/10k catalog records with one slow/offline connector. | Bounded memory/queue/page sizes; API remains responsive; record actual timings/resources without invented SLA. |
| A-54 | R-03 | Seerr | Multi-page media/request lists, native partial availability and delayed scans. | Read-only connector; preserves native status and coverage; never creates/approves requests. |
| A-55 | R-05 | Jellyfin | Refresh accepted, library unavailable or wrong mapping, then later visible item. | Acceptance and availability separate; unsupported refresh scope blocked; mapping verified. |
| A-56 | R-08 | API | Same key changed payload, absent precondition, unknown field, oversized manifest. | Stable problem codes and statuses; zero unapproved effects; bounded request handling. |
| A-57 | R-10 | Janitor | Expired entry has unresolved client/payload effect or suspect clock change. | Hold or reconcile, never blind repeated delete; show reason and pending effect. |
| A-58 | R-14 | Release | Planning and code evidence compared to claimed scope and unresolved gates. | No release-ready claim with unresolved critical gate; publication/deployment authority separate. |
| A-59 | R-08 | Recovery | Crash after local destination publication before DB effect journal. | Identity/digest read-back resolves applied effect without overwriting or duplicating. |
| A-60 | R-05 | Extensibility | Add a typed test action via port/handler contract and call standalone versus workflow. | Same validation/idempotency/cancel semantics; no arbitrary commands or UI-only authority. |

## Evidence levels and completion

- Contract/unit checks prove local validation/state transitions against controlled
  ports. Fault fixtures record exact effect counts and crash boundaries.
- Disposable upstream integration checks prove behavior for pinned versions only.
  Native Arr overwrite/client relocation capabilities need these before enabling writes.
- Filesystem tests run on supported target OS/filesystems and exercise real atomic
  operations, EXDEV, permissions, sync and recovery. A temporary-directory happy path
  does not establish network-filesystem support.
- Browser checks exercise the generated HTTP client, actual BFF rendering and API
  validation. Include direct forged requests and transport interruption.
- Container and backup checks prove portable packaging and isolated recovery.
  Production playback/availability and ingress are separate live acceptance.

For each consequential browser action keep the adversarial ledger specified in
[dashboard](../specs/spec-001-media-reconciliation/dashboard.md). Record passed,
failed, blocked and not-run separately. No current application test is claimed by
this planning repository. The planning validator only checks document structure.
