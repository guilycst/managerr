# D-02 independent review, round one

## Decision

`changes_requested`.

AES-256-GCM encryption, fresh nonces, length-prefixed connection/field
associated data, wrong-key rejection, key checks, redacted metadata, exclusive
publication, file and directory sync, and no-regeneration behavior pass. Two
path-handling failures remain: the explicit key-file reader rejects Kubernetes
projected Secret files, while generated-key creation follows a symlinked
`keys/` directory outside the configured data directory.

## Review identity and scope

- Reviewer: `/root/d02_reviewer`, independent of product author
  `/root/d02_implementer`.
- Reviewed product: `8ff358be55891e7b7a588e76a57bd3e7d8ce661c`;
  direct parent `f772cdd066a8c10c147e1da30e73702d59e6ce68`;
  tree `ce7d1641c00f4d21280a258c8a79e17c604ea81e`.
- Reviewed handoff: `47b1dfaaa6d6ba000d7bdc36e26490eab2925b51`;
  direct child of the product; tree
  `1a3e6d86448490b112004497e3e466fff892e512`.
- Coordinator state checkpoint:
  `a642adcda155f0f7b6044deb11663ef61b708cc3`; direct child of the
  handoff; tree `364bf2d0be8a23323b5f6baa88a04cc84d2fd9a2`.
- Product scope: `internal/credentials/credentials.go` and
  `internal/credentials/credentials_test.go`; no other path changed.
- Scoped product diff SHA-256:
  `832e15f794f41c61ba12ce9e324962c093aec840b66268e31ff0610842cde602`.
- Acceptance reviewed: A-40, A-41, and A-42.

Executable review ran in a clean detached clone at the exact product commit.
Disposable adversarial tests existed only in that external clone. No live
service, credential, private endpoint, media, upstream write, release, or
deployment was used.

## Findings

### P1: explicit key files reject Kubernetes projected Secrets

`readKeyFile` calls `os.Lstat` and requires the configured path itself to be a
regular file at `internal/credentials/credentials.go:453-461`. Kubernetes
Secret and projected volumes use the Kubernetes AtomicWriter; visible files
are symlinks into a timestamped data directory. The project targets Kubernetes
and specifies `MASTARR_CREDENTIAL_KEY_FILE`, so the normal mounted-Secret form
must work. Kubernetes documents the layout in its
[AtomicWriter source](https://github.com/kubernetes/kubernetes/blob/master/pkg/volume/util/atomic_writer.go).

An external synthetic probe created a base64 32-byte key as a regular file,
created an absolute configured symlink to it using the projected-volume shape,
then called `LoadKey` with `KeyFileProvided=true`. It failed:

```text
projected secret symlink rejected: credential key is invalid
```

Required correction: explicit operator-configured key-file paths may follow a
symlink, but the resolved target must be a bounded regular file. Preserve
sanitized errors, exact base64/length validation, and no generated-key fallback.
Add a deterministic projected-Secret symlink regression plus directory/FIFO
rejections.

### P1: generated-key creation escapes through a symlinked keys directory

`createPersistentKey` runs `MkdirAll` and then `Chmod` on
`<data-dir>/keys` without rejecting a symlink at
`internal/credentials/credentials.go:492-505`. If `keys` is a symlink to an
external directory, both `Chmod` and temporary/canonical key creation occur in
that external directory. This violates the promised stable path under
`MASTARR_DATA_DIR` and can mutate permissions and create a credential key
outside persistent storage.

An external synthetic probe made `<data-dir>/keys` a symlink to another
temporary directory. `LoadKey` succeeded and created
`credentials.key` in the external target:

```text
generated key escaped data directory through keys symlink
```

Required correction: generated-key creation must reject a symlinked or
non-directory `keys` component before chmod or file creation and retain
exclusive publication. Add a regression proving zero mutation outside the
configured data directory. Use a directory-relative, no-follow operation if
the supported platform contract requires resistance to concurrent replacement;
otherwise record the remaining local-filesystem trust assumption explicitly.

## Verified behavior

- Wrong manager key returns `ErrAuthenticationFailed`; edited ciphertext,
  fingerprint, version, connection ID, and field binding fail without plaintext.
- Two encryptions of the same value produced distinct nonces and ciphertext.
- Key-check verification succeeds only with the authentic envelope.
- Empty explicit sources fail when their `Provided` flag is set; malformed,
  missing, and existing-data-without-key cases do not create a replacement.
- Concurrent first initialization converges on one fingerprint. Fifty focused
  repetitions and ten race-enabled repetitions passed.
- Generated canonical files use base64 plus newline, mode 0600, directory mode
  0700, file sync, exclusive hard-link publication, and parent sync.
- Credential metadata excludes ciphertext and plaintext; public errors do not
  contain supplied material. No credential-shaped private value or coordinate
  exists in the product or handoff.
- `EnvironmentProvided`, `KeyFileProvided`, and `ExistingCredentialData` are
  necessary downstream inputs. D-03/C-04 must derive presence from raw startup
  configuration and persisted key-check/encrypted-row state; string values alone
  cannot distinguish unset from explicitly empty.

## Independent checks

| Command or scenario | Result |
| --- | --- |
| Exact commit/tree/ancestry, owned-path diff and clean detached status | Passed. |
| CodeRabbit 0.7.6 review of exact product against dispatch base | Completed with zero additional findings. |
| `GOWORK=off go test -count=50 ./internal/credentials` | Passed. |
| `GOWORK=off go test -race -count=10 ./internal/credentials` | Passed. |
| Wrong-key and repeated-encryption external probe | Passed. |
| Projected Secret symlink probe | Failed as finding one records. |
| Generated directory symlink escape probe | Failed as finding two records. |
| `GOWORK=off go vet ./internal/credentials` and `GOWORK=off go mod verify` | Passed; all modules verified. |
| CGO-free Linux amd64 and arm64 compile-only focused tests | Passed. |
| `./scripts/check-guardrails.sh --ci` at exact product | Passed; generation, Vacuum, architecture, lint, root/UI/tools/client tests, vet, and module verification all passed. |
| Public secret/private-coordinate scan | Passed; only field names, synthetic values, and sanitized messages matched. |

## Acceptance disposition

- A-40: not accepted. Generated-key format, modes, restart, concurrency,
  selection, and no-fallback behavior pass, but the explicit Kubernetes key-file
  path fails and generated path confinement is not enforced.
- A-41: accepted for D-02's local crypto contribution. Wrong key, envelope
  tampering, and connection/field substitution fail authentication without key
  replacement. DB readiness wiring remains D-03/C-04 scope.
- A-42: accepted for D-02's envelope and redaction contribution. Managed values
  are encrypted and ordinary metadata/errors expose no plaintext. API/YAML
  secret-path authority and HTML/log behavior remain downstream checks.

No product file, task definition, shared script, module manifest, or
`docs/execution/state.json` was modified by the reviewer.
