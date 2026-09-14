# D-02 independent review, round two

## Decision

`changes_requested`.

Both round-one failures are closed for stable filesystem state. Kubernetes
projected Secret symlinks now resolve to regular key files, and a symlinked or
non-directory persistent `keys` path is rejected without outside mutation.
One time-of-check/time-of-use failure remains in the explicit key-file reader:
it validates one path target, then opens and reads the path again without
validating the opened object.

## Review identity and scope

- Reviewer: `/root/d02_reviewer`, independent of product author
  `/root/d02_implementer`.
- Reviewed correction product:
  `0ee6f0a0f1046d7db44e8bb3e59e46b005037ec5`; direct parent
  `abbe01bb416fc508569cb766f771b9da87f16bae`; tree
  `054fb4e20d674f25519afda7ed59a00bbdb2c14e`.
- Reviewed correction handoff:
  `93420af525440b8ad81df31af1b22f9276d89fb2`; direct child of the
  product; tree `db3542a6f1cc9d5651fdea53ab778845d26e8b53`.
- Round-one receipt:
  `ad6a2bc7d8e6944a5001cbadf39287af746e64cf`.
- Correction scope: `internal/credentials/credentials.go`,
  `credentials_test.go`, and `credentials_unix_test.go`.
- Scoped correction diff SHA-256:
  `98c9e4162d283072ba817d8a6ffa52ab8436d74848ad0756f715f6e08b2dd547`.
- Acceptance reviewed: A-40, A-41, and A-42.

Checks ran in a clean detached clone at the exact product commit. Shared X-05
correction files were neither read as evidence nor changed. No live service,
credential, private endpoint, media, upstream write, release, or deployment was
used.

## Round-one finding closure

### Kubernetes projected Secret support: functionally closed

`readKeyFile` now follows an explicitly configured symlink with `os.Stat` and
accepts its regular target. The committed fixture uses the Kubernetes
AtomicWriter `..data` and timestamp-directory layout. One hundred repetitions
passed. Stable directory and FIFO targets return `ErrInvalidKey`, and no
generated fallback appears.

### Persistent keys-directory confinement: closed within documented scope

Persistent load and generation now reject a symlink or non-directory
`<data-dir>/keys` before chmod or publication. The committed regression proves
no external key creation, no external permission change, and no sentinel-file
change. One hundred repetitions passed.

The source documents its portable ceiling: hostile concurrent directory
replacement between validation and mutation is outside the standard-library
guarantee. This matches the remedy permitted by round one. Complete hostile
local-filesystem race resistance would require directory-handle-relative,
no-follow operations and is not claimed by this product.

## Finding

### P1: explicit key validation and reading use different path resolutions

At `internal/credentials/credentials.go:456-466`, `os.Stat(path)` validates the
object reached by the configured path, then `readBoundedFile(path)` performs a
second path lookup and open. The opened object is never checked with
`file.Stat`. A replacement between these operations can therefore make the
reader consume an object different from the validated regular file. A swapped
FIFO can block startup in `os.Open`; a swapped directory or device reaches the
read path despite the regular-file policy.

This conflicts with the correction's stated rule that projected symlinks are
followed only to a bounded regular target. A-40 requires invalid key sources to
fail startup without fallback, and A-42 requires the secret-file boundary to
avoid arbitrary reads. The documented generated-directory race assumption does
not currently cover this explicit source, and a file-descriptor check is
available in the standard library.

Required correction: keep the pre-open type check that prevents a stable FIFO
from blocking, but read through one opened file descriptor and validate that
descriptor with `file.Stat` before consuming bytes. Preserve the 4 KiB bound,
regular-file rule, projected-symlink compatibility, sanitized errors, and zero
generated fallback. Document any remaining hostile replacement window,
including FIFO-open behavior, if the portable implementation intentionally
accepts it. Add a regression around the descriptor-based helper so type
validation and bounded reading cannot drift apart.

## Preserved behavior

- AES-256-GCM, fresh nonces, key fingerprints, version checks, key-check
  authentication, and length-prefixed connection/field AAD remain unchanged.
- Wrong key, modified ciphertext/fingerprint/version, and substituted binding
  return sanitized authentication or envelope failures.
- Missing, empty, malformed, and existing-data-without-key sources do not create
  replacement keys. Concurrent first initialization still converges on one key.
- Redacted metadata contains no plaintext or encrypted payload.
- Persistent key mode, format, exclusive link publication, file sync, and parent
  sync remain intact.

## Independent checks

| Command or scenario | Result |
| --- | --- |
| Exact product/handoff ancestry, correction path scope, diff hash, and clean detached status | Passed. |
| Four correction regressions, `-count=100` | Passed: projected Secret, directory/FIFO rejection, and persistent path confinement. |
| `GOWORK=off go test -count=50 ./internal/credentials` | Passed. |
| `GOWORK=off go test -race -count=10 ./internal/credentials` | Passed. |
| `GOWORK=off go vet ./internal/credentials` and `GOWORK=off go mod verify` | Passed; all modules verified. |
| CGO-free Linux amd64 and arm64 compile-only focused tests | Passed. |
| `./scripts/check-guardrails.sh --ci` at exact product | Passed; generation, Vacuum, architecture, lint, all module tests, vet, and module verification passed. |
| CodeRabbit 0.7.6 correction review | Reported the explicit-file descriptor race and the documented persistent-directory race. Independent review retains the first and accepts the second only within the recorded portability ceiling. |

## Acceptance disposition

- A-40: not accepted for D-02. Both original stable-path defects are fixed, but
  explicit key-file validation is not bound to the object actually read.
- A-41: remains accepted for D-02's local cryptographic contribution. Database
  readiness and persisted key-check wiring remain D-03/C-04 scope.
- A-42: remains accepted for D-02's envelope/redaction contribution, but final
  explicit secret-file boundary acceptance waits for the descriptor race fix.

No product file, task definition, shared script, module manifest, or
`docs/execution/state.json` was modified by the reviewer.
