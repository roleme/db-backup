package driver

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/roleme/db-backup/internal/config"
	"github.com/roleme/db-backup/internal/logx"
	"github.com/roleme/db-backup/internal/proc"
)

var (
	mysqlCreateTable = regexp.MustCompile(`^CREATE TABLE `)
	definerRe        = regexp.MustCompile("DEFINER=`[^`]*`@`[^`]*`")
)

const orphanQuery = "select concat('select count(*) from `', replace(k.TABLE_NAME, '`', '``'), '` c left join `', replace(k.REFERENCED_TABLE_NAME, '`', '``'), '` p on ',\n" +
	"  group_concat(concat('c.`', replace(k.COLUMN_NAME, '`', '``'), '` = p.`', replace(k.REFERENCED_COLUMN_NAME, '`', '``'), '`') separator ' and '),\n" +
	"  ' where p.`', replace(min(k.REFERENCED_COLUMN_NAME), '`', '``'), '` is null and ',\n" +
	"  group_concat(concat('c.`', replace(k.COLUMN_NAME, '`', '``'), '` is not null') separator ' and '))\n" +
	"from information_schema.KEY_COLUMN_USAGE k\n" +
	"where k.TABLE_SCHEMA = database() and k.REFERENCED_TABLE_SCHEMA = database() and k.REFERENCED_TABLE_NAME is not null\n" +
	"group by k.CONSTRAINT_NAME, k.TABLE_NAME, k.REFERENCED_TABLE_NAME"

type mysql struct {
	cfg  *config.Config
	run  proc.Runner
	args []string
	env  []string
	dbs  []string
}

func newMySQL(cfg *config.Config, run proc.Runner) *mysql {
	return &mysql{cfg: cfg, run: run}
}

func (m *mysql) Validate() error {
	if err := m.cfg.Require("DB_HOST", "DB_USER", "DATABASES"); err != nil {
		return err
	}
	password, err := m.cfg.Secret("DB_PASSWORD")
	if err != nil {
		return err
	}
	if password == "" {
		return errors.New("password_env or password_file is required")
	}
	port := m.cfg.Get("DB_PORT")
	if port == "" {
		port = "3306"
	}
	m.env = append(proc.BaseEnv(), "MYSQL_PWD="+password)
	ca, fingerprint, err := tlsKeys(m.cfg)
	if err != nil {
		return err
	}
	m.args = []string{"-h", m.cfg.Get("DB_HOST"), "-P", port, "-u", m.cfg.Get("DB_USER")}
	switch {
	case ca != "":
		m.args = append(m.args, "--ssl-ca="+ca, "--ssl-verify-server-cert")
	case fingerprint != "":
		m.args = append(m.args, "--ssl-fp="+fingerprint)
	default:
		m.args = append(m.args, "--skip-ssl-verify-server-cert")
	}
	m.dbs = config.SplitList(m.cfg.Get("DATABASES"))
	if len(m.dbs) == 0 {
		return errors.New("databases lists no databases")
	}
	return nil
}

func (m *mysql) Units() []string {
	return m.dbs
}

func (m *mysql) Suffix() string {
	return ".sql.gz"
}

func (m *mysql) client(extra ...string) []string {
	return append(append([]string{}, m.args...), extra...)
}

func (m *mysql) exec(ctx context.Context, sql string) error {
	return m.run.Run(ctx, proc.Spec{Name: "mariadb", Args: m.client("-e", sql), Env: m.env, Stdout: io.Discard})
}

func (m *mysql) query(ctx context.Context, db, sql string) (string, error) {
	args := m.client("-N", "-s")
	if db != "" {
		args = append(args, db)
	}
	args = append(args, "-e", sql)
	return proc.Output(ctx, m.run, proc.Spec{Name: "mariadb", Args: args, Env: m.env})
}

func (m *mysql) Dump(ctx context.Context, unit string, w io.Writer) error {
	args := m.client("--single-transaction", "--no-tablespaces", "--routines", "--triggers")
	args = append(args, m.cfg.ExtraOpts...)
	for _, t := range config.SplitList(m.cfg.ExcludeTableData) {
		if strings.Contains(t, ".") {
			args = append(args, "--ignore-table-data="+t)
		} else {
			args = append(args, "--ignore-table-data="+unit+"."+t)
		}
	}
	args = append(args, unit)
	return m.run.Run(ctx, proc.Spec{Name: "mariadb-dump", Args: args, Env: m.env, Stdout: w})
}

func (m *mysql) Tables(_ context.Context, _, path string) (int, error) {
	return countTables(path, mysqlCreateTable)
}

func stripDefiners(r io.Reader) io.Reader {
	pr, pw := io.Pipe()
	go func() {
		br := bufio.NewReaderSize(r, 64<<10)
		for {
			line, err := br.ReadBytes('\n')
			if len(line) > 0 {
				if _, werr := pw.Write(definerRe.ReplaceAll(line, nil)); werr != nil {
					return
				}
			}
			if err != nil {
				pw.CloseWithError(nilIfEOF(err))
				return
			}
		}
	}()
	return pr
}

func nilIfEOF(err error) error {
	if err == io.EOF {
		return nil
	}
	return err
}

func (m *mysql) orphans(ctx context.Context, scratch string) (int, error) {
	queries, err := m.query(ctx, scratch, orphanQuery)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, q := range strings.Split(queries, "\n") {
		if strings.TrimSpace(q) == "" {
			continue
		}
		out, err := m.query(ctx, scratch, q)
		if err != nil {
			return 0, err
		}
		n, err := strconv.Atoi(strings.TrimSpace(out))
		if err != nil {
			return 0, err
		}
		total += n
	}
	return total, nil
}

func (m *mysql) restore(ctx context.Context, scratch, path string) (got, orphans int, err error) {
	r, err := openGzip(path)
	if err != nil {
		return 0, 0, err
	}
	defer r.Close()
	err = m.run.Run(ctx, proc.Spec{Name: "mariadb", Args: m.client("--one-database", scratch), Env: m.env, Stdin: stripDefiners(r), Stdout: io.Discard})
	if err != nil {
		return 0, 0, err
	}
	out, err := m.query(ctx, "", "select count(*) from information_schema.tables where table_schema = "+quoteMySQLString(scratch)+" and table_type = 'BASE TABLE'")
	if err != nil {
		return 0, 0, err
	}
	if got, err = strconv.Atoi(strings.TrimSpace(out)); err != nil {
		return 0, 0, err
	}
	if m.cfg.ExcludeTableData != "" {
		if orphans, err = m.orphans(ctx, scratch); err != nil {
			return 0, 0, err
		}
	}
	return got, orphans, nil
}

func (m *mysql) Verify(ctx context.Context, unit, path string) error {
	scratch := "dbb_verify_" + unit
	if err := m.exec(ctx, "DROP DATABASE IF EXISTS "+quoteMySQL(scratch)); err != nil {
		return err
	}
	if err := m.exec(ctx, "CREATE DATABASE "+quoteMySQL(scratch)); err != nil {
		return err
	}
	got, orphans, rerr := m.restore(ctx, scratch, path)
	if err := m.exec(ctx, "DROP DATABASE IF EXISTS "+quoteMySQL(scratch)); err != nil {
		logx.Warnf("could not drop %s", scratch)
	}
	if rerr != nil {
		return rerr
	}
	if got == 0 {
		return fmt.Errorf("%s restored no tables", unit)
	}
	if orphans != 0 {
		return fmt.Errorf("%s restored %d orphaned child rows after excluding table rows", unit, orphans)
	}
	return nil
}
