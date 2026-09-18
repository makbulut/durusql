package db

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Change is one pending grid edit. Key holds the original values of the primary-key columns
// (or of every column when the table has no key); Values holds the new column values.
type Change struct {
	Op     string         `json:"op"` // update | delete | insert
	Key    map[string]any `json:"key"`
	Values map[string]any `json:"values"`
}

var selectFrom = regexp.MustCompile(`(?is)^\s*select\s+.+?\s+from\s+([\w$]+(?:\s*\.\s*[\w$]+)?|` + "`[^`]+`(?:\\s*\\.\\s*`[^`]+`)?" + `|"[^"]+"(?:\s*\.\s*"[^"]+")?)\s*(?:as\s+\w+\s*|\w+\s*)?(?:where\b|order\b|limit\b|group\b|having\b|;|$)`)

// DetectTable returns the single table a SELECT reads from, or "" when the statement is
// anything else (joins, subqueries, unions, DML …). Used to decide whether a result is editable.
func DetectTable(q string) string {
	low := strings.ToLower(q)
	for _, bad := range []string{" join ", " union ", "(select", "( select", " group by ", " distinct ", "count(", "sum(", "avg(", "min(", "max("} {
		if strings.Contains(low, bad) {
			return ""
		}
	}
	m := selectFrom.FindStringSubmatch(q)
	if m == nil {
		return ""
	}
	parts := strings.Split(m[1], ".")
	for i, p := range parts {
		parts[i] = strings.Trim(strings.TrimSpace(p), "`\"")
	}
	return strings.Join(parts, ".")
}

// PrimaryKey returns the primary-key column names of table ("schema.table" or "table").
func (c *Conn) PrimaryKey(ctx context.Context, table string) ([]string, error) {
	if c.doc != nil {
		return nil, nil
	}
	schema, name := "", table
	if i := strings.LastIndexByte(table, '.'); i > 0 {
		schema, name = table[:i], table[i+1:]
	}
	if c.isPG() {
		if schema == "" {
			schema = "public"
		}
		return c.strings(ctx, `
			SELECT a.attname FROM pg_index i
			JOIN pg_class t ON t.oid = i.indrelid
			JOIN pg_namespace n ON n.oid = t.relnamespace
			JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = ANY(i.indkey)
			WHERE i.indisprimary AND n.nspname = $1 AND t.relname = $2
			ORDER BY array_position(i.indkey, a.attnum)`, schema, name)
	}
	if schema == "" {
		return c.strings(ctx, `
			SELECT column_name FROM information_schema.key_column_usage
			WHERE constraint_name = 'PRIMARY' AND table_schema = DATABASE() AND table_name = ?
			ORDER BY ordinal_position`, name)
	}
	return c.strings(ctx, `
		SELECT column_name FROM information_schema.key_column_usage
		WHERE constraint_name = 'PRIMARY' AND table_schema = ? AND table_name = ?
		ORDER BY ordinal_position`, schema, name)
}

// Apply runs all changes against table inside one transaction. Every UPDATE/DELETE must match
// exactly one row, otherwise the whole batch is rolled back.
func (c *Conn) Apply(ctx context.Context, table string, changes []Change) (int64, error) {
	if c.doc != nil {
		return 0, fmt.Errorf("grid edits are %w", errDocUnsupported)
	}
	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	n, err := c.applyOn(ctx, tx, table, changes)
	if err != nil {
		return 0, err
	}
	return n, tx.Commit()
}

// ApplyIn runs the changes inside an open manual-mode session (no commit; the user commits).
// With a savepoint-less driver we cannot undo a partial batch, so the multi-row guard is checked
// before each statement runs by counting matches first.
func (c *Conn) ApplyIn(ctx context.Context, sess *Session, table string, changes []Change) (int64, error) {
	if c.doc != nil {
		return 0, fmt.Errorf("grid edits are %w", errDocUnsupported)
	}
	if !sess.Open() {
		return 0, fmt.Errorf("no open transaction")
	}
	// pre-check: every UPDATE/DELETE key must match exactly one row
	for i, ch := range changes {
		if ch.Op == "insert" {
			continue
		}
		q, args, err := c.buildCount(table, ch)
		if err != nil {
			return 0, fmt.Errorf("change %d: %w", i+1, err)
		}
		var n int64
		if err := sess.tx.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
			return 0, err
		}
		if n != 1 {
			return 0, fmt.Errorf("%s would affect %d rows instead of 1 (row not uniquely identified); nothing was changed", strings.ToUpper(ch.Op), n)
		}
	}
	n, err := c.applyOn(ctx, sess.tx, table, changes)
	if err == nil {
		sess.Count += len(changes)
	}
	return n, err
}

