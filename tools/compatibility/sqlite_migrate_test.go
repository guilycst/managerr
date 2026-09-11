package compatibility

import (
	"database/sql"
	"testing"

	"github.com/golang-migrate/migrate/v4/database/sqlite"
	_ "modernc.org/sqlite"
)

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
	if driver == nil {
		t.Fatal("sqlite migration driver is nil")
	}
	if err := driver.Close(); err != nil {
		t.Fatal(err)
	}
}
