// Package datastore owns connection-string construction, pooling, and
// migration orchestration for Sonarr's two persistent stores: the main DB
// (series/episodes/config/etc.) and the log DB. It abstracts over the two
// supported engines, SQLite (via modernc.org/sqlite, pure Go) and PostgreSQL
// (via jackc/pgx/v5).
//
// The schema is migrated by github.com/pressly/goose/v3, with one .sql file
// per migration version per dialect (see internal/datastore/migrations/).
// Schema parity with the C# FluentMigrator runner is verified in
// migrate_test.go against fixtures derived from analysis/schema/.
//
// Phase 1.0 milestone: Open() resolves a configured engine, the migration
// runner replays the embedded ledger from version 0 forward, and a smoke
// test asserts the post-migration-1 schema matches the C# reference.
package datastore

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"

	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver "pgx"
	_ "modernc.org/sqlite"             // database/sql driver "sqlite"
)

// Engine identifies which database backend a Config refers to.
type Engine string

const (
	// EngineSQLite is the modernc.org/sqlite driver. Pure Go, no cgo. The
	// C# side uses System.Data.SQLite (CGo); the Go port deliberately
	// drops cgo so cross-compilation stays trivial.
	EngineSQLite Engine = "sqlite"
	// EnginePostgres is the jackc/pgx/v5 driver, used via the stdlib
	// database/sql shim. Sonarr's existing Postgres support is shipped
	// via Npgsql on the C# side; the wire-level schema is the contract.
	EnginePostgres Engine = "postgres"
)

// Role identifies which logical database a connection targets. Sonarr keeps
// the noisy log table off the main DB so backups can skip it; LogDbEnabled
// in the C# config can disable the log DB entirely (see analysis/subsystems
// /system.md). The Go port preserves that separation.
type Role string

const (
	// RoleMain is the primary application database — series, episodes,
	// config, history, providers, etc. Always present.
	RoleMain Role = "main"
	// RoleLog is the application log database. Optional; an empty Log
	// configuration in Config disables it.
	RoleLog Role = "log"
)

// Config describes how to open one logical database. Both engines use the
// same struct; engine-specific fields are tagged in the field comments.
type Config struct {
	Engine Engine
	Role   Role

	// Path is the on-disk file location for SQLite. Ignored for Postgres.
	Path string

	// Host, Port, User, Password, Database are used only by Postgres.
	// Host may be a directory (e.g. "/tmp") to use Unix-domain sockets,
	// matching libpq behaviour.
	Host     string
	Port     int
	User     string
	Password string
	Database string

	// SearchPath is the Postgres search_path applied per-connection.
	// Empty means the server default.
	SearchPath string
}

// DB is an opened, migration-ready connection pool plus enough metadata
// for the migration runner to dispatch on dialect.
type DB struct {
	SQL    *sql.DB
	Engine Engine
	Role   Role
}

// Close releases the underlying connection pool. Safe to call on a nil DB.
func (d *DB) Close() error {
	if d == nil || d.SQL == nil {
		return nil
	}
	return d.SQL.Close()
}

// Open opens a connection pool for the given configuration. The pool is
// pinged once before being returned; an error reaches the caller if the
// driver rejects the DSN or the server is unreachable.
//
// Open does NOT run migrations; call Migrate after Open to bring the schema
// up to head.
func Open(ctx context.Context, c Config) (*DB, error) {
	if c.Role == "" {
		c.Role = RoleMain
	}
	driver, dsn, err := resolveDSN(c)
	if err != nil {
		return nil, fmt.Errorf("datastore: resolve dsn for %s/%s: %w", c.Engine, c.Role, err)
	}
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("datastore: sql.Open %s: %w", driver, err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("datastore: ping %s/%s: %w", c.Engine, c.Role, err)
	}
	return &DB{SQL: db, Engine: c.Engine, Role: c.Role}, nil
}

// resolveDSN turns a Config into a (driverName, dsn) pair that database/sql
// can hand to the registered driver. The pragmas/options applied here mirror
// the C# Datastore connection-string construction so the Go port runs against
// identically-configured SQLite and Postgres instances.
func resolveDSN(c Config) (driver, dsn string, err error) {
	switch c.Engine {
	case EngineSQLite:
		if c.Path == "" {
			return "", "", fmt.Errorf("sqlite path is required")
		}
		// Pragmas mirror the C# system.data.sqlite connection string:
		//   journal_mode=WAL, synchronous=NORMAL, foreign_keys=ON,
		//   busy_timeout=5000ms.
		// See go/analysis/spikes/sqlite-modernc.md for the spike that
		// validated each surface area.
		v := url.Values{}
		v.Add("_pragma", "journal_mode(WAL)")
		v.Add("_pragma", "synchronous(NORMAL)")
		v.Add("_pragma", "foreign_keys(1)")
		v.Add("_pragma", "busy_timeout(5000)")
		return "sqlite", "file:" + filepath.Clean(c.Path) + "?" + v.Encode(), nil
	case EnginePostgres:
		if c.Database == "" {
			return "", "", fmt.Errorf("postgres database is required")
		}
		// pgx accepts URI-style or keyword/value DSNs; URI is more
		// readable and what the spike used. Host may be a directory
		// (Unix socket) — pgx and libpq both accept that.
		u := url.URL{Scheme: "postgres", Path: "/" + c.Database}
		if c.User != "" {
			if c.Password != "" {
				u.User = url.UserPassword(c.User, c.Password)
			} else {
				u.User = url.User(c.User)
			}
		}
		switch {
		case c.Host == "":
			// libpq default: localhost.
		case filepath.IsAbs(c.Host):
			// Unix-socket directory; pgx wants it via the host
			// query parameter, not the URL host.
			q := u.Query()
			q.Set("host", c.Host)
			u.RawQuery = q.Encode()
		default:
			if c.Port != 0 {
				u.Host = fmt.Sprintf("%s:%d", c.Host, c.Port)
			} else {
				u.Host = c.Host
			}
		}
		if c.SearchPath != "" {
			q := u.Query()
			q.Set("search_path", c.SearchPath)
			u.RawQuery = q.Encode()
		}
		return "pgx", u.String(), nil
	default:
		return "", "", fmt.Errorf("unknown engine %q", c.Engine)
	}
}
