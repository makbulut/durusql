package db

import "testing"

func TestDetectTable(t *testing.T) {
	cases := map[string]string{
		"SELECT * FROM users": "users",
		"select id, name from `shop`.`orders` where id > 3 limit 5": "shop.orders",
		"SELECT * FROM \"public\".\"images\" ORDER BY id":           "public.images",
		"SELECT * FROM icestar_icedb.beo_orders LIMIT 200":          "icestar_icedb.beo_orders",
		"SELECT u.* FROM users u WHERE u.id=1":                      "users",
		"SELECT * FROM a JOIN b ON a.id=b.a_id":                     "",
		"SELECT COUNT(*) FROM users":                                "",
		"UPDATE users SET x=1":                                      "",
		"SELECT * FROM (SELECT 1) t":                                "",
		"  select *\n from orders\n where 1":                        "orders",
	}
	for q, want := range cases {
		if got := DetectTable(q); got != want {
			t.Errorf("DetectTable(%q) = %q, want %q", q, got, want)
		}
	}
}

func TestBuildChange(t *testing.T) {
	my := &Conn{}
	q, args, err := my.buildChange("db.t", Change{Op: "update", Key: map[string]any{"id": 5, "x": nil}, Values: map[string]any{"b": "2", "a": 1}})
	if err != nil || q != "UPDATE `db`.`t` SET `a` = ?, `b` = ? WHERE `id` = ? AND `x` IS NULL" || len(args) != 3 {
		t.Errorf("mysql update: %q %v %v", q, args, err)
	}
	pg := &Conn{}
	pg.Cfg.Driver = "postgres"
	q, args, err = pg.buildChange("s.t", Change{Op: "delete", Key: map[string]any{"id": 5}})
	if err != nil || q != `DELETE FROM "s"."t" WHERE "id" = $1` || len(args) != 1 {
		t.Errorf("pg delete: %q %v %v", q, args, err)
	}
	q, _, err = pg.buildChange("t", Change{Op: "insert", Values: map[string]any{"a": 1, "b": "x"}})
	if err != nil || q != `INSERT INTO "t" ("a", "b") VALUES ($1, $2)` {
		t.Errorf("pg insert: %q %v", q, err)
	}
}
