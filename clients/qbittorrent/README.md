# Mastarr qBittorrent read client

This directory is an independent Go module at
`github.com/guilycst/mastarr/clients/qbittorrent`. It freezes the small
qBittorrent WebUI API v2 compatibility surface needed by Mastarr inventory
work. It does not import the Mastarr root module or any domain, port, storage,
workflow or adapter package.

The compatibility candidate is qBittorrent **5.0+** with WebAPI **2.x**. The
connected qBittorrent product build is still runtime evidence; this module does
not infer a build from the API documentation. The contract is
[`openapi.yaml`](openapi.yaml), based on the pinned qBittorrent 5 WebUI API
evidence recorded in `docs/research/upstream-evidence.md` (wiki page commit
`cc6579ab58f534d03b40ae0deba5755c4a342319`, blob
`82f67b6f5aa73ef54d4c88dcd38c695ae1191b72`).

The handwritten client exposes only these operations:

- cookie login through `POST /api/v2/auth/login`;
- application and WebAPI version reads;
- torrent inventory with qBittorrent's filter, category, tag, sort, reverse,
  limit, offset and hashes query fields;
- generic properties and file contents for one torrent hash;
- all categories and all tags.

Login accepts the upstream `Ok.` body only when the response also establishes a
nonempty, usable `SID` cookie for the configured origin. A client requires a
nonempty UTF-8 username (at most 256 characters) and password (at most 1024
characters); there is no credential-less login mode. Requests carry the
configured origin's `Origin` and `Referer` headers, and redirects are rejected
so a session-bearing request cannot be sent to another location. Credentials
are held only in the client configuration and login form. Upstream body text is
never copied to an error; failures are classified by the typed `UpstreamError` codes
`unavailable`, `rate_limited`, `unauthorized`, `invalid_input`, `conflict`,
`unsupported` and `unknown`.

Only the documented HTTP 200 response is accepted for login and each read;
other 2xx statuses are typed as unsupported before body decoding. JSON
responses are decoded with exact case-sensitive field names, duplicate-member
rejection, raw UTF-8 validation, unknown-field rejection, trailing-data checks,
and presence validation for every OpenAPI-required member of inventory,
properties, file and category objects. Null object values and null required
members are rejected. The default response bound is 8 MiB, with at most 10,000 inventory
records, 100,000 files, or 10,000 category/tag values. Bounds reject an
over-limit response instead of silently truncating evidence. qBittorrent Unix
timestamps and its `-1` unknown sentinels are preserved in the normalized
values. Every inventory record must carry one 40-character v1 or 64-character
v2 hexadecimal hash; one malformed or duplicate logical identity rejects the
complete observation. Concurrent authentication is single-flight: callers
joining one attempt observe that attempt's result, while a later call can
deliberately retry after it drains. Callers waiting for it retain context
cancellation semantics.
Inventory filter and sort values are limited to 64 characters, category
and tag values to 512 characters. Hash list members are at most 128 characters,
cannot contain `|`, whitespace, controls or invalid UTF-8, cannot repeat, and
the decoded joined value including separators is capped at 4096 bytes. Both
40-character and 64-character hash identities are accepted; duplicate hex
identities are compared case-insensitively while the original spelling is sent
upstream. Injected HTTP clients with zero or negative timeouts use the safe
15-second default, while positive timeouts are preserved.

Application versions must be one whitespace-free `vMAJOR.MINOR.PATCH` token;
WebAPI versions must be one whitespace-free `MAJOR.MINOR` or
`MAJOR.MINOR.PATCH` token. Either accepts qBittorrent-style alphanumeric
prerelease/build suffixes such as `-alpha1`, `beta1`, `-rc1` and `+git`; empty,
multiline, control, HTML and trailing-token responses are rejected. File piece
ranges are inclusive, nonnegative `[start, end]` pairs with `start <= end`; no
sentinel is accepted. Version-route 404/405/501 and undocumented successful
status responses are typed as `unsupported`, while a torrent resource 404
remains `unavailable`.

The configured endpoint may contain a literal reverse-proxy path prefix, but
dot segments, percent-encoded path bytes, backslashes and repeated separators
are rejected so request URI interpretation remains stable across proxies.

The generated models and typed operation request builders in
`generated/client.gen.go` come from oapi-codegen **v2.8.0**. Regenerate from
this directory with:

```sh
GOWORK=off go generate ./...
```

Check reproducibility without modifying the checkout with:

```sh
./check-generation.sh
```

Run module checks with:

```sh
GOWORK=off go test ./...
GOWORK=off go test -race ./...
GOWORK=off go vet ./...
GOWORK=off go mod verify
```

Mutation routes such as stop, resume, relocation, rename, category/tag
changes, deletion and torrent creation are intentionally absent. Descriptor
export, client-specific scope translation and the Mastarr root adapter remain
separate migration work; this module supplies only typed, read-only upstream
evidence.
