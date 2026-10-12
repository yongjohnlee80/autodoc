// Package pgtest gives a test a scratch PostgreSQL database with pgvector: one of its own, made on
// the server AUTODOC_TEST_PGURL names (a user that may create databases) and dropped when the test
// ends. Without that URL the test skips, unless AUTODOC_TEST_PG_REQUIRED names a runner whose
// service it must not skip on: then a missing URL fails. pgstore's own cells keep their gate
// (cells_test.go); this is the same gate for the packages built on pgstore.
//
// Concurrency: each call makes its own database; any number of tests may call it in parallel.
package pgtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"testing"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/dao/postgres"
)

// URLEnv and RequiredEnv are the environment Scratch reads.
const (
	URLEnv      = "AUTODOC_TEST_PGURL"
	RequiredEnv = "AUTODOC_TEST_PG_REQUIRED"
)

// Scratch is a fresh database prepared by migrate, closed and dropped when t ends.
func Scratch(t testing.TB, migrate func(context.Context, dao.DataConn) error) dao.DataConn {
	t.Helper()
	dsn := ScratchURL(t)
	ctx := context.Background()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("the scratch database's URL: %v", err)
	}
	conn, err := postgres.OpenNamed(ctx, u.Path[1:], dsn)
	if err != nil {
		t.Fatalf("connect to the scratch database: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := migrate(ctx, conn); err != nil {
		t.Fatal(err)
	}
	return conn
}

// ScratchURL is the URL of a fresh, empty database, dropped when t ends (WITH (FORCE), so a pool
// the test left open does not keep it), for a test that opens its own pools on it: with options
// such as pgstore.WithSchema, or from configuration, as a server does. It skips or fails as
// Scratch does when AUTODOC_TEST_PGURL is not set.
func ScratchURL(t testing.TB) string {
	t.Helper()
	dsn := os.Getenv(URLEnv)
	if dsn == "" {
		if os.Getenv(RequiredEnv) != "" {
			t.Fatalf("%s is required here and is not set", URLEnv)
		}
		t.Skipf("no %s: the PostgreSQL cells skip", URLEnv)
	}
	ctx := context.Background()
	admin, err := postgres.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to %s: %v", URLEnv, err)
	}
	t.Cleanup(func() { admin.Close() })
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	name := "autodoc_pgtest_" + hex.EncodeToString(b)
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("create the scratch database: %v", err)
	}
	t.Cleanup(func() { admin.ExecContext(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)") })
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("%s is not a URL: %v", URLEnv, err)
	}
	u.Path = "/" + name
	return u.String()
}
