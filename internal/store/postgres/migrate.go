package postgres

import (
	"context"
	"embed"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

// migrationFS embeds this package's own schema migrations. River migrates
// its own tables separately (see runMigrations); nothing here targets them.
//
//go:embed migrations/*.sql
var migrationFS embed.FS

// schemaLockKey is an arbitrary fixed key for pg_advisory_xact_lock, used
// only to serialize concurrent Store.Open callers racing to migrate the same
// database (for example two replicas starting up together). It has no
// meaning beyond being a constant every caller agrees on.
const schemaLockKey int64 = 472_819_003

// migration is one embedded schema file, named "<version>_<name>.sql".
type migration struct {
	version int
	name    string
	sql     string
}

// loadMigrations reads and orders every embedded migration by version.
func loadMigrations() ([]migration, error) {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}

	migrations := make([]migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		prefix, _, ok := strings.Cut(entry.Name(), "_")
		if !ok {
			return nil, fmt.Errorf("migration %q missing a version prefix", entry.Name())
		}
		version, err := strconv.Atoi(prefix)
		if err != nil {
			return nil, fmt.Errorf("migration %q has a non-numeric version: %w", entry.Name(), err)
		}

		contents, err := migrationFS.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", entry.Name(), err)
		}

		migrations = append(migrations, migration{version: version, name: entry.Name(), sql: string(contents)})
	}

	sort.Slice(migrations, func(i, j int) bool { return migrations[i].version < migrations[j].version })
	return migrations, nil
}

// runMigrations applies every embedded schema migration not yet recorded in
// schema_migrations, then River's own migrations. pool must not have
// AfterConnect registering pgvector types: the first migration is what
// creates the `vector` extension, so a codec registered ahead of that would
// fail on the very connection this function needs (see Open).
//
// Each migration runs in its own transaction holding a Postgres advisory
// lock for the duration, so two processes migrating the same database at
// once serialize instead of racing: whichever runs second simply finds the
// version already recorded and skips it.
func runMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	migrations, err := loadMigrations()
	if err != nil {
		return err
	}

	err = withSchemaLock(ctx, pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			CREATE TABLE IF NOT EXISTS schema_migrations (
				version    int PRIMARY KEY,
				applied_at timestamptz NOT NULL DEFAULT now()
			)`)
		return err
	})
	if err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	for _, m := range migrations {
		err := withSchemaLock(ctx, pool, func(tx pgx.Tx) error {
			var applied bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`, m.version).Scan(&applied); err != nil {
				return fmt.Errorf("check migration %s: %w", m.name, err)
			}
			if applied {
				return nil
			}
			if _, err := tx.Exec(ctx, m.sql); err != nil {
				return fmt.Errorf("apply migration %s: %w", m.name, err)
			}
			if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, m.version); err != nil {
				return fmt.Errorf("record migration %s: %w", m.name, err)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}

	if err := checkPgvectorVersion(ctx, pool); err != nil {
		return err
	}

	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		return fmt.Errorf("create river migrator: %w", err)
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		return fmt.Errorf("apply river migrations: %w", err)
	}

	return nil
}

// minPgvectorMajor and minPgvectorMinor are the ADR-0007 floor: "pgvector
// (pinned >= 0.8.0, iterative scans enabled)". SearchVector sets
// hnsw.iterative_scan on every call; older pgvector doesn't recognize
// that setting and fails with an opaque internal error, so this is
// checked once at startup instead.
const (
	minPgvectorMajor = 0
	minPgvectorMinor = 8
)

// checkPgvectorVersion fails with a clear error when the vector extension
// is older than ADR-0007 requires. Runs every Open (not gated on whether
// runMigrations actually applied anything this time), on the same
// bootstrap pool the versioned migrations just ran on: CREATE EXTENSION
// IF NOT EXISTS vector, applied by 0001_init.sql above, guarantees
// pg_extension has a row by the time this runs, whether that migration
// ran just now or on a prior startup.
func checkPgvectorVersion(ctx context.Context, pool *pgxpool.Pool) error {
	var version string
	if err := pool.QueryRow(ctx, `SELECT extversion FROM pg_extension WHERE extname = 'vector'`).Scan(&version); err != nil {
		return fmt.Errorf("read pgvector version: %w", err)
	}

	major, minor, err := parseMajorMinor(version)
	if err != nil {
		return fmt.Errorf("parse pgvector version %q: %w", version, err)
	}
	if major < minPgvectorMajor || (major == minPgvectorMajor && minor < minPgvectorMinor) {
		return fmt.Errorf("postgres: pgvector %s found; >= 0.8.0 required (ADR-0007)", version)
	}
	return nil
}

// parseMajorMinor parses the leading "major.minor" of a dotted extension
// version string (for example "0.8.1" -> 0, 8), ignoring any further
// component: pgvector's own version scheme is numeric dotted releases
// only, never a suffix like "-beta".
func parseMajorMinor(version string) (major, minor int, err error) {
	parts := strings.SplitN(version, ".", 3)
	if len(parts) < 2 {
		return 0, 0, fmt.Errorf("expected major.minor, got %q", version)
	}
	major, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("major: %w", err)
	}
	minor, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("minor: %w", err)
	}
	return major, minor, nil
}

// withSchemaLock runs fn inside a transaction holding the schema advisory
// lock, committing on success and rolling back (releasing the lock) on
// error.
func withSchemaLock(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once Commit succeeds

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, schemaLockKey); err != nil {
		return fmt.Errorf("acquire schema lock: %w", err)
	}

	if err := fn(tx); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}
