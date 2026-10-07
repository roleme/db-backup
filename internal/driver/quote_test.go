package driver

import "testing"

func TestQuoting(t *testing.T) {
	cases := []struct{ got, want string }{
		{quotePG(`a"b`), `"a""b"`},
		{quoteMySQL("a`b"), "`a``b`"},
		{quoteMySQLString(`it's \ ok`), `'it\'s \\ ok'`},
		{quoteSQLite(`t"x`), `"t""x"`},
		{quoteSQLiteString("it's"), "'it''s'"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("got %s, want %s", c.got, c.want)
		}
	}
}
