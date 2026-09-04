// Package testpg gives contract-seam tests (internal/api/api_test.go) and
// postgres package tests a real, disposable Postgres database without a
// hand-run fixture: New returns a DSN for a fresh, empty database, and Main
// tears down whatever New started.
package testpg

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// adminDatabaseURLEnv, if set, is an admin DSN for a Postgres server New
// creates and drops per-test databases on (for example a CI-provisioned
// instance). It takes priority over the temporary cluster.
const adminDatabaseURLEnv = "CE_TEST_DATABASE_URL"

var (
	clusterOnce  sync.Once
	clusterAdmin string
	clusterDir   string
	clusterErr   error
)

// New returns a DSN for a fresh, empty database, dropped in a t.Cleanup.
// It uses $CE_TEST_DATABASE_URL as an admin DSN if set (per-test databases
// are created on that server); otherwise it lazily starts a temporary
// Postgres cluster from initdb/pg_ctl on PATH, shared by every test in the
// binary and stopped by Main. If neither is available, it skips t.
func New(t testing.TB) string {
	t.Helper()

	admin := adminDSN(t)

	dbName := "loom_test_" + randomHex(8)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatalf("testpg: connect to admin database: %v", err)
	}
	defer func() {
		if cerr := conn.Close(ctx); cerr != nil {
			t.Logf("testpg: close admin connection: %v", cerr)
		}
	}()

	if _, err := conn.Exec(ctx, `CREATE DATABASE `+dbName); err != nil {
		t.Fatalf("testpg: create database %s: %v", dbName, err)
	}

	t.Cleanup(func() {
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer dropCancel()

		dropConn, err := pgx.Connect(dropCtx, admin)
		if err != nil {
			t.Errorf("testpg: connect to admin database to drop %s: %v", dbName, err)
			return
		}
		defer func() {
			if cerr := dropConn.Close(dropCtx); cerr != nil {
				t.Logf("testpg: close admin connection after dropping %s: %v", dbName, cerr)
			}
		}()

		// FORCE disconnects any sessions the test's own Store pool left open,
		// so a forgotten Store.Close never leaks a database.
		if _, err := dropConn.Exec(dropCtx, `DROP DATABASE IF EXISTS `+dbName+` WITH (FORCE)`); err != nil {
			t.Errorf("testpg: drop database %s: %v", dbName, err)
		}
	})

	dsn, err := withDatabase(admin, dbName)
	if err != nil {
		t.Fatalf("testpg: build dsn for %s: %v", dbName, err)
	}
	return dsn
}

// Main is the TestMain helper for packages that call New. It runs the
// package's tests and, if New started a temporary cluster, stops it and
// removes its data directory.
func Main(m *testing.M) {
	code := m.Run()
	stopCluster()
	os.Exit(code)
}

// adminDSN resolves the admin DSN New uses. With neither
// CE_TEST_DATABASE_URL set nor a usable initdb/pg_ctl on PATH, it skips t
// under `go test -short` (a deliberately reduced run) and otherwise fails
// t outright: the contract seam is supposed to run against a real
// Postgres (Testing Decisions), so a silent skip in a normal run would
// let `go test ./...` pass having asserted nothing.
func adminDSN(t testing.TB) string {
	t.Helper()

	if dsn := os.Getenv(adminDatabaseURLEnv); dsn != "" {
		return dsn
	}

	dsn, err := ensureCluster()
	if err != nil {
		msg := fmt.Sprintf("testpg: no local Postgres available: set %s to an admin DSN, or put initdb and pg_ctl on PATH for a temporary cluster (e.g. `nix develop`) (%v)", adminDatabaseURLEnv, err)
		if testing.Short() {
			t.Skip(msg)
		}
		t.Fatal(msg)
	}
	return dsn
}

// ensureCluster starts the shared temporary cluster on first call and
// returns its admin DSN on every call.
func ensureCluster() (string, error) {
	clusterOnce.Do(func() {
		clusterAdmin, clusterErr = startCluster()
	})
	return clusterAdmin, clusterErr
}

// startCluster initializes and starts a throwaway Postgres cluster: trust
// auth and fsync off since it holds nothing that outlives the test binary.
func startCluster() (string, error) {
	initdbPath, err := exec.LookPath("initdb")
	if err != nil {
		return "", fmt.Errorf("initdb not on PATH: %w", err)
	}
	pgCtlPath, err := exec.LookPath("pg_ctl")
	if err != nil {
		return "", fmt.Errorf("pg_ctl not on PATH: %w", err)
	}

	dir, err := os.MkdirTemp("", "loom-testpg-")
	if err != nil {
		return "", fmt.Errorf("create cluster directory: %w", err)
	}

	port, err := freeTCPPort()
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("find free port: %w", err)
	}

	initCmd := exec.Command(initdbPath, "-D", dir, "-U", "postgres", "--auth=trust", "-E", "UTF8")
	if out, err := initCmd.CombinedOutput(); err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("initdb: %w\n%s", err, out)
	}

	// pg_ctl start leaves the postgres server (and its long-lived helper
	// processes: checkpointer, background writer, ...) running with
	// stdout/stderr inherited from pg_ctl itself. A pipe-backed
	// CombinedOutput would then wait for EOF that never arrives, since
	// postgres keeps the write end open long after pg_ctl exits — so its
	// launch log goes to a real file, which Run doesn't block on.
	logFile, err := os.Create(filepath.Join(dir, "pg_ctl_start.log"))
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("create pg_ctl start log: %w", err)
	}
	defer func() { _ = logFile.Close() }()

	options := fmt.Sprintf("-p %d -c listen_addresses=127.0.0.1 -c fsync=off -c synchronous_commit=off -c full_page_writes=off", port)
	startCmd := exec.Command(pgCtlPath, "-D", dir, "-o", options, "-w", "start")
	startCmd.Stdout = logFile
	startCmd.Stderr = logFile
	if err := startCmd.Run(); err != nil {
		log, _ := os.ReadFile(logFile.Name())
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("pg_ctl start: %w\n%s", err, log)
	}

	// Only recorded once startup actually succeeds, so a failed start never
	// leaves stopCluster trying to stop a cluster that isn't running.
	clusterDir = dir

	return fmt.Sprintf("postgres://postgres@127.0.0.1:%d/postgres?sslmode=disable", port), nil
}

// stopCluster stops the cluster startCluster started, if any, and removes
// its data directory. Called only from Main, after m.Run() has returned so
// every test (and thus every possible New call) has completed.
func stopCluster() {
	if clusterDir == "" {
		return
	}
	defer func() { _ = os.RemoveAll(clusterDir) }()

	pgCtlPath, err := exec.LookPath("pg_ctl")
	if err != nil {
		return
	}
	cmd := exec.Command(pgCtlPath, "-D", clusterDir, "stop", "-m", "immediate")
	_ = cmd.Run()
}

// freeTCPPort finds a currently-unused TCP port on 127.0.0.1. Postgres has
// no "pick any port" mode of its own, so the caller reserves one this way,
// releases it, and immediately hands the same number to pg_ctl.
func freeTCPPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// withDatabase returns dsn with its database path replaced by dbName.
func withDatabase(dsn, dbName string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("parse dsn: %w", err)
	}
	u.Path = "/" + dbName
	return u.String(), nil
}

// randomHex returns n random bytes hex-encoded, for unique-enough per-test
// database names.
func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand.Read on any supported platform only fails if the OS
		// entropy source itself is broken, which nothing downstream could
		// recover from either.
		panic("testpg: read random bytes: " + err.Error())
	}
	return hex.EncodeToString(buf)
}
