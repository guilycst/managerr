# Mastarr Sonarr client

`github.com/guilycst/mastarr/clients/sonarr` is an independent Go module for
Mastarr's narrow, read-only Sonarr v3 compatibility boundary. It has no import
path to the Mastarr root module, domain, ports, storage, workflow, adapters, or
another upstream client. The root Arr adapter owns translation from these
observations into Mastarr contracts.

The Mastarr-owned [`openapi.yaml`](openapi.yaml) is OpenAPI 3.1.1 and covers:

- `GET /api/v3/system/status` for the Sonarr version and status;
- `GET /api/v3/series` and `GET /api/v3/series/{id}` for the catalog;
- `GET /api/v3/rootfolder` and `GET /api/v3/qualityprofile` for options;
- `GET /api/v3/episode?seriesId=...&includeEpisodeFile=...` and
  `GET /api/v3/episodefile?seriesId=...` for episode-file observations;
- `GET /api/v3/manualimport` for native, read-only import previews. The
  downloaded-folder form uses `folder` and an optional `downloadId`; the
  registered-library form uses `seriesId` and an optional `seasonNumber`.

The candidate compatibility range is Sonarr 3.x and its `/api/v3` API. This
module does not infer support from a version string: callers retain the status
observation and decide which capability evidence is sufficient. Registration,
manual-import execution, command dispatch, history, lookup, and every other
write remain future contract work. The preview endpoint is a GET and never
executes an import.

`New` accepts an absolute HTTP or HTTPS endpoint and appends `/api/v3`; a
reverse-proxy path prefix is preserved. Embedded credentials, queries,
fragments, encoded path bytes, dot segments, backslashes, and repeated path
separators are rejected. A nonempty API key is sent only in the `X-Api-Key`
header. The HTTP client is cloned, cookie state is disabled, redirects are
rejected, and a request context gets a 15-second deadline unless the caller
already supplied one or configured another timeout. API keys and upstream body
text are never placed in errors or `Client.String()` output.

Every response body is bounded before decoding. Sonarr's catalog and option
endpoints return full arrays rather than cursors, so the client reports a
complete `Page[T]` only after retaining the complete bounded array. It leaves
`NextCursor` empty rather than presenting a later mutable request as a
continuation. An over-bound response is rejected. Required fields, nonnegative
IDs and sizes, duplicate identities, episode-to-series scope, nested
`episodeFile` identity, duplicate JSON members, invalid UTF-8, trailing JSON,
control characters, and bounded native text are checked. Unknown upstream
members are accepted for forward compatibility but remain in generated DTO
storage only; normalized public observations copy only the typed fields in this
module.

Native manual-import candidates preserve series and exact episode identity,
season and absolute numbering, episode-file IDs, typed quality and Sonarr 3.x's
singular language field, download ID, release metadata, subtitle flag aliases,
and native rejection `reason` text. The older plural `languages` and
`message` members are retained as compatibility aliases; if both forms are
present they must describe the exact same semantic set (a singular language
therefore requires a one-element plural list). Folder previews reject a
supplied `seriesId`, and library previews require the returned series and every
episode association to match the requested series. Missing nested identity,
association, language, or rejection fields invalidate the complete
observation. No subtitle companion is inferred. Callers still need an
explicit reviewed association before any later root workflow action.

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
service coordinates, live inventories, tracker data, or media files belong in
this repository.
