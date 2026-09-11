package compatibility

import (
	"database/sql"
	"embed"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "modernc.org/sqlite"
)

//go:embed migrations
var migrations embed.FS

func TestSQLiteMigrateDriver(t *testing.T) {
	db, err := sql.Open("sqlite", "file:compatibility?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := db.Ping(); err != nil {
		t.Fatal(err)
	}
	driver, err := sqlite.WithInstance(db, &sqlite.Config{})
	if err != nil {
		t.Fatal(err)
	}
	source, err := iofs.New(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	runner, err := migrate.NewWithInstance("iofs", source, "sqlite", driver)
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Up(); err != nil {
		t.Fatal(err)
	}
	version, dirty, err := driver.Version()
	if err != nil {
		t.Fatal(err)
	}
	if version != 1 || dirty {
		t.Fatalf("unexpected migration state: version=%d dirty=%t", version, dirty)
	}
	if sourceErr, databaseErr := runner.Close(); sourceErr != nil || databaseErr != nil {
		t.Fatalf("close migration runner: source=%v database=%v", sourceErr, databaseErr)
	}
}
