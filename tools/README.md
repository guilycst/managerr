# Development tools

The tools module keeps generators out of the application modules. Run commands
with `GOWORK=off` from the module that owns them:

```sh
GOWORK=off go tool github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen -version
GOWORK=off go tool github.com/sqlc-dev/sqlc/cmd/sqlc version
GOWORK=off go tool github.com/golang-migrate/migrate/v4/cmd/migrate -version
GOWORK=off go tool github.com/g4s8/envdoc -h
```

The compatibility test uses `modernc.org/sqlite` with migrate's pure-Go
`database/sqlite` driver. This keeps the database path free of CGO requirements
for the container builds.
