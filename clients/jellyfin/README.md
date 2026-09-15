# Mastarr Jellyfin client

`github.com/guilycst/mastarr/clients/jellyfin` is an independent Go module
that owns the narrow Jellyfin HTTP compatibility boundary used by Mastarr.
It does not import the Mastarr root module, domain, ports, storage, workflow,
adapters or another client module. The root adapter translates these public
observations into Mastarr records.

The module covers the read surfaces needed for reconciliation:

- `GET /System/Info/Public` for product and version observation;
- `GET /Library/MediaFolders`, with a read-only user-view fallback, for library
  identities;
- `GET /Items` for bounded item pages and provider-ID relationships; and
- explicit `POST /Library/Refresh` and `POST /Items/{itemId}/Refresh` calls
  whose result records native request acceptance only.

Refresh acceptance is never library availability. Callers must perform a later
item or inventory read and retain that observation separately. Item refresh is
represented in the compatibility contract but remains a capability decision
for the caller until a versioned upstream fixture proves it; an unsupported
route is returned as a typed `unsupported` error.

The client sends a Jellyfin token in `X-Emby-Token`, refuses redirects, applies
per-request deadlines, bounds response and collection sizes, rejects malformed
JSON including duplicate keys and trailing values, validates required native
identities, and returns sanitized typed upstream errors. Unknown upstream
fields are ignored at the generated boundary and never leak through the
normalized public observations. Provider namespaces and IDs are copied and
sorted for stable, instance-local translation.

The contract is Mastarr-owned OpenAPI 3.1.1 in `openapi.yaml`. Generated output
is committed at `internal/generated/client.gen.go` and is never hand-edited.
The contract follows the Jellyfin 10.x HTTP shape, but no specific Jellyfin
release is currently pinned in Mastarr's compatibility matrix. Successful
system information therefore proves only that observation; runtime capability
enablement and deployment-specific path/mount mapping remain integration gates.

Generate and check the module offline after the repository tools module is
warmed:

```sh
GOWORK=off GOPROXY=off GOSUMDB=off go generate ./...
GOWORK=off GOPROXY=off GOSUMDB=off ./check-generation.sh
```

The tests use only synthetic `httptest` handlers. They do not contact a live
Jellyfin service or perform media/filesystem actions.
