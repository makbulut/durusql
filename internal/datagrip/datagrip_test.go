package datagrip

import "testing"

func TestDecodeSecret(t *testing.T) {
	cases := []struct{ in, user, pw string }{
		{"@hunter2", "", "hunter2"},
		{"bob@p@ss", "bob", "p@ss"},
		{`a\@b@pw`, "a@b", "pw"},
		{`c\\d@pw`, `c\d`, "pw"},
		{"nouser", "nouser", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		u, p := decodeSecret(c.in)
		if u != c.user || p != c.pw {
			t.Errorf("decodeSecret(%q) = (%q,%q), want (%q,%q)", c.in, u, p, c.user, c.pw)
		}
	}
}

func TestParseJDBC(t *testing.T) {
	d, h, p, db, err := parseJDBC("jdbc:postgresql://127.0.0.1:5432/images?serverVersion=11&charset=utf8")
	if err != nil || d != "postgres" || h != "127.0.0.1" || p != 5432 || db != "images" {
		t.Errorf("got %s %s %d %s %v", d, h, p, db, err)
	}
	d, h, p, db, err = parseJDBC("jdbc:mariadb://10.130.0.211:3306/")
	if err != nil || d != "mysql" || h != "10.130.0.211" || p != 3306 || db != "" {
		t.Errorf("got %s %s %d %s %v", d, h, p, db, err)
	}
	if _, _, _, _, err := parseJDBC("jdbc:oracle:thin:@x"); err == nil {
		t.Error("expected error for oracle")
	}
}
