package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"
)

// SchemaObjects is what the explorer shows directly under a database/schema.
type SchemaObjects struct {
	Tables    []string   `json:"tables"`
	Views     []string   `json:"views"`
	Routines  []Routine  `json:"routines"`
	Events    []string   `json:"events"`
	Sequences []Sequence `json:"sequences"` // PostgreSQL
	Types     []TypeInfo `json:"types"`     // PostgreSQL enums / composite types
}

type Sequence struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type TypeInfo struct {
	Name   string   `json:"name"`
	Kind   string   `json:"kind"` // enum | composite | domain
	Values []string `json:"values"`
}

// DatabaseObjects are server-level things DataGrip lists under "Database Objects" (PostgreSQL).
type DatabaseObjects struct {
	Database   string      `json:"database"`
	Extensions []Extension `json:"extensions"`
	Languages  []string    `json:"languages"`
}

type Extension struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// DatabaseObjectsList returns extensions and languages of the connected PostgreSQL database.
func (c *Conn) DatabaseObjectsList(ctx context.Context) (*DatabaseObjects, error) {
	out := &DatabaseObjects{Database: c.Cfg.Database, Extensions: []Extension{}, Languages: []string{}}
	if !c.isPG() {
		return out, nil
	}
	rows, err := c.DB.QueryContext(ctx, `SELECT extname, extversion FROM pg_extension ORDER BY 1`)
	if err == nil {
		for rows.Next() {
			var e Extension
			if rows.Scan(&e.Name, &e.Version) == nil {
				out.Extensions = append(out.Extensions, e)
			}
		}
		rows.Close()
	}
	if ls, err := c.strings(ctx, `SELECT lanname FROM pg_language ORDER BY 1`); err == nil {
		out.Languages = ls
	}
	if c.Cfg.Database == "" {
		if d, err := c.strings(ctx, `SELECT current_database()`); err == nil && len(d) == 1 {
			out.Database = d[0]
		}
	}
	return out, nil
}

type Routine struct {
	Name string `json:"name"`
	Type string `json:"type"` // FUNCTION | PROCEDURE
}

type ColumnInfo struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
	Key      string `json:"key"` // PRI | UNI | MUL | ""
	Default  string `json:"default"`
	Extra    string `json:"extra"`
	Comment  string `json:"comment"`
	// extended properties for the Modify dialog
	Collation  string `json:"collation"`
	Generation string `json:"generation"` // expression of a generated column
	GenKind    string `json:"genKind"`    // STORED | VIRTUAL | ""
	OnUpdate   string `json:"onUpdate"`   // MySQL: ON UPDATE expression
	Hidden     bool   `json:"hidden"`     // MySQL INVISIBLE
	Identity   string `json:"identity"`   // PostgreSQL: ALWAYS | BY DEFAULT | ""
}

// TableInfo holds table-level properties shown in the Modify dialog.
type TableInfo struct {
	Engine        string `json:"engine"`
	Collation     string `json:"collation"`
	Comment       string `json:"comment"`
	RowFormat     string `json:"rowFormat"`
	AutoIncrement int64  `json:"autoIncrement"` // MySQL: next auto-increment value
}

type KeyInfo struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"` // PRIMARY KEY | UNIQUE
	Columns []string `json:"columns"`
}

type ForeignKeyInfo struct {
	Name       string   `json:"name"`
	Columns    []string `json:"columns"`
	RefTable   string   `json:"refTable"`
	RefColumns []string `json:"refColumns"`
	Definition string   `json:"definition"`
}

type IndexInfo struct {
	Name       string   `json:"name"`
	Unique     bool     `json:"unique"`
	Columns    []string `json:"columns"`
	Definition string   `json:"definition"`
}

type TriggerInfo struct {
	Name   string `json:"name"`
	Timing string `json:"timing"` // BEFORE | AFTER
	Event  string `json:"event"`  // INSERT | UPDATE | DELETE
}

type PartitionInfo struct {
	Name        string `json:"name"`
	Method      string `json:"method"`
	Expression  string `json:"expression"`
	Description string `json:"description"`
}

