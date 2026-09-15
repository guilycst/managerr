# Mastarr Radarr client

`github.com/guilycst/mastarr/clients/radarr` is an independent Go module for
Mastarr's narrow, read-only Radarr v3 compatibility boundary. It has no import
path to the Mastarr root module, domain, ports, storage, workflow, adapters or
another upstream client. The root Arr adapter owns translation from these
observations into Mastarr contracts.

The Mastarr-owned [`openapi.yaml`](openapi.yaml) is OpenAPI 3.1.1 and covers:

- `GET /api/v3/system/status` for the Radarr version and status;
- `GET /api/v3/movie` and `GET /api/v3/movie/{id}` for the catalog;
- `GET /api/v3/rootfolder` and `GET /api/v3/qualityprofile` for options;
- `GET /api/v3/moviefile?movieId=...` for movie-file observations;
- `GET /api/v3/manualimport` for native, read-only import previews.

The candidate compatibility range is Radarr 3.x and its `/api/v3` API. This
module does not infer support from a version string: callers retain the status
observation and decide which capability evidence is sufficient. Registration,
manual-import execution, command dispatch, history, lookup, search and every
other write remain future contract work. The preview endpoint is a GET and
never executes an import.

`New` accepts an absolute HTTP or HTTPS endpoint and appends `/api/v3`; a
reverse-proxy path prefix is preserved. Embedded credentials, queries,
fragments, encoded path bytes, dot segments, backslashes and repeated path
separators are rejected. A nonempty API key is sent only in the `X-Api-Key`
header. The HTTP client is cloned, cookie state is disabled, redirects are
rejected, and a request context gets a 15-second deadline unless the caller
already supplied one or configured another timeout. API keys and upstream body
text are never placed in errors or `Client.String()` output.

Every response body is bounded before decoding. Radarr's catalog, options,
movie-file and preview endpoints return full arrays rather than cursors, so the
client reports a complete `Page[T]` only after retaining the complete bounded
array. It leaves `NextCursor` empty rather than presenting a later mutable
request as a continuation. An over-bound response is rejected. Required
identity and path fields, duplicate identities, movie scope, nested movie-file
identity, duplicate JSON members, invalid UTF-8, trailing JSON, and bounded
native text are checked. Unknown upstream members are accepted for forward
compatibility but remain in generated DTO storage only; normalized public
observations copy only the typed fields in this module.

Radarr's native manual-import query has two meanings. `PreviewManualImport`
requires a downloaded folder and can carry a native `downloadId`; it rejects a
movie ID so a source-folder preview cannot silently become a registered-library
scan. `PreviewLibraryImport` (also available as `PreviewMovieImport`) sends
only a movie ID and verifies every returned movie association. Candidates
preserve exact paths, sizes, movie identity, movie-file identity, typed
quality/language/release data and native rejection `reason` text. No subtitle
pairing or filesystem/path mapping is inferred. Callers still need an explicit
reviewed association before any later root workflow action.

Generated code is produced by pinned `oapi-codegen` v2.8.0 and lives under
`internal/generated`; it is committed and never hand-edited. From this
directory:

```sh
GOWORK=off GOPROXY=off GOSUMDB=off go generate ./...
GOWORK=off GOPROXY=off GOSUMDB=off ./check-generation.sh
GOWORK=off go test ./...
GOWORK=off go test -race ./...
GOWORK=off go vet ./...
GOWORK=off go mod verify
```

The module uses synthetic `httptest` fixtures only. No credentials, private
service coordinates, live inventories, tracker data or media files belong in
this repository.
