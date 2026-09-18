package db

import "testing"

func TestSplitStatements(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"SELECT 1; SELECT 2", 2},
		{"SELECT 'a;b'; SELECT \"x;y\"", 2},
		{"SELECT 1 -- comment; not a split\n; SELECT 2", 2},
		{"/* a; b */ SELECT 1", 1},
		{"SELECT 1;\n\n", 1},
		{"DELIMITER $$\nCREATE PROCEDURE p() BEGIN SELECT 1; SELECT 2; END$$\nDELIMITER ;\nSELECT 3;", 2},
		{"UPDATE t SET a='it''s'; DELETE FROM t", 2},
		{"", 0},
	}
	for _, c := range cases {
		got := SplitStatements(c.in)
		if len(got) != c.want {
			t.Errorf("SplitStatements(%q) = %d statements %q, want %d", c.in, len(got), got, c.want)
		}
	}
}
