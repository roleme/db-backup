package driver

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roleme/db-backup/internal/config"
	"github.com/roleme/db-backup/internal/proc"
)

func testConfig(t *testing.T, kv ...string) *config.Config {
	t.Helper()
	m := map[string]string{"DRIVER": "sqlite", "BACKUP_DIR": t.TempDir()}
	for i := 0; i < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	c, err := config.FromVars(m)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestPathsRejectsDuplicateNames(t *testing.T) {
	cfg := testConfig(t, "EXTRA_PATHS", "/data/a/up,/data/b/up")
	err := newPaths(cfg, &proc.Fake{}).Validate()
	if err == nil || !strings.Contains(err.Error(), "EXTRA_PATHS has two directories named up") {
		t.Errorf("error = %v", err)
	}
}

func TestPathsDumpUsesTarWithoutAShell(t *testing.T) {
	dir := t.TempDir()
	keys := filepath.Join(dir, "my keys; rm -rf x")
	if err := os.Mkdir(keys, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(t, "EXTRA_PATHS", keys)
	f := &proc.Fake{}
	p := newPaths(cfg, f)
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	unit := p.Units()[0]
	if err := p.Dump(context.Background(), unit, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	got := f.Calls[0].Spec
	want := []string{"-C", dir, "-cf", "-", "my keys; rm -rf x"}
	if got.Name != "tar" || strings.Join(got.Args, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("call = %s %q", got.Name, got.Args)
	}
}

func TestPathsMissingDirectoryFails(t *testing.T) {
	cfg := testConfig(t, "EXTRA_PATHS", "/data/nope")
	p := newPaths(cfg, &proc.Fake{})
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	err := p.Dump(context.Background(), "nope", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "EXTRA_PATHS entry /data/nope is not a directory") {
		t.Errorf("error = %v", err)
	}
}

func TestPathsHasNoTableCounter(t *testing.T) {
	var a Adapter = newPaths(testConfig(t), &proc.Fake{})
	if _, ok := a.(TableCounter); ok {
		t.Error("an archive adapter must not implement TableCounter")
	}
}
