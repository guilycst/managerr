# Mastarr NZBGet client

`github.com/guilycst/mastarr/clients/nzbget` is a small, read-only Go client
for the NZBGet JSON-RPC API. It is an independent module: it does not import
Mastarr's root module, domain, ports, adapters, storage, workflow, or
infrastructure packages. The later X-13 lane translates these observations
into Mastarr's download inventory port.

## Compatibility

The compatibility document in [`openrpc.json`](openrpc.json) is owned by
Mastarr and is intentionally limited to the initial read surface:

- `version()`
- `listgroups(NumberOfLogEntries)`
- `listfiles(IDFrom, IDTo, NZBID)`
- `history(Hidden)`

The base API is documented for NZBGet 13 and later. Fields introduced by later
versions are optional in the generated DTOs and remain nil when an older
server omits them. The client reports the server's version through `Version`
and does not claim a product build or feature that the response did not
provide. The schema is based on the pinned NZBGet API source at
[b609226e18da11955ce8dda2c7df959258655579](https://github.com/nzbgetcom/nzbget/tree/b609226e18da11955ce8dda2c7df959258655579/docs/api).

NZBGet uses a JSON-RPC 1.1 envelope whose protocol member is named `version`.
All arguments are positional and are sent in the order shown above. The
client sends an empty JSON array for `version`; it sends `0` for the required
deprecated `NumberOfLogEntries`, `IDFrom`, and `IDTo` arguments. It never calls
`rpc.discover`, because the supported NZBGet API does not provide introspection.
The request ID is a process-local, monotonically increasing unsigned integer
and a response with a different ID or envelope version is rejected.

`NZBID` is the canonical NZB identity. History's deprecated `ID` is retained
as an alias and is accepted as a fallback only when `NZBID` is absent. A
positive disagreement between the two aliases is malformed evidence. `Hi`
and `Lo` fields are retained and combined into an unsigned 64-bit convenience
field without narrowing. Version-specific or nullable upstream fields use
pointers in generated DTOs and normalized observations.

## Transport bounds and errors

`New` accepts an absolute HTTP or HTTPS endpoint. A missing path gets
`/jsonrpc`; embedded credentials, query strings, and fragments are rejected.
Endpoint paths retain clean custom prefixes exactly, while dot segments,
encoded dot segments, encoded slashes or backslashes, and repeated separators
are rejected. Credentials are sent only with HTTP Basic authentication.
Requests use a 15-second deadline by default when the caller has not supplied
one. Response bodies are bounded to 8 MiB by default and may be configured up
to 64 MiB. Redirects to another origin are not followed.

Failures are returned as `*UpstreamError`, with a stable `ErrorKind`, method,
HTTP status or JSON-RPC code, and a sanitized detail. Raw response bodies,
upstream messages, endpoint URLs, and credentials are not included in errors.
Non-success HTTP status is classified before response bytes are retained.
Context cancellation and deadlines remain discoverable with `errors.Is`, while
raw transport errors are never in the public error chain. JSON-RPC responses
must contain one non-null `version`, one non-null `id`, and exactly one valid
`result` or `error` member; duplicate object members are rejected.

Locally refused but syntactically safe methods, including `rpc.discover` and
mutations, return `ErrorUnsupported` without a network request. Malformed
method names and invalid arguments return `ErrorInvalidInput`.

The convenience methods (`Version`, `ListGroups`, `ListFiles`, and `History`)
return normalized observations. `Client.Methods()` exposes generated wire
wrappers for consumers that need the exact DTOs. No mutation methods,
queue-control operations, descriptor lookup, or automatic retry is included.
A caller that receives a timeout or an unavailable error must reconcile by
reading again before attempting any later write in another boundary.

## Generation

`generated.go` is produced from `openrpc.json`; do not edit it by hand. From
the repository root, run:

```sh
(cd tools && GOWORK=off go run ./internal/nzbgetgen \
  -input ../clients/nzbget/openrpc.json \
  -output ../clients/nzbget/generated.go)
```

The module also records the same command in `generate.go` for `go generate`.
The tools-module generator pins OpenRPC 1.2.6, requires every positional
parameter, rejects OpenAPI's `nullable` keyword, validates Draft 7 type unions,
and checks the exact method set, positional parameter mode, and schema
references. Its tests generate twice and compare both outputs with the
committed file to keep the output reproducible.
The `check-generation.sh` helper performs the same comparison from a temporary
working directory without modifying the checkout:

```sh
./check-generation.sh
```
