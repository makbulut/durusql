package db

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// SplitStatements splits a script on ';' outside of strings, quoted identifiers and comments.
// It also understands MySQL's DELIMITER command (used in routine definitions).
func SplitStatements(script string) []string {
	var out []string
	var cur strings.Builder
	delim := ";"
	flush := func() {
		s := strings.TrimSpace(cur.String())
		cur.Reset()
		if s != "" {
			out = append(out, s)
		}
	}
	i := 0
	n := len(script)
	lineStart := true
	for i < n {
		c := script[i]
		// DELIMITER xx (MySQL client command) at line start
		if lineStart && strings.HasPrefix(strings.ToUpper(script[i:min(i+10, n)]), "DELIMITER ") {
			flush()
			j := i + 10
			for j < n && (script[j] == ' ' || script[j] == '\t') {
				j++
			}
			k := j
			for k < n && script[k] != '\n' && script[k] != '\r' {
				k++
			}
			delim = strings.TrimSpace(script[j:k])
			if delim == "" {
				delim = ";"
			}
			i = k
			continue
		}
		lineStart = c == '\n'
		switch {
		case c == '\'' || c == '"' || c == '`':
			q := c
			cur.WriteByte(c)
			i++
			for i < n {
				cur.WriteByte(script[i])
				if script[i] == '\\' && q != '`' && i+1 < n {
					i++
					cur.WriteByte(script[i])
				} else if script[i] == q {
					if i+1 < n && script[i+1] == q { // doubled quote
						i++
						cur.WriteByte(script[i])
					} else {
						i++
						break
					}
				}
				i++
			}
			continue
		case c == '-' && i+1 < n && script[i+1] == '-':
			for i < n && script[i] != '\n' {
				cur.WriteByte(script[i])
				i++
			}
			continue
		case c == '#' && !strings.Contains(delim, "#"):
			for i < n && script[i] != '\n' {
				cur.WriteByte(script[i])
				i++
			}
			continue
		case c == '/' && i+1 < n && script[i+1] == '*':
			j := strings.Index(script[i+2:], "*/")
			if j < 0 {
				cur.WriteString(script[i:])
				i = n
			} else {
				cur.WriteString(script[i : i+2+j+2])
				i += 2 + j + 2
			}
			continue
		}
		if strings.HasPrefix(script[i:], delim) {
			flush()
			i += len(delim)
			continue
		}
		cur.WriteByte(c)
		i++
	}
	flush()
	return out
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// execer is what a statement runs on: a plain connection or an open transaction.
type execer interface {
	ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error)
}

// Session keeps a dedicated connection for manual-transaction mode.
type Session struct {
	conn  *sql.Conn
	tx    *sql.Tx
	Count int // statements run inside the open transaction
}

func (s *Session) Open() bool { return s != nil && s.tx != nil }

// Begin starts a transaction on a dedicated connection with schema as default database.
func (c *Conn) Begin(ctx context.Context, schema string) (*Session, error) {
	cn, err := c.DB.Conn(ctx)
	if err != nil {
		return nil, err
	}
	if err := c.useSchema(ctx, cn, schema); err != nil {
		cn.Close()
		return nil, err
	}
	tx, err := cn.BeginTx(ctx, nil)
	if err != nil {
		cn.Close()
		return nil, err
	}
	return &Session{conn: cn, tx: tx}, nil
}

func (s *Session) Commit() error {
	if s == nil || s.tx == nil {
		return nil
	}
	err := s.tx.Commit()
	s.tx = nil
	s.conn.Close()
	return err
}

func (s *Session) Rollback() error {
	if s == nil || s.tx == nil {
		return nil
	}
	err := s.tx.Rollback()
	s.tx = nil
	s.conn.Close()
	return err
}

// RunOne executes a single statement on ex (connection or transaction).
func (c *Conn) runOne(ctx context.Context, ex execer, q string, limit int) (*Result, error) {
	start := time.Now()
	res := &Result{Columns: []string{}, Rows: [][]any{}, SQL: q}
	if !isRead(q) {
		r, err := ex.ExecContext(ctx, q)
		if err != nil {
			return nil, err
		}
		res.RowsAffected, _ = r.RowsAffected()
		res.Ms = time.Since(start).Milliseconds()
		return res, nil
	}
	rows, err := ex.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	res.Columns, err = rows.Columns()
	if err != nil {
		return nil, err
	}
	n := len(res.Columns)
	for rows.Next() {
		if limit > 0 && len(res.Rows) >= limit {
			res.Truncated = true
			break
		}
		raw := make([]any, n)
		ptrs := make([]any, n)
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		for i, v := range raw {
			raw[i] = normalize(v)
		}
		res.Rows = append(res.Rows, raw)
	}
	res.Ms = time.Since(start).Milliseconds()
	return res, rows.Err()
}

// RunScript executes every statement of script in order. With a session (manual transaction
// mode) they run inside it; otherwise on one pooled connection with schema as default database.
// Execution stops at the first error; the results so far are returned together with the error.
func (c *Conn) RunScript(ctx context.Context, sess *Session, schema, script string, limit int) ([]*Result, error) {
	stmts := SplitStatements(script)
	if len(stmts) == 0 {
		return nil, nil
	}
	var ex execer
	if sess.Open() {
		ex = sess.tx
	} else {
		cn, err := c.DB.Conn(ctx)
		if err != nil {
			return nil, err
		}
		defer cn.Close()
		if err := c.useSchema(ctx, cn, schema); err != nil {
			return nil, err
		}
		ex = cn
	}
	var out []*Result
	for _, q := range stmts {
		r, err := c.runOne(ctx, ex, q, limit)
		if err != nil {
			return out, &StatementError{Index: len(out), SQL: q, Err: err}
		}
		if sess.Open() {
			sess.Count++
		}
		out = append(out, r)
	}
	return out, nil
}

// StatementError says which statement of a script failed.
type StatementError struct {
	Index int
	SQL   string
	Err   error
}

func (e *StatementError) Error() string { return e.Err.Error() }
func (e *StatementError) Unwrap() error { return e.Err }