func (c *Conn) applyOn(ctx context.Context, ex execer, table string, changes []Change) (int64, error) {
	var total int64
	for i, ch := range changes {
		q, args, err := c.buildChange(table, ch)
		if err != nil {
			return 0, fmt.Errorf("change %d: %w", i+1, err)
		}
		res, err := ex.ExecContext(ctx, q, args...)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", strings.ToUpper(ch.Op), err)
		}
		n, _ := res.RowsAffected()
		if ch.Op != "insert" && n != 1 {
			return 0, fmt.Errorf("%s would affect %d rows instead of 1 (row not uniquely identified); nothing was changed", strings.ToUpper(ch.Op), n)
		}
		total += n
	}
	return total, nil
}

// buildCount builds "SELECT COUNT(*) FROM table WHERE <key>" for a change's key.
func (c *Conn) buildCount(table string, ch Change) (string, []any, error) {
	var args []any
	if len(ch.Key) == 0 {
		return "", nil, fmt.Errorf("no key values")
	}
	var parts []string
	for _, col := range sortedKeys(ch.Key) {
		v := ch.Key[col]
		if v == nil {
			parts = append(parts, c.Quote(col)+" IS NULL")
			continue
		}
		args = append(args, v)
		if c.isPG() {
			parts = append(parts, fmt.Sprintf("%s = $%d", c.Quote(col), len(args)))
		} else {
			parts = append(parts, c.Quote(col)+" = ?")
		}
	}
	return "SELECT COUNT(*) FROM " + c.Quote(table) + " WHERE " + strings.Join(parts, " AND "), args, nil
}

func (c *Conn) buildChange(table string, ch Change) (string, []any, error) {
	var args []any
	ph := func() string {
		if c.isPG() {
			return fmt.Sprintf("$%d", len(args))
		}
		return "?"
	}
	where := func() (string, error) {
		if len(ch.Key) == 0 {
			return "", fmt.Errorf("no key values")
		}
		var parts []string
		for _, col := range sortedKeys(ch.Key) {
			v := ch.Key[col]
			if v == nil {
				parts = append(parts, c.Quote(col)+" IS NULL")
				continue
			}
			args = append(args, v)
			parts = append(parts, c.Quote(col)+" = "+ph())
		}
		return " WHERE " + strings.Join(parts, " AND "), nil
	}
	switch ch.Op {
	case "update":
		if len(ch.Values) == 0 {
			return "", nil, fmt.Errorf("nothing to update")
		}
		var sets []string
		for _, col := range sortedKeys(ch.Values) {
			args = append(args, ch.Values[col])
			sets = append(sets, c.Quote(col)+" = "+ph())
		}
		w, err := where()
		if err != nil {
			return "", nil, err
		}
		return "UPDATE " + c.Quote(table) + " SET " + strings.Join(sets, ", ") + w, args, nil
	case "delete":
		w, err := where()
		if err != nil {
			return "", nil, err
		}
		return "DELETE FROM " + c.Quote(table) + w, args, nil
	case "insert":
		if len(ch.Values) == 0 {
			return "", nil, fmt.Errorf("empty row")
		}
		var cols, vals []string
		for _, col := range sortedKeys(ch.Values) {
			args = append(args, ch.Values[col])
			cols = append(cols, c.Quote(col))
			vals = append(vals, ph())
		}
		return "INSERT INTO " + c.Quote(table) + " (" + strings.Join(cols, ", ") + ") VALUES (" + strings.Join(vals, ", ") + ")", args, nil
	}
	return "", nil, fmt.Errorf("unknown op %q", ch.Op)
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

var _ = sql.ErrNoRows
