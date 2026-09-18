package db

import (
	"context"
	"database/sql"
	"encoding/csv"
	"fmt"
	"io"
	"strings"
	"time"
)

// Truncate empties a table.
func (c *Conn) Truncate(ctx context.Context, table string) error {
	_, err := c.DB.ExecContext(ctx, "TRUNCATE TABLE "+c.Quote(table))
	return err
}

// DDL returns the CREATE statement of a table (MySQL/MariaDB only).
func (c *Conn) DDL(ctx context.Context, table string) (string, error) {
	if c.isPG() {
		return "", fmt.Errorf("DDL export is only available for MySQL/MariaDB; use pg_dump (structure only) for PostgreSQL")
	}
	rows, err := c.DB.QueryContext(ctx, "SHOW CREATE TABLE "+c.Quote(table))
	if err != nil {
		return "", err
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	if !rows.Next() {
		return "", fmt.Errorf("no DDL returned")
	}
	raw := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range raw {
		ptrs[i] = &raw[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return "", err
	}
	if len(raw) < 2 {
		return "", fmt.Errorf("unexpected SHOW CREATE TABLE result")
	}
	return fmt.Sprint(normalize(raw[1])), nil
}

// ViewDDL returns the definition of a view.
func (c *Conn) ViewDDL(ctx context.Context, view string) (string, error) {
	if c.isPG() {
		schema, name := c.splitTable(view)
		var def string
		err := c.DB.QueryRowContext(ctx, `SELECT pg_get_viewdef(($1 || '.' || $2)::regclass, true)`, schema, name).Scan(&def)
		if err != nil {
			return "", err
		}
		return "CREATE OR REPLACE VIEW " + c.Quote(view) + " AS\n" + strings.TrimSpace(def), nil
	}
	rows, err := c.DB.QueryContext(ctx, "SHOW CREATE VIEW "+c.Quote(view))
	if err != nil {
		return "", err
	}
	defer rows.Close()
	return secondColumn(rows)
}

// RoutineSource returns the CREATE statement of a stored function / procedure.
func (c *Conn) RoutineSource(ctx context.Context, schema, name, kind string) (string, error) {
	if c.isPG() {
		rows, err := c.DB.QueryContext(ctx, `
			SELECT pg_get_functiondef(p.oid) FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
			WHERE n.nspname = $1 AND p.proname = $2`, schema, name)
		if err != nil {
			return "", err
		}
		defer rows.Close()
		var defs []string
		for rows.Next() {
			var d string
			if rows.Scan(&d) == nil {
				defs = append(defs, strings.TrimSpace(d)+";")
			}
		}
		if len(defs) == 0 {
			return "", fmt.Errorf("routine %s.%s not found", schema, name)
		}
		return strings.Join(defs, "\n\n"), nil
	}
	k := "FUNCTION"
	if strings.EqualFold(kind, "PROCEDURE") {
		k = "PROCEDURE"
	}
	rows, err := c.DB.QueryContext(ctx, "SHOW CREATE "+k+" "+c.Quote(schema+"."+name))
	if err != nil {
		return "", err
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	if !rows.Next() {
		return "", fmt.Errorf("no source returned")
	}
	raw := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range raw {
		ptrs[i] = &raw[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return "", err
	}
	for i, cn := range cols {
		if strings.HasPrefix(cn, "Create ") {
			if v := normalize(raw[i]); v != nil {
				return "DELIMITER $$\n" + fmt.Sprint(v) + "$$\nDELIMITER ;", nil
			}
		}
	}
	return "", fmt.Errorf("routine source is not available (missing privileges?)")
}

func secondColumn(rows *sql.Rows) (string, error) {
	cols, _ := rows.Columns()
	if !rows.Next() {
		return "", fmt.Errorf("no DDL returned")
	}
	raw := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range raw {
		ptrs[i] = &raw[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return "", err
	}
	if len(raw) < 2 {
		return "", fmt.Errorf("unexpected result")
	}
	return fmt.Sprint(normalize(raw[1])), nil
}

// ExportCSV streams the result of q (run with schema as default database) to w as CSV.
// Returns the number of data rows written.
func (c *Conn) ExportCSV(ctx context.Context, schema, q string, w io.Writer) (int64, error) {
	cn, err := c.DB.Conn(ctx)
	if err != nil {
		return 0, err
	}
	defer cn.Close()
	if err := c.useSchema(ctx, cn, schema); err != nil {
		return 0, err
	}
	rows, err := cn.QueryContext(ctx, q)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return 0, err
	}
	cw := csv.NewWriter(w)
	if err := cw.Write(cols); err != nil {
		return 0, err
	}
	raw := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range raw {
		ptrs[i] = &raw[i]
	}
	rec := make([]string, len(cols))
	var n int64
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return n, err
		}
		for i, v := range raw {
			switch t := normalize(v).(type) {
			case nil:
				rec[i] = ""
			case string:
				rec[i] = t
			case time.Time:
				rec[i] = t.Format("2006-01-02 15:04:05")
			default:
				rec[i] = fmt.Sprint(t)
			}
		}
		if err := cw.Write(rec); err != nil {
			return n, err
		}
		n++
		if n%5000 == 0 {
			cw.Flush()
			if err := cw.Error(); err != nil {
				return n, err
			}
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return n, err
	}
	return n, rows.Err()
}

// SelectAll builds the unlimited SELECT used for table exports.
func (c *Conn) SelectAll(table string) string { return "SELECT * FROM " + c.Quote(table) }

var _ = strings.TrimSpace
