package driver

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/roleme/db-backup/internal/proc"
)

func newTestMySQL(t *testing.T, f *proc.Fake, kv ...string) *mysql {
	t.Helper()
	base := []string{"DRIVER", "mysql", "DB_HOST", "my", "DB_USER", "backup", "DB_PASSWORD", "pw", "DATABASES", "shop"}
	m := newMySQL(testConfig(t, append(base, kv...)...), f)
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestMySQLDumpArgsAndEnv(t *testing.T) {
	f := &proc.Fake{}
	m := newTestMySQL(t, f, "EXCLUDE_TABLE_DATA", "logs,other.audit", "EXTRA_OPTS", "--skip-comments")
	if err := m.Dump(context.Background(), "shop", &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	c := f.Calls[0].Spec
	want := "-h my -P 3306 -u backup --skip-ssl-verify-server-cert --single-transaction --no-tablespaces --routines --triggers --skip-comments --ignore-table-data=shop.logs --ignore-table-data=other.audit shop"
	if c.Name != "mariadb-dump" || strings.Join(c.Args, " ") != want {
		t.Errorf("call = %s %q", c.Name, strings.Join(c.Args, " "))
	}
	if !strings.Contains(strings.Join(c.Env, "\n"), "MYSQL_PWD=pw") {
		t.Error("the password must travel in MYSQL_PWD")
	}
	if strings.Contains(strings.Join(c.Args, " "), "pw") {
		t.Error("the password must never be an argument")
	}
}

func TestMySQLVerifyStripsDefinersAndQuotesNames(t *testing.T) {
	var restored []byte
	var calls []string
	f := &proc.Fake{Handler: func(s proc.Spec) error {
		calls = append(calls, strings.Join(s.Args, " "))
		if s.Stdin != nil {
			restored, _ = io.ReadAll(s.Stdin)
		}
		if s.Stdout != nil && strings.Contains(strings.Join(s.Args, " "), "information_schema.tables") {
			_, _ = s.Stdout.Write([]byte("1\n"))
		}
		return nil
	}}
	m := newTestMySQL(t, f)
	long := "CREATE DEFINER=`root`@`%` TRIGGER t BEFORE INSERT ON a FOR EACH ROW SET NEW.x = 1;\nINSERT INTO a VALUES ('" + strings.Repeat("y", 3<<20) + "');\nCREATE TABLE a (x int);\n"
	err := m.Verify(context.Background(), "sh`op", writeGz(t, long), 1)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(restored), "DEFINER") || !strings.Contains(string(restored), "CREATE  TRIGGER t") {
		t.Errorf("definer not stripped: %.120s", restored)
	}
	if !strings.Contains(string(restored), strings.Repeat("y", 3<<20)) {
		t.Error("a multi-megabyte line must pass through intact")
	}
	all := strings.Join(calls, "\n")
	if !strings.Contains(all, "DROP DATABASE IF EXISTS `dbb_verify_sh``op`") {
		t.Errorf("scratch name not backtick-escaped: %s", all)
	}
	if !strings.Contains(all, `'dbb_verify_sh`+"`"+`op'`) {
		t.Errorf("scratch name not string-quoted in the count query: %s", all)
	}
}

func TestMySQLVerifyFailsOnOrphanedChildRows(t *testing.T) {
	f := &proc.Fake{Handler: func(s proc.Spec) error {
		joined := strings.Join(s.Args, " ")
		switch {
		case s.Stdout == nil:
		case strings.Contains(joined, "KEY_COLUMN_USAGE"):
			_, _ = io.WriteString(s.Stdout, "select count(*) from `a` c left join `b` p on c.`x` = p.`y` where p.`y` is null\n")
		case strings.Contains(joined, "left join"):
			_, _ = io.WriteString(s.Stdout, "2\n")
		case strings.Contains(joined, "information_schema.tables"):
			_, _ = io.WriteString(s.Stdout, "1\n")
		}
		return nil
	}}
	m := newTestMySQL(t, f, "EXCLUDE_TABLE_DATA", "logs")
	err := m.Verify(context.Background(), "shop", writeGz(t, "CREATE TABLE a (x int);\n"), 1)
	if err == nil || !strings.Contains(err.Error(), "shop restored 2 orphaned child rows after excluding table rows") {
		t.Errorf("error = %v", err)
	}
}

func TestMySQLOrphanCheckRunsOnlyAfterExcludingRows(t *testing.T) {
	f := &proc.Fake{Handler: func(s proc.Spec) error {
		if s.Stdout != nil && strings.Contains(strings.Join(s.Args, " "), "information_schema.tables") {
			_, _ = io.WriteString(s.Stdout, "1\n")
		}
		return nil
	}}
	m := newTestMySQL(t, f)
	if err := m.Verify(context.Background(), "shop", writeGz(t, "CREATE TABLE a (x int);\n"), 1); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.Calls {
		if strings.Contains(strings.Join(c.Spec.Args, " "), "KEY_COLUMN_USAGE") {
			t.Error("without excluded rows the orphan check must not run")
		}
	}
}

func TestMySQLTablesDoesNotCountUnloggedSyntax(t *testing.T) {
	m := newTestMySQL(t, &proc.Fake{})
	n, err := m.Tables(context.Background(), "shop", writeGz(t, "CREATE TABLE a (x int);\nCREATE TABLE b (x int);\nINSERT INTO a VALUES (1);\n"))
	if err != nil || n != 2 {
		t.Errorf("Tables = %d, %v", n, err)
	}
}
