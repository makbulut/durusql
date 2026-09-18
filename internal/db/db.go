package db

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"golang.org/x/crypto/ssh"

	"durusql/internal/config"
	"durusql/internal/tunnel"
)

type Conn struct {
	Cfg config.Connection
	DB  *sql.DB // nil for document stores
	ssh *ssh.Client
	doc *docStore // OpenSearch / Elasticsearch
}

type Result struct {
	Columns      []string `json:"columns"`
	Rows         [][]any  `json:"rows"`
	RowsAffected int64    `json:"rowsAffected"`
	Ms           int64    `json:"ms"`
	Truncated    bool     `json:"truncated"`
	Table        string   `json:"table,omitempty"` // single source table when the result is editable
	Keys         []string `json:"keys,omitempty"`  // its primary-key columns
	SQL          string   `json:"sql,omitempty"`   // the statement that produced it (scripts)
}

type Column struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
	Key      string `json:"key"`
}

func Open(c config.Connection) (*Conn, error) {
	conn := &Conn{Cfg: c}
	var dialer func(ctx context.Context, network, addr string) (net.Conn, error)

	if c.SSH != nil && c.SSH.Host != "" {
		client, err := tunnel.Dial(c.SSH)
		if err != nil {
			return nil, err
		}
		conn.ssh = client
		dialer = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return client.Dial(network, addr)
		}
	}

	switch c.Driver {
	case "opensearch", "elasticsearch":
		d, err := openDoc(c, dialer)
		if err != nil {
			conn.Close()
			return nil, err
		}
		conn.doc = d
		return conn, nil

	case "mysql", "mariadb":
		mc := mysql.NewConfig()
		mc.User, mc.Passwd, mc.DBName = c.User, c.Password, c.Database
		mc.Net, mc.Addr = "tcp", fmt.Sprintf("%s:%d", c.Host, c.Port)
		mc.ParseTime = true
		mc.ClientFoundRows = true   // affected rows = matched rows, so no-op edits still count as 1
		mc.InterpolateParams = true // one round trip per parameterized query instead of prepare+execute
		mc.Timeout = 10 * time.Second
		// autocommit=1: servers configured with autocommit=0 would otherwise pin every pooled
		// connection to the snapshot of its first SELECT, so reloads after edits look stale.
		mc.Params = map[string]string{"charset": "utf8mb4", "autocommit": "1"}
		if dialer != nil {
			netName := "ssh-" + c.ID
			mysql.RegisterDialContext(netName, func(ctx context.Context, addr string) (net.Conn, error) {
				return dialer(ctx, "tcp", addr)
			})
			mc.Net = netName
		}
		connector, err := mysql.NewConnector(mc)
		if err != nil {
			conn.Close()
			return nil, err
		}
		conn.DB = sql.OpenDB(connector)

	case "postgres", "postgresql":
		pc, err := pgx.ParseConfig(fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=prefer",
			c.User, c.Password, c.Host, c.Port, c.Database))
		if err != nil {
			conn.Close()
			return nil, err
		}
		pc.ConnectTimeout = 10 * time.Second
		// Simple protocol: parameters are sent as text and cast by the server, so grid edits
		// typed as strings land in int/date/… columns without client-side type juggling.
		pc.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
		if dialer != nil {
			pc.DialFunc = dialer
			pc.TLSConfig = nil // through tunnel, plain is fine
		}
		conn.DB = stdlib.OpenDB(*pc)

	default:
		conn.Close()
		return nil, fmt.Errorf("unsupported driver %q", c.Driver)
	}

	// The explorer fans metadata queries out concurrently, so keep a few warm connections around.
	conn.DB.SetMaxOpenConns(poolSize)
	conn.DB.SetMaxIdleConns(poolSize)
	conn.DB.SetConnMaxIdleTime(5 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := conn.DB.PingContext(ctx); err != nil {
		conn.Close()
		return nil, err
	}
	go func() { // warm the rest of the pool in the background; connecting stays fast
		wctx, wcancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer wcancel()
		conn.warm(wctx)
	}()
	return conn, nil
}

const poolSize = 6

