// Package migrations embeds the ordered SQLite schema migrations used by the
// application at startup and by migration tests.
package migrations

import "embed"

// FS contains every golang-migrate up/down migration in this directory.
//
//go:embed *.sql
var FS embed.FS
