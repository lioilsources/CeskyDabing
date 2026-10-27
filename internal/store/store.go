// Package store drží SQLite (stav pipeline, cast, katalog hlasů). DB je cache,
// zdrojem pravdy pro cast jsou soubory v series/<name>/.
package store

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "modernc.org/sqlite"

	"github.com/lioilsources/CeskyDabing/internal/store/db"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct {
	DB *sql.DB
	*db.Queries
}

// Open otevře (a případně založí) databázi a doběhne migrace.
// SQLite patří na lokální disk M2, ne na NFS.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(1)
	if err := migrateUp(conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("migrace: %w", err)
	}
	return &Store{DB: conn, Queries: db.New(conn)}, nil
}

func (s *Store) Close() error { return s.DB.Close() }

func migrateUp(conn *sql.DB) error {
	src, err := iofs.New(migrations, "migrations")
	if err != nil {
		return err
	}
	drv, err := sqlite.WithInstance(conn, &sqlite.Config{})
	if err != nil {
		return err
	}
	m, err := migrate.NewWithInstance("iofs", src, "sqlite", drv)
	if err != nil {
		return err
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	return nil
}
