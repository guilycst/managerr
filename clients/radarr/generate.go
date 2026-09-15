// Package radarr is an independent Radarr v3 compatibility client.
package radarr

// The generator is resolved from the repository's pinned tools module. Keep
// generation offline and deterministic after that module has been warmed.
//go:generate sh -c "GOWORK=off GOPROXY=off GOSUMDB=off go tool -modfile=../../tools/go.mod github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen -config oapi-codegen.yaml openapi.yaml"
