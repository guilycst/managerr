# D-02 independent review, round three

## Decision

`approved`.

Product commit `f931a1256bd56a9aa373e295bbbbb3b1eaa7adef`
closes the remaining descriptor race within the documented portable filesystem
ceiling. Explicit key-file type validation and bounded reading now use the same
opened descriptor. Projected Secret compatibility, stable FIFO/directory
rejection, generated-key path confinement, AEAD binding, no-regeneration, and
redaction remain intact. No open D-02 finding remains.

## Review identity and scope

- Reviewer: `/root/d02_reviewer`, independent of product author
  `/root/d02_implementer`.
- Reviewed correction product:
  `f931a1256bd56a9aa373e295bbbbb3b1eaa7adef`; direct parent
  `75607fc674f449732ebbd62fb2b51e22be57eb15`; tree
  `b5d53eaa748d90c789c27bd1d956036f1186f0d6`.
- Reviewed correction handoff:
  `4048c7ee5035ead345a919e4ffa21ec6e289eef4`; product ancestor with
  only the independent X-05 review receipt between them; tree
  `d1136e391d2a82bc28a5f860ce1398f683557bc9`.
- Coordinator review checkpoint:
  `484419e329bbcdcd4d768590c889c118a683ecf6`; tree
  `712cb3a721d29207a6c3927fc37f60d2ce73b332`. No D-02 product byte
  changed between the product and this checkpoint.
- Round-one and round-two receipts:
  `ad6a2bc7d8e6944a5001cbadf39287af746e64cf` and
  `412e014d02be2807f34f3762f7f21500f3560562`.
- Correction scope: `internal/credentials/credentials.go` and
  `credentials_unix_test.go`.
- Scoped correction diff SHA-256:
  `30d888e8ea64e5685204ae9c7ff93d5fb61378aa939a3a6c1e602f5bb5898d3b`.
- Acceptance reviewed: A-40, A-41, and A-42.

Executable checks ran in a clean detached clone at the exact product commit.
Disposable adversarial tests existed only outside the product tree. No live
service, credential, private endpoint, media, upstream write, release, or
deployment was used.

## Finding closure

`readKeyFile` keeps the path-level `os.Stat` needed to reject a stable FIFO
before blocking in `os.Open`. After opening, it passes that one descriptor to
`readBoundedDescriptor`. The helper runs `file.Stat`, requires a regular file,
and reads at most 4 KiB plus one byte from the same descriptor. A rename and
replacement regression proves post-open path changes cannot change consumed
bytes.

The remaining pre-open window is stated accurately: a hostile local replacement
between path stat and open can still be followed or block if it becomes a FIFO.
The product claims no portable no-follow/nonblocking guarantee. This matches the
local-filesystem trust ceiling accepted in round two and does not weaken stable
invalid-source rejection.

Round-one closures remain valid:

- Kubernetes AtomicWriter `..data` symlinks resolve to bounded regular files.
- Stable directory and FIFO sources fail with no generated fallback.
- Symlinked or non-directory persistent `keys` paths fail before chmod or key
  creation, with no outside permission or content mutation.
- Persistent reads also use descriptor-bound regular-file validation through
  the shared helper.

## Preserved behavior

- AES-256-GCM uses fresh nonces and length-prefixed connection/field associated
  data. Wrong keys, tampered envelopes, and substituted bindings fail safely.
- Key-check envelopes authenticate the active key without exposing plaintext.
- Missing, empty, malformed, and existing-data-without-key states never create
  a replacement. Concurrent first initialization converges on one key.
- Generated key format, 0600 file mode, 0700 directory mode, exclusive publish,
  file sync, and parent sync remain unchanged.
- Credential metadata and errors expose no managed secret or ciphertext.
- D-03/C-04 still own raw environment-presence binding, persisted key-check
  readiness, storage translation, and API/log/HTML end-to-end verification.

## Independent checks

| Command or scenario | Result |
| --- | --- |
| Exact product/handoff/checkpoint identity, ancestry, owned diff and clean status | Passed. |
| Descriptor replacement, projected Secret, directory/FIFO, and generated-path regressions plus external oversized/type probes, `-count=100` | Passed. |
| External wrong-key and repeated-encryption probe, `-count=100` | Passed. |
| `GOWORK=off go test -count=50 ./internal/credentials` | Passed. |
| `GOWORK=off go test -race -count=10 ./internal/credentials` | Passed. |
| `GOWORK=off go vet ./internal/credentials` and `GOWORK=off go mod verify` | Passed; all modules verified. |
| CGO-free Linux amd64 and arm64 compile-only focused tests | Passed. |
| `./scripts/check-guardrails.sh --ci` at exact product | Passed; generation, Vacuum, architecture, lint, root/UI/tools/client tests, vet, and module verification passed. |
| CodeRabbit 0.7.6 correction review | Completed with zero findings. |

## Acceptance disposition

- A-40: accepted for D-02. Stable generated and explicit sources, projected
  Secret compatibility, modes, format, concurrency, invalid-source behavior,
  and no fallback pass within the documented pre-open race assumption.
- A-41: accepted for D-02's local crypto contribution. Wrong key, tampering,
  and binding substitution fail without replacement. Database readiness remains
  D-03/C-04 scope.
- A-42: accepted for D-02's envelope, descriptor-bound secret-file, and
  redaction contribution. API/YAML authority and end-to-end HTML/log checks
  remain downstream.

No product file, task definition, shared script, module manifest, or
`docs/execution/state.json` was modified by the reviewer.
