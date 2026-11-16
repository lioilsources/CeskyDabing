package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/lioilsources/CeskyDabing/internal/store/db"
)

func TestMigrationsUpDownUp(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "dub.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	src, err := iofs.New(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	drv, err := sqlite.WithInstance(st.DB, &sqlite.Config{})
	if err != nil {
		t.Fatal(err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "sqlite", drv)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Down(); err != nil {
		t.Fatalf("down: %v", err)
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("up again: %v", err)
	}

	ctx := context.Background()
	s, err := st.UpsertSeries(ctx, db.UpsertSeriesParams{Slug: "bluey", Title: "Bluey"})
	if err != nil {
		t.Fatal(err)
	}
	again, err := st.UpsertSeries(ctx, db.UpsertSeriesParams{Slug: "bluey", Title: "other"})
	if err != nil || again.ID != s.ID || again.Title != "Bluey" {
		t.Fatalf("upsert must keep the row: %+v, %v", again, err)
	}
}
