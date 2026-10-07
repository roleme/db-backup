package driver

import "strings"

func quotePG(id string) string {
	return `"` + strings.ReplaceAll(id, `"`, `""`) + `"`
}

func quoteSQLite(id string) string {
	return quotePG(id)
}

func quoteMySQL(id string) string {
	return "`" + strings.ReplaceAll(id, "`", "``") + "`"
}

func quoteMySQLString(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}

func quoteSQLiteString(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