type TableDetails struct {
	Columns     []ColumnInfo     `json:"columns"`
	Keys        []KeyInfo        `json:"keys"`
	ForeignKeys []ForeignKeyInfo `json:"foreignKeys"`
	Indexes     []IndexInfo      `json:"indexes"`
	Triggers    []TriggerInfo    `json:"triggers"`
	Partitions  []PartitionInfo  `json:"partitions"`
	Errors      []string         `json:"errors,omitempty"` // sections that could not be read
	Ms          int64            `json:"ms"`
	Info        TableInfo        `json:"info"`
}

// ph returns the n-th (1-based) positional placeholder for the driver.
func (c *Conn) ph(n int) string {
	if c.isPG() {
		return fmt.Sprintf("$%d", n)
	}
	return "?"
}

func (c *Conn) splitTable(table string) (schema, name string) {
	if i := strings.LastIndexByte(table, '.'); i > 0 {
		return table[:i], table[i+1:]
	}
	if c.isPG() {
		return "public", table
	}
	return c.Cfg.Database, table
}

// SchemaObjects lists tables, views, routines and (MySQL) events of one schema.
func (c *Conn) SchemaObjects(ctx context.Context, schema string) (*SchemaObjects, error) {
	out := &SchemaObjects{Tables: []string{}, Views: []string{}, Routines: []Routine{}, Events: []string{}, Sequences: []Sequence{}, Types: []TypeInfo{}}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var tablesErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		rows, err := c.DB.QueryContext(ctx, `SELECT table_name, table_type FROM information_schema.tables WHERE table_schema = `+c.ph(1)+` ORDER BY 1`, schema)
		if err != nil {
			tablesErr = err
			return
		}
		defer rows.Close()
		var tables, views []string
		for rows.Next() {
			var n, t string
			if err := rows.Scan(&n, &t); err != nil {
				tablesErr = err
				return
			}
			if strings.Contains(strings.ToUpper(t), "VIEW") {
				views = append(views, n)
			} else {
				tables = append(tables, n)
			}
		}
		mu.Lock()
		out.Tables, out.Views = append(out.Tables, tables...), append(out.Views, views...)
		mu.Unlock()
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		rows, err := c.DB.QueryContext(ctx, `SELECT routine_name, routine_type FROM information_schema.routines WHERE routine_schema = `+c.ph(1)+` ORDER BY 1`, schema)
		if err != nil {
			return
		}
		defer rows.Close()
		var rs []Routine
		for rows.Next() {
			var r Routine
			if rows.Scan(&r.Name, &r.Type) == nil {
				rs = append(rs, r)
			}
		}
		mu.Lock()
		out.Routines = append(out.Routines, rs...)
		mu.Unlock()
	}()
	if !c.isPG() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ev, err := c.strings(ctx, `SELECT event_name FROM information_schema.events WHERE event_schema = ? ORDER BY 1`, schema); err == nil {
				mu.Lock()
				out.Events = append(out.Events, ev...)
				mu.Unlock()
			}
		}()
	} else {
		wg.Add(2)
		go func() {
			defer wg.Done()
			rows, err := c.DB.QueryContext(ctx, `SELECT sequence_name, data_type FROM information_schema.sequences WHERE sequence_schema = $1 ORDER BY 1`, schema)
			if err != nil {
				return
			}
			defer rows.Close()
			var sq []Sequence
			for rows.Next() {
				var q Sequence
				if rows.Scan(&q.Name, &q.Type) == nil {
					sq = append(sq, q)
				}
			}
			mu.Lock()
			out.Sequences = append(out.Sequences, sq...)
			mu.Unlock()
		}()
		go func() {
			defer wg.Done()
			rows, err := c.DB.QueryContext(ctx, `
				SELECT t.typname,
				       CASE t.typtype WHEN 'e' THEN 'enum' WHEN 'd' THEN 'domain' ELSE 'composite' END,
				       COALESCE((SELECT string_agg(e.enumlabel, ',' ORDER BY e.enumsortorder) FROM pg_enum e WHERE e.enumtypid = t.oid), '')
				FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace
				LEFT JOIN pg_class cl ON cl.oid = t.typrelid
				WHERE n.nspname = $1 AND (t.typtype IN ('e','d') OR (t.typtype = 'c' AND cl.relkind = 'c'))
				ORDER BY 1`, schema)
			if err != nil {
				return
			}
			defer rows.Close()
			var ts []TypeInfo
			for rows.Next() {
				var ti TypeInfo
				var vals string
				if rows.Scan(&ti.Name, &ti.Kind, &vals) == nil {
					if vals != "" {
						ti.Values = strings.Split(vals, ",")
					} else {
						ti.Values = []string{}
					}
					ts = append(ts, ti)
				}
			}
			mu.Lock()
			out.Types = append(out.Types, ts...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	if tablesErr != nil {
		return nil, tablesErr
	}
	return out, nil
}

// TableDetails collects the structure of one table for the explorer. Sections that fail are
// reported in Errors rather than hiding the whole table.
func (c *Conn) TableDetails(ctx context.Context, table string) (*TableDetails, error) {
	start := time.Now()
	schema, name := c.splitTable(table)
	d := &TableDetails{Columns: []ColumnInfo{}, Keys: []KeyInfo{}, ForeignKeys: []ForeignKeyInfo{}, Indexes: []IndexInfo{}, Triggers: []TriggerInfo{}, Partitions: []PartitionInfo{}}
	var err error
	if d.Columns, err = c.columnInfo(ctx, schema, name); err != nil {
		return nil, err
	}
	c.enrichColumns(ctx, schema, name, d.Columns)
	// the remaining sections are independent: run them concurrently (the pool has 4 connections)
	var mu sync.Mutex
	var wg sync.WaitGroup
	fail := func(what string, err error) {
		if err != nil {
			mu.Lock()
			d.Errors = append(d.Errors, what+": "+err.Error())
			mu.Unlock()
		}
	}
	section := func(f func()) { wg.Add(1); go func() { defer wg.Done(); f() }() }
	if c.isPG() {
		section(func() { k, e := c.keyInfo(ctx, schema, name); mu.Lock(); d.Keys = k; mu.Unlock(); fail("keys", e) })
		section(func() {
			ix, e := c.indexes(ctx, schema, name)
			mu.Lock()
			d.Indexes = ix
			mu.Unlock()
			fail("indexes", e)
		})
	} else {
		// one SHOW INDEX gives both the constraints (PRIMARY/unique) and the index list
		section(func() {
			k, ix, e := c.showIndex(ctx, schema, name)
			mu.Lock()
			d.Keys, d.Indexes = k, ix
			mu.Unlock()
			fail("keys/indexes", e)
		})
	}
	section(func() {
		fk, e := c.foreignKeys(ctx, schema, name)
		mu.Lock()
		d.ForeignKeys = fk
		mu.Unlock()
		fail("foreign keys", e)
	})
	section(func() {
		tr, e := c.triggers(ctx, schema, name)
		mu.Lock()
		d.Triggers = tr
		mu.Unlock()
		fail("triggers", e)
	})
	section(func() {
		pt, e := c.partitions(ctx, schema, name)
		mu.Lock()
		d.Partitions = pt
		mu.Unlock()
		fail("partitions", e)
	})
	section(func() {
		ti, e := c.tableInfo(ctx, schema, name)
		mu.Lock()
		d.Info = ti
		mu.Unlock()
		fail("table info", e)
	})
	wg.Wait()
	for _, p := range []*[]KeyInfo{&d.Keys} {
		if *p == nil {
			*p = []KeyInfo{}
		}
	}
	if d.ForeignKeys == nil {
		d.ForeignKeys = []ForeignKeyInfo{}
	}
	if d.Indexes == nil {
		d.Indexes = []IndexInfo{}
	}
	if d.Triggers == nil {
		d.Triggers = []TriggerInfo{}
	}
	if d.Partitions == nil {
		d.Partitions = []PartitionInfo{}
	}
	d.Ms = time.Since(start).Milliseconds()
	return d, nil
}

// showIndex (MySQL/MariaDB) derives keys and indexes from SHOW INDEX, which is instant, unlike
// joins over information_schema that scan every schema on the server.
func (c *Conn) showIndex(ctx context.Context, schema, name string) ([]KeyInfo, []IndexInfo, error) {
	rows, err := c.DB.QueryContext(ctx, "SHOW INDEX FROM "+c.Quote(schema+"."+name))
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	pos := map[string]int{}
	for i, cn := range cols {
		pos[strings.ToLower(cn)] = i
	}
	var keys []KeyInfo
	var idx []IndexInfo
	kpos, ipos := map[string]int{}, map[string]int{}
	for rows.Next() {
		raw := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, nil, err
		}
		str := func(k string) string {
			if i, ok := pos[k]; ok {
				if v := normalize(raw[i]); v != nil {
					return fmt.Sprint(v)
				}
			}
			return ""
		}
		kn, col := str("key_name"), str("column_name")
		if col == "" {
			col = str("expression")
		}
		nonUnique := str("non_unique") != "0"
		if i, ok := ipos[kn]; ok {
			idx[i].Columns = append(idx[i].Columns, col)
		} else {
			ipos[kn] = len(idx)
			idx = append(idx, IndexInfo{Name: kn, Unique: !nonUnique, Columns: []string{col}})
		}
		if !nonUnique {
			if i, ok := kpos[kn]; ok {
				keys[i].Columns = append(keys[i].Columns, col)
			} else {
				t := "UNIQUE"
				if kn == "PRIMARY" {
					t = "PRIMARY KEY"
				}
				kpos[kn] = len(keys)
				keys = append(keys, KeyInfo{Name: kn, Type: t, Columns: []string{col}})
			}
		}
	}
	for i := range idx {
		idx[i].Definition = "(" + strings.Join(idx[i].Columns, ", ") + ")"
	}
	return keys, idx, rows.Err()
}

func (c *Conn) columnInfo(ctx context.Context, schema, name string) ([]ColumnInfo, error) {
	var q string
	if c.isPG() {
		q = `SELECT c.column_name,
		            CASE WHEN c.data_type IN ('character varying','character') THEN c.data_type || '(' || c.character_maximum_length || ')'
		                 WHEN c.data_type = 'numeric' AND c.numeric_precision IS NOT NULL THEN 'numeric(' || c.numeric_precision || ',' || COALESCE(c.numeric_scale,0) || ')'
		                 WHEN c.data_type = 'USER-DEFINED' THEN c.udt_name ELSE c.data_type END,
		            c.is_nullable = 'YES',
		            COALESCE((SELECT 'PRI' FROM information_schema.key_column_usage k
		                      JOIN information_schema.table_constraints t ON t.constraint_name = k.constraint_name AND t.constraint_schema = k.constraint_schema
		                      WHERE t.constraint_type = 'PRIMARY KEY' AND k.table_schema = c.table_schema AND k.table_name = c.table_name AND k.column_name = c.column_name LIMIT 1), ''),
		            COALESCE(c.column_default, ''), '',
		            COALESCE(col_description((quote_ident(c.table_schema) || '.' || quote_ident(c.table_name))::regclass, c.ordinal_position), '')
		     FROM information_schema.columns c WHERE c.table_schema = $1 AND c.table_name = $2 ORDER BY c.ordinal_position`
	} else {
		return c.showColumns(ctx, schema, name)
	}
	rows, err := c.DB.QueryContext(ctx, q, schema, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ColumnInfo{}
	for rows.Next() {
		var col ColumnInfo
		if err := rows.Scan(&col.Name, &col.Type, &col.Nullable, &col.Key, &col.Default, &col.Extra, &col.Comment); err != nil {
			return nil, err
		}
		out = append(out, col)
	}
	return out, rows.Err()
}

// showColumns (MySQL/MariaDB) reads SHOW FULL COLUMNS, which only touches this table.
func (c *Conn) showColumns(ctx context.Context, schema, name string) ([]ColumnInfo, error) {
	rows, err := c.DB.QueryContext(ctx, "SHOW FULL COLUMNS FROM "+c.Quote(schema+"."+name))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	pos := map[string]int{}
	for i, cn := range cols {
		pos[strings.ToLower(cn)] = i
	}
	out := []ColumnInfo{}
	for rows.Next() {
		raw := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		str := func(k string) (string, bool) {
			if i, ok := pos[k]; ok {
				if v := normalize(raw[i]); v != nil {
					return fmt.Sprint(v), true
				}
			}
			return "", false
		}
		var col ColumnInfo
		col.Name, _ = str("field")
		col.Type, _ = str("type")
		n, _ := str("null")
		col.Nullable = strings.EqualFold(n, "YES")
		col.Key, _ = str("key")
		if def, ok := str("default"); ok {
			col.Default = def
		} else {
			col.Default = "NULL"
		}
		col.Extra, _ = str("extra")
		col.Comment, _ = str("comment")
		out = append(out, col)
	}
	return out, rows.Err()
}

// keyInfo (PostgreSQL) reads primary/unique constraints straight from pg_catalog.
func (c *Conn) keyInfo(ctx context.Context, schema, name string) ([]KeyInfo, error) {
	rows, err := c.DB.QueryContext(ctx, `
		SELECT con.conname, con.contype,
		       (SELECT string_agg(a.attname, ',' ORDER BY k.ord)
		          FROM unnest(con.conkey) WITH ORDINALITY k(attnum, ord)
		          JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = k.attnum)
		FROM pg_constraint con JOIN pg_class cl ON cl.oid = con.conrelid JOIN pg_namespace n ON n.oid = cl.relnamespace
		WHERE con.contype IN ('p','u') AND n.nspname = $1 AND cl.relname = $2 ORDER BY con.contype, con.conname`, schema, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KeyInfo
	for rows.Next() {
		var n, t, cols string
		if err := rows.Scan(&n, &t, &cols); err != nil {
			return nil, err
		}
		k := KeyInfo{Name: n, Type: "UNIQUE", Columns: strings.Split(cols, ",")}
		if t == "p" {
			k.Type = "PRIMARY KEY"
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (c *Conn) foreignKeys(ctx context.Context, schema, name string) ([]ForeignKeyInfo, error) {
	var out []ForeignKeyInfo
	if c.isPG() {
		rows, err := c.DB.QueryContext(ctx, `
			SELECT con.conname, pg_get_constraintdef(con.oid)
			FROM pg_constraint con JOIN pg_class cl ON cl.oid = con.conrelid JOIN pg_namespace n ON n.oid = cl.relnamespace
			WHERE con.contype = 'f' AND n.nspname = $1 AND cl.relname = $2 ORDER BY 1`, schema, name)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var fk ForeignKeyInfo
			if err := rows.Scan(&fk.Name, &fk.Definition); err != nil {
				return nil, err
			}
			out = append(out, fk)
		}
		return out, rows.Err()
	}
	rows, err := c.DB.QueryContext(ctx, `
		SELECT constraint_name, column_name, referenced_table_schema, referenced_table_name, referenced_column_name
		FROM information_schema.key_column_usage
		WHERE table_schema = ? AND table_name = ? AND referenced_table_name IS NOT NULL
		ORDER BY constraint_name, ordinal_position`, schema, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	idx := map[string]int{}
	for rows.Next() {
		var n, col, rs, rt, rc string
		if err := rows.Scan(&n, &col, &rs, &rt, &rc); err != nil {
			return nil, err
		}
		i, ok := idx[n]
		if !ok {
			idx[n] = len(out)
			ref := rt
			if rs != schema {
				ref = rs + "." + rt
			}
			out = append(out, ForeignKeyInfo{Name: n, RefTable: ref})
			i = len(out) - 1
		}
		out[i].Columns = append(out[i].Columns, col)
		out[i].RefColumns = append(out[i].RefColumns, rc)
	}
	for i := range out {
		out[i].Definition = fmt.Sprintf("(%s) → %s (%s)", strings.Join(out[i].Columns, ", "), out[i].RefTable, strings.Join(out[i].RefColumns, ", "))
	}
	return out, rows.Err()
}

func (c *Conn) indexes(ctx context.Context, schema, name string) ([]IndexInfo, error) {
	var out []IndexInfo
	if c.isPG() {
		rows, err := c.DB.QueryContext(ctx, `SELECT indexname, indexdef FROM pg_indexes WHERE schemaname = $1 AND tablename = $2 ORDER BY 1`, schema, name)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var ix IndexInfo
			if err := rows.Scan(&ix.Name, &ix.Definition); err != nil {
				return nil, err
			}
			ix.Unique = strings.HasPrefix(ix.Definition, "CREATE UNIQUE")
			if i := strings.Index(ix.Definition, "("); i > 0 && strings.HasSuffix(ix.Definition, ")") {
				ix.Columns = strings.Split(ix.Definition[i+1:len(ix.Definition)-1], ", ")
			}
			out = append(out, ix)
		}
		return out, rows.Err()
	}
	rows, err := c.DB.QueryContext(ctx, `
		SELECT index_name, non_unique = 0, column_name FROM information_schema.statistics
		WHERE table_schema = ? AND table_name = ? ORDER BY index_name, seq_in_index`, schema, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	idx := map[string]int{}
	for rows.Next() {
		var n, col string
		var uniq bool
		if err := rows.Scan(&n, &uniq, &col); err != nil {
			return nil, err
		}
		i, ok := idx[n]
		if !ok {
			idx[n] = len(out)
			out = append(out, IndexInfo{Name: n, Unique: uniq})
			i = len(out) - 1
		}
		out[i].Columns = append(out[i].Columns, col)
	}
	for i := range out {
		out[i].Definition = "(" + strings.Join(out[i].Columns, ", ") + ")"
	}
	return out, rows.Err()
}

func (c *Conn) triggers(ctx context.Context, schema, name string) ([]TriggerInfo, error) {
	if !c.isPG() {
		rows, err := c.DB.QueryContext(ctx, "SHOW TRIGGERS FROM "+c.Quote(schema)+" WHERE `Table` = ?", name)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		cols, _ := rows.Columns()
		pos := map[string]int{}
		for i, cn := range cols {
			pos[strings.ToLower(cn)] = i
		}
		var out []TriggerInfo
		for rows.Next() {
			raw := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range raw {
				ptrs[i] = &raw[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				return nil, err
			}
			str := func(k string) string {
				if i, ok := pos[k]; ok {
					if v := normalize(raw[i]); v != nil {
						return fmt.Sprint(v)
					}
				}
				return ""
			}
			out = append(out, TriggerInfo{Name: str("trigger"), Timing: str("timing"), Event: str("event")})
		}
		return out, rows.Err()
	}
	rows, err := c.DB.QueryContext(ctx, `
		SELECT trigger_name, action_timing, event_manipulation FROM information_schema.triggers
		WHERE event_object_schema = `+c.ph(1)+` AND event_object_table = `+c.ph(2)+` ORDER BY 1, 3`, schema, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TriggerInfo
	for rows.Next() {
		var t TriggerInfo
		if err := rows.Scan(&t.Name, &t.Timing, &t.Event); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (c *Conn) partitions(ctx context.Context, schema, name string) ([]PartitionInfo, error) {
	var q string
	if c.isPG() {
		q = `SELECT ch.relname, '', COALESCE(pg_get_expr(ch.relpartbound, ch.oid), ''), ''
		     FROM pg_inherits i JOIN pg_class ch ON ch.oid = i.inhrelid
		     JOIN pg_class p ON p.oid = i.inhparent JOIN pg_namespace n ON n.oid = p.relnamespace
		     WHERE n.nspname = $1 AND p.relname = $2 ORDER BY 1`
	} else {
		q = `SELECT partition_name, COALESCE(partition_method, ''), COALESCE(partition_expression, ''), COALESCE(partition_description, '')
		     FROM information_schema.partitions
		     WHERE table_schema = ? AND table_name = ? AND partition_name IS NOT NULL ORDER BY partition_ordinal_position`
	}
	rows, err := c.DB.QueryContext(ctx, q, schema, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PartitionInfo
	for rows.Next() {
		var p PartitionInfo
		var nm sql.NullString
		if err := rows.Scan(&nm, &p.Method, &p.Expression, &p.Description); err != nil {
			return nil, err
		}
		p.Name = nm.String
		out = append(out, p)
	}
	return out, rows.Err()
}

// ColumnRef is a column name with its type, for editor autocompletion.
type ColumnRef struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// SchemaColumns returns every column of every table/view in one schema, keyed by table name.
// One information_schema query, so the editor can complete columns without expanding tables.
func (c *Conn) SchemaColumns(ctx context.Context, schema string) (map[string][]ColumnRef, error) {
	var q string
	if c.isPG() {
		q = `SELECT table_name, column_name, CASE WHEN data_type = 'USER-DEFINED' THEN udt_name ELSE data_type END
		     FROM information_schema.columns WHERE table_schema = $1 ORDER BY table_name, ordinal_position`
	} else {
		q = `SELECT table_name, column_name, column_type FROM information_schema.columns WHERE table_schema = ? ORDER BY table_name, ordinal_position`
	}
	rows, err := c.DB.QueryContext(ctx, q, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]ColumnRef{}
	for rows.Next() {
		var t, n, ty string
		if err := rows.Scan(&t, &n, &ty); err != nil {
			return nil, err
		}
		out[t] = append(out[t], ColumnRef{Name: n, Type: ty})
	}
	return out, rows.Err()
}

func (c *Conn) tableInfo(ctx context.Context, schema, name string) (TableInfo, error) {
	var ti TableInfo
	if c.isPG() {
		var cm sql.NullString
		err := c.DB.QueryRowContext(ctx, `SELECT obj_description((quote_ident($1::text) || '.' || quote_ident($2::text))::regclass, 'pg_class')`, schema, name).Scan(&cm)
		ti.Comment = cm.String
		return ti, err
	}
	var eng, coll, cm, rf sql.NullString
	var ai sql.NullInt64
	err := c.DB.QueryRowContext(ctx, `SELECT engine, table_collation, table_comment, row_format, auto_increment FROM information_schema.tables WHERE table_schema = ? AND table_name = ?`, schema, name).Scan(&eng, &coll, &cm, &rf, &ai)
	ti.Engine, ti.Collation, ti.Comment, ti.RowFormat, ti.AutoIncrement = eng.String, coll.String, cm.String, rf.String, ai.Int64
	return ti, err
}

// enrichColumns adds collation, generation expression, ON UPDATE, invisibility and identity
// from information_schema.columns (one filtered query; errors are ignored).
func (c *Conn) enrichColumns(ctx context.Context, schema, name string, cols []ColumnInfo) {
	byName := map[string]*ColumnInfo{}
	for i := range cols {
		byName[cols[i].Name] = &cols[i]
	}
	var rows *sql.Rows
	var err error
	if c.isPG() {
		rows, err = c.DB.QueryContext(ctx, `SELECT column_name, COALESCE(collation_name,''), COALESCE(generation_expression,''), COALESCE(identity_generation,'') FROM information_schema.columns WHERE table_schema = $1 AND table_name = $2`, schema, name)
	} else {
		rows, err = c.DB.QueryContext(ctx, `SELECT column_name, COALESCE(collation_name,''), COALESCE(generation_expression,''), '' FROM information_schema.columns WHERE table_schema = ? AND table_name = ?`, schema, name)
	}
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var n, coll, gen, ident string
		if rows.Scan(&n, &coll, &gen, &ident) != nil {
			continue
		}
		col := byName[n]
		if col == nil {
			continue
		}
		col.Collation, col.Identity = coll, ident
		if gen != "" {
			col.Generation = gen
			col.GenKind = "STORED"
		}
		ex := strings.ToUpper(col.Extra)
		if strings.Contains(ex, "VIRTUAL") {
			col.GenKind = "VIRTUAL"
		} else if strings.Contains(ex, "STORED") || strings.Contains(ex, "PERSISTENT") {
			col.GenKind = "STORED"
		}
		if i := strings.Index(strings.ToLower(col.Extra), "on update "); i >= 0 {
			rest := col.Extra[i+len("on update "):]
			if j := strings.Index(strings.ToUpper(rest), " INVISIBLE"); j >= 0 {
				rest = rest[:j]
			}
			col.OnUpdate = strings.TrimSpace(rest)
		}
		col.Hidden = strings.Contains(ex, "INVISIBLE")
	}
}

// SearchHit is one result of a server-wide name search.
type SearchHit struct {
	Kind   string `json:"kind"` // table | view | routine | column
	Schema string `json:"schema"`
	Name   string `json:"name"`
	Table  string `json:"table,omitempty"` // for columns
	Detail string `json:"detail,omitempty"`
}

// Search finds tables, views, routines and columns whose name contains term, across all
// user schemas of the server (system schemas excluded). Results are capped per kind.
func (c *Conn) Search(ctx context.Context, term string, limit int) ([]SearchHit, error) {
	if limit <= 0 {
		limit = 100
	}
	like := "%" + strings.NewReplacer("%", "\\%", "_", "\\_").Replace(term) + "%"
	var sysFilter, colSys, rtSys string
	if c.isPG() {
		sysFilter = "AND table_schema NOT IN ('pg_catalog','information_schema') AND table_schema NOT LIKE 'pg\\_%'"
		colSys = sysFilter
		rtSys = "AND routine_schema NOT IN ('pg_catalog','information_schema')"
	} else {
		sysFilter = "AND table_schema NOT IN ('information_schema','performance_schema','sys','mysql')"
		colSys = sysFilter
		rtSys = "AND routine_schema NOT IN ('information_schema','performance_schema','sys','mysql')"
	}
	p := func(n int) string { return c.ph(n) }
	var out []SearchHit
	// tables and views
	rows, err := c.DB.QueryContext(ctx, fmt.Sprintf(`SELECT table_schema, table_name, table_type FROM information_schema.tables WHERE table_name LIKE %s %s ORDER BY table_schema, table_name LIMIT %d`, p(1), sysFilter, limit), like)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var s, n, t string
		if rows.Scan(&s, &n, &t) == nil {
			k := "table"
			if strings.Contains(strings.ToUpper(t), "VIEW") {
				k = "view"
			}
			out = append(out, SearchHit{Kind: k, Schema: s, Name: n})
		}
	}
	rows.Close()
	// routines
	rows, err = c.DB.QueryContext(ctx, fmt.Sprintf(`SELECT routine_schema, routine_name, routine_type FROM information_schema.routines WHERE routine_name LIKE %s %s ORDER BY 1, 2 LIMIT %d`, p(1), rtSys, limit), like)
	if err == nil {
		for rows.Next() {
			var s, n, t string
			if rows.Scan(&s, &n, &t) == nil {
				out = append(out, SearchHit{Kind: "routine", Schema: s, Name: n, Detail: strings.ToLower(t)})
			}
		}
		rows.Close()
	}
	// columns
	rows, err = c.DB.QueryContext(ctx, fmt.Sprintf(`SELECT table_schema, table_name, column_name, %s FROM information_schema.columns WHERE column_name LIKE %s %s ORDER BY table_schema, table_name, ordinal_position LIMIT %d`,
		map[bool]string{true: "data_type", false: "column_type"}[c.isPG()], p(1), colSys, limit), like)
	if err == nil {
		for rows.Next() {
			var s, t, n, ty string
			if rows.Scan(&s, &t, &n, &ty) == nil {
				out = append(out, SearchHit{Kind: "column", Schema: s, Name: n, Table: t, Detail: ty})
			}
		}
		rows.Close()
	}
	return out, nil
}
