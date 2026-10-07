package driver

import (
	"compress/gzip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func writeGz(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "d.sql.gz")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	if _, err := gz.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	gz.Close()
	f.Close()
	return p
}

func TestCountTablesIgnoresMidLineMatchesAndHugeLines(t *testing.T) {
	huge := "INSERT INTO t VALUES ('" + strings.Repeat("x", 5<<20) + "');\n"
	body := "CREATE TABLE a (x int);\n" + huge + "-- CREATE TABLE b\nCREATE UNLOGGED TABLE c (y int);\n  CREATE TABLE d (z int);\nCREATE TABLE e (z int);\n"
	re := regexp.MustCompile(`^CREATE (UNLOGGED )?TABLE `)
	n, err := countTables(writeGz(t, body), re)
	if err != nil || n != 3 {
		t.Errorf("count = %d, %v; want 3", n, err)
	}
}

func TestCountTablesRejectsCorruptInput(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bad.gz")
	if err := os.WriteFile(p, []byte("not gzip"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := countTables(p, regexp.MustCompile(`^CREATE TABLE `)); err == nil {
		t.Error("expected an error for a non-gzip file")
	}
}