// warm opens the remaining pooled connections in parallel so the first burst of metadata
// queries does not pay one handshake per connection (noticeable over SSH tunnels).
func (c *Conn) warm(ctx context.Context) {
	held := make([]*sql.Conn, 0, poolSize)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < poolSize-1; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if cn, err := c.DB.Conn(ctx); err == nil {
				mu.Lock()
				held = append(held, cn)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	for _, cn := range held {
		cn.Close() // returns it to the idle pool
	}
}

func (c *Conn) Close() {
	if c.DB != nil {
		c.DB.Close()
	}
	if c.doc != nil {
		c.doc.close()
	}
	if c.ssh != nil {
		c.ssh.Close()
	}
}

func (c *Conn) isPG() bool { return c.Cfg.Driver == "postgres" || c.Cfg.Driver == "postgresql" }

var readPrefixes = []string{"SELECT", "SHOW", "WITH", "EXPLAIN", "DESCRIBE", "DESC", "VALUES", "TABLE", "PRAGMA"}

func isRead(q string) bool {
	u := strings.ToUpper(strings.TrimSpace(q))
	for _, p := range readPrefixes {
		if strings.HasPrefix(u, p+" ") || strings.HasPrefix(u, p+"\n") || u == p {
			return true
		}
	}
	return false
}

// Query executes a single statement; read statements return rows (capped at limit).
func (c *Conn) Query(ctx context.Context, q string, limit int) (*Result, error) {
	return c.QueryIn(ctx, "", q, limit)
}

// QueryIn runs q with schema as the default database (MySQL USE / PostgreSQL search_path),
// so unqualified table names in a console resolve against the database picked in the UI.
// The default is set on the dedicated pooled connection right before the statement.
func (c *Conn) QueryIn(ctx context.Context, schema, q string, limit int) (*Result, error) {
	if c.doc != nil {
		return c.doc.run(ctx, q, limit)
	}
	start := time.Now()
	res := &Result{Columns: []string{}, Rows: [][]any{}}

	cn, err := c.DB.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer cn.Close()
	if err := c.useSchema(ctx, cn, schema); err != nil {
		return nil, err
	}

	if !isRead(q) {
		r, err := cn.ExecContext(ctx, q)
		if err != nil {
			return nil, err
		}
		res.RowsAffected, _ = r.RowsAffected()
		res.Ms = time.Since(start).Milliseconds()
		return res, nil
	}

	rows, err := cn.QueryContext(ctx, q)
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

// useSchema selects the default database on one connection. With an empty schema it falls back
// to the connection's configured database so pooled connections don't leak a previous choice.
func (c *Conn) useSchema(ctx context.Context, cn *sql.Conn, schema string) error {
	if schema == "" {
		schema = c.Cfg.Database
	}
	if c.isPG() {
		if schema == "" {
			_, err := cn.ExecContext(ctx, "RESET search_path")
			return err
		}
		_, err := cn.ExecContext(ctx, "SET search_path TO "+c.Quote(schema)+", public")
		return err
	}
	if schema == "" {
		return nil
	}
	_, err := cn.ExecContext(ctx, "USE "+c.Quote(schema))
	return err
}

func normalize(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case []byte:
		return string(t)
	case time.Time:
		return t.Format("2006-01-02 15:04:05")
	default:
		return v
	}
}

// Databases lists the schemas a user would browse: MySQL databases, or PostgreSQL schemas
// of the connected database. The connection's default database/schema comes first.
func (c *Conn) Databases(ctx context.Context) ([]string, error) {
	if c.doc != nil {
		return []string{c.doc.cluster}, nil
	}
	var q string
	if c.isPG() {
		q = `SELECT schema_name FROM information_schema.schemata
		     WHERE schema_name NOT LIKE 'pg\_%' AND schema_name <> 'information_schema'
		     ORDER BY (schema_name <> 'public'), 1`
	} else {
		q = `SELECT schema_name FROM information_schema.schemata
		     WHERE schema_name NOT IN ('information_schema','performance_schema','sys')
		     ORDER BY (schema_name <> DATABASE()), (schema_name = 'mysql'), 1`
	}
	return c.strings(ctx, q)
}

// Tables lists the tables and views of one schema/database.
func (c *Conn) Tables(ctx context.Context, schema string) ([]string, error) {
	if c.doc != nil {
		o, err := c.doc.schemaObjects(ctx)
		if err != nil {
			return nil, err
		}
		return o.Tables, nil
	}
	var q string
	if c.isPG() {
		q = `SELECT table_name FROM information_schema.tables WHERE table_schema = $1 ORDER BY 1`
	} else {
		q = `SELECT table_name FROM information_schema.tables WHERE table_schema = ? ORDER BY 1`
	}
	return c.strings(ctx, q, schema)
}

func (c *Conn) strings(ctx context.Context, q string, args ...any) ([]string, error) {
	rows, err := c.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (c *Conn) Columns(ctx context.Context, table string) ([]Column, error) {
	if c.doc != nil {
		return c.doc.columns(ctx, table)
	}
	var rows *sql.Rows
	var err error
	if c.isPG() {
		schema, name := "public", table
		if i := strings.IndexByte(table, '.'); i > 0 {
			schema, name = table[:i], table[i+1:]
		}
		rows, err = c.DB.QueryContext(ctx, `
			SELECT c.column_name, c.data_type, c.is_nullable='YES',
			       COALESCE((SELECT 'PRI' FROM information_schema.key_column_usage k
			                 JOIN information_schema.table_constraints t ON t.constraint_name=k.constraint_name
			                 WHERE t.constraint_type='PRIMARY KEY' AND k.table_schema=c.table_schema
			                   AND k.table_name=c.table_name AND k.column_name=c.column_name LIMIT 1),'')
			FROM information_schema.columns c
			WHERE c.table_schema=$1 AND c.table_name=$2 ORDER BY c.ordinal_position`, schema, name)
	} else {
		if i := strings.IndexByte(table, '.'); i > 0 {
			rows, err = c.DB.QueryContext(ctx, `
				SELECT column_name, column_type, is_nullable='YES', column_key
				FROM information_schema.columns
				WHERE table_schema=? AND table_name=? ORDER BY ordinal_position`, table[:i], table[i+1:])
		} else {
			rows, err = c.DB.QueryContext(ctx, `
				SELECT column_name, column_type, is_nullable='YES', column_key
				FROM information_schema.columns
				WHERE table_schema=DATABASE() AND table_name=? ORDER BY ordinal_position`, table)
		}
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Column{}
	for rows.Next() {
		var col Column
		if err := rows.Scan(&col.Name, &col.Type, &col.Nullable, &col.Key); err != nil {
			return nil, err
		}
		out = append(out, col)
	}
	return out, rows.Err()
}

// Quote returns a driver-appropriate quoted identifier.
func (c *Conn) Quote(ident string) string {
	if c.doc != nil {
		return c.doc.quote(ident)
	}
	parts := strings.Split(ident, ".")
	for i, p := range parts {
		if c.isPG() {
			parts[i] = `"` + strings.ReplaceAll(p, `"`, `""`) + `"`
		} else {
			parts[i] = "`" + strings.ReplaceAll(p, "`", "``") + "`"
		}
	}
	return strings.Join(parts, ".")
}
