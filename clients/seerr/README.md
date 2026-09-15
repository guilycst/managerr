# Mastarr Seerr client

`github.com/guilycst/mastarr/clients/seerr` is an independent Go module for
the read boundary Mastarr uses to observe Seerr. It owns the HTTP transport,
credential handling, generated compatibility types, bounded JSON decoding,
pagination state and sanitized upstream errors. It does not import Mastarr's
root module, domain, ports, storage, workflow or adapters, and generated types
never leave this module's handwritten normalizer.

The compatibility contract is the versioned, Mastarr-owned
[`openapi.yaml`](openapi.yaml). The initial surface is deliberately narrow:

- `GET /api/v1/status` for product and version observation;
- `GET /api/v1/media?take=&skip=` for media records, provider IDs, native media
  status, nested requests and seasons, Arr service relationships, Jellyfin
  identifiers and availability; and
- `GET /api/v1/request?take=&skip=` for request status, nested media evidence,
  seasons and typed `serviceErrors`.

The client accepts an endpoint with an optional reverse-proxy path prefix. It
sends `X-Api-Key` when `Config.APIKey` is configured, or
`Authorization: Bearer ...` for `BearerToken`, `Token` or `AuthToken`. The
credential is never included in errors, `Endpoint`, `String`, cursors or
generated public observations. Redirects are returned as errors so a
credential-bearing request cannot be replayed at another host.

Seerr uses mutable offset pagination. The client bounds response bytes, page
size, page count, records, nesting and cursor state. It requires native
`pageInfo` metadata before it can continue safely, detects contradictory or
changing totals, preserves overlap and short-page evidence, and never reports
an incomplete or multi-page traversal as a complete immutable snapshot. Use
`ListMedia`/`ListRequests` for one page, their authenticated `NextCursor` for
continuation, or `ListAllMedia`/`ListAllRequests` for bounded traversal.

Native status numbers and their normalized names are both retained. A status
of `partially_available` remains distinct from `available`, request status,
and Jellyfin library availability. Missing status, nested identity or page
metadata remains evidence of uncertainty.

This v0.0.1 client is read-only. It has no request creation, approval,
cancellation, deletion, search, command, or discovery method. Seerr writes
are intentionally deferred to a separately reviewed compatibility extension.
The module's tests use synthetic `httptest` servers only.

Generated output is committed at `internal/generated/client.gen.go` and is
never hand-edited. With the repository tools module warmed, generation and the
reproducibility check run offline:

```sh
GOWORK=off GOPROXY=off GOSUMDB=off go generate ./...
GOWORK=off GOPROXY=off GOSUMDB=off ./check-generation.sh
```

The supported compatibility candidate is the inspected Seerr HTTP API shape
used by Seerr 2.x and compatible Overseerr releases. Mastarr has not yet
frozen a product-version fixture for this module, so a successful status read
does not by itself enable a runtime capability. The coordinator's C-06 lane
must add this module to the repository generation, lint and CI matrices after
the module is published without a local `replace` directive.
