package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"durusql/internal/config"
	"durusql/internal/datagrip"
	"durusql/internal/db"
	"durusql/internal/dump"
	"durusql/internal/keyring"
	"durusql/internal/sysinfo"
	"durusql/internal/update"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx      context.Context
	store    *config.Store
	mu       sync.Mutex
	open     map[string]*db.Conn
	running  map[string]context.CancelFunc // runID → cancel of a query in flight
	sessions map[string]*db.Session        // connection id → open manual transaction
	txMode   map[string]string             // connection id → "auto" | "manual"
}

func NewApp() *App {
	return &App{open: map[string]*db.Conn{}, running: map[string]context.CancelFunc{}, sessions: map[string]*db.Session{}, txMode: map[string]string{}}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	s, err := config.Open()
	if err != nil {
		panic(err)
	}
	a.store = s
}

func (a *App) shutdown(ctx context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, c := range a.open {
		c.Close()
	}
}

// ---- connections ----

func (a *App) ListConnections() []config.Connection { return a.store.Connections() }

func (a *App) SaveConnection(c config.Connection) (config.Connection, error) {
	a.Disconnect(c.ID)
	return a.store.Save(c)
}

func (a *App) DeleteConnection(id string) error {
	a.Disconnect(id)
	return a.store.Delete(id)
}

func (a *App) TestConnection(c config.Connection) error {
	conn, err := db.Open(c)
	if err != nil {
		return err
	}
	conn.Close()
	return nil
}

func (a *App) Connect(id string) error {
	a.mu.Lock()
	if _, ok := a.open[id]; ok {
		a.mu.Unlock()
		return nil
	}
	a.mu.Unlock()

	cfg, ok := a.store.Get(id)
	if !ok {
		return errors.New("connection not found")
	}
	conn, err := db.Open(cfg)
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.open[id] = conn
	a.mu.Unlock()
	return nil
}

func (a *App) Disconnect(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if s, ok := a.sessions[id]; ok {
		s.Rollback()
		delete(a.sessions, id)
	}
	if c, ok := a.open[id]; ok {
		c.Close()
		delete(a.open, id)
	}
}

func (a *App) IsConnected(id string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, ok := a.open[id]
	return ok
}

func (a *App) conn(id string) (*db.Conn, error) {
	if err := a.Connect(id); err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.open[id], nil
}

// Version returns the build version (see package.sh).
func (a *App) Version() string { return version }

// ---- settings & updates ----

func (a *App) GetSettings() config.Settings {
	st := a.store.Settings()
	if st.UpdateURL == "" {
		st.UpdateURL = updateSource // compiled-in default (package.sh -X main.updateSource=…)
	}
	return st
}
func (a *App) SaveSettings(st config.Settings) error { return a.store.SaveSettings(st) }

// CheckUpdate queries the release channel. It never fails hard: errors come back in Status.Error.
func (a *App) CheckUpdate(force bool) *update.Status {
	st := a.GetSettings()
	ctx, cancel := context.WithTimeout(a.ctx, 20*time.Second)
	defer cancel()
	res, err := update.Check(ctx, st.UpdateURL, st.UpdateChannel, version)
	if err != nil {
		res.Error = err.Error()
	} else {
		st.LastCheck = time.Now().Format(time.RFC3339)
		_ = a.store.SaveSettings(st)
		if res.Available && !force && st.SkipVersion == res.Latest {
			res.Available = false // user chose to skip this one
		}
	}
	return res
}

// DownloadUpdate fetches the package for this installation kind (or the given one), verifying
// its checksum, and emits "update:progress" events. Returns the local path.
func (a *App) DownloadUpdate(kind string) (string, error) {
	st := a.GetSettings()
	ctx, cancel := context.WithTimeout(a.ctx, time.Hour)
	defer cancel()
	rel, err := update.FetchRelease(ctx, st.UpdateURL, st.UpdateChannel)
	if err != nil {
		return "", err
	}
	if kind == "" {
		kind = update.InstallKind()
	}
	f, ok := rel.Files[kind]
	if !ok {
		return "", fmt.Errorf("the release has no %s package (available: %s)", kind, strings.Join(keys(rel.Files), ", "))
	}
	name := f.URL[strings.LastIndex(f.URL, "/")+1:]
	last := time.Now()
	path, err := update.Download(ctx, f, name, func(done, total int64) {
		if time.Since(last) > 150*time.Millisecond || done == total {
			last = time.Now()
			runtime.EventsEmit(a.ctx, "update:progress", map[string]any{"done": done, "total": total})
		}
	})
	return path, err
}

// InstallUpdate applies a downloaded package. When the binary was replaced in place the app
// relaunches itself and quits.
func (a *App) InstallUpdate(kind, path string) (string, error) {
	if kind == "" {
		kind = update.InstallKind()
	}
	restart, err := update.Install(kind, path)
	if err != nil {
		return "", err
	}
	if restart {
		a.shutdown(a.ctx)
		if err := update.Relaunch(); err != nil {
			return "", fmt.Errorf("installed, but relaunch failed: %v (start %s manually)", err, config.AppName)
		}
		go func() { time.Sleep(300 * time.Millisecond); runtime.Quit(a.ctx) }()
		return "restarting", nil
	}
	return "opened", nil
}

func keys(m map[string]update.File) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Stats returns memory/process usage for the status bar.
func (a *App) Stats() sysinfo.Stats { return sysinfo.Collect() }

// ---- import ----

type ImportResult struct {
	Imported int      `json:"imported"`
	IDs      []string `json:"ids"`
	Names    []string `json:"names"`
	Warnings []string `json:"warnings"`
}

// ImportDataGrip pulls every data source out of the local DataGrip install (plus saved
// passwords from the system keyring) and upserts them as connections. Re-running updates
// previously imported ones in place (they share the same dg-<uuid> id).
func (a *App) ImportDataGrip() (*ImportResult, error) {
	res, err := importDataGrip(a.store)
	if err != nil {
		return nil, err
	}
	for _, id := range res.IDs {
		a.Disconnect(id)
	}
	return res, nil
}

// ImportJetBrains imports data sources from explicit dataSources.xml files, from folders that
// are scanned for .idea projects (PhpStorm, IntelliJ, GoLand, …), and/or from pasted XML.
func (a *App) ImportJetBrains(paths []string, xmlText string) (*ImportResult, error) {
	var lookup datagrip.SecretLookup
	kr, err := keyring.Open()
	if err == nil {
		defer kr.Close()
		lookup = kr.LookupService
	}
	var files []string
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		if st.IsDir() {
			if filepath.Base(p) == ".idea" {
				p = filepath.Dir(p)
			}
			files = append(files, datagrip.FindProjectFiles(p, 6)...)
		} else {
			files = append(files, p)
		}
	}
	out := &ImportResult{IDs: []string{}, Names: []string{}, Warnings: []string{}}
	if kr == nil {
		out.Warnings = append(out.Warnings, "system keyring not available, passwords were not imported")
	}
	var results []*datagrip.Result
	var plain []string
	for _, f := range files {
		if strings.EqualFold(filepath.Ext(f), ".zip") {
			r, err := datagrip.ImportArchive(f, lookup)
			if err != nil {
				return nil, err
			}
			results = append(results, r)
			continue
		}
		plain = append(plain, f)
	}
	files = plain
	if len(files) > 0 {
		r, err := datagrip.ImportFiles(files, lookup)
		if err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	if strings.TrimSpace(xmlText) != "" {
		r, err := datagrip.ImportXML(xmlText, lookup)
		if err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	if len(results) == 0 {
		return nil, errors.New("nothing to import: choose an exported .xml/.zip, a folder with projects, or paste XML")
	}
	for _, res := range results {
		out.Warnings = append(out.Warnings, res.Warnings...)
		for _, c := range res.Connections {
			if prev, ok := a.store.Get(c.ID); ok {
				c.Favorite, c.Color = prev.Favorite, prev.Color
				if c.Password == "" {
					c.Password = prev.Password
				}
				if prev.Group != "" {
					c.Group = prev.Group
				}
			}
			if _, err := a.store.Save(c); err != nil {
				return nil, fmt.Errorf("save %s: %w", c.Name, err)
			}
			a.Disconnect(c.ID)
			out.Imported++
			out.IDs = append(out.IDs, c.ID)
			out.Names = append(out.Names, c.Name)
		}
	}
	if len(files) > 0 {
		out.Warnings = append([]string{fmt.Sprintf("scanned %d dataSources.xml file(s)", len(files))}, out.Warnings...)
	}
	return out, nil
}

func (a *App) PickDirectory(title string) (string, error) {
	home, _ := os.UserHomeDir()
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: title, DefaultDirectory: home})
}

func importDataGrip(store *config.Store) (*ImportResult, error) {
	var lookup datagrip.SecretLookup
	kr, err := keyring.Open()
	if err == nil {
		defer kr.Close()
		lookup = kr.LookupService
	}
	res, err := datagrip.Import(lookup)
	if err != nil {
		return nil, err
	}
	out := &ImportResult{IDs: []string{}, Names: []string{}, Warnings: res.Warnings}
	if kr == nil {
		out.Warnings = append([]string{"system keyring not available, passwords were not imported"}, out.Warnings...)
	}
	for _, c := range res.Connections {
		if prev, ok := store.Get(c.ID); ok {
			c.Favorite, c.Color = prev.Favorite, prev.Color // keep local tweaks
			if c.Password == "" {
				c.Password = prev.Password
			}
		}
		if _, err := store.Save(c); err != nil {
			return nil, fmt.Errorf("save %s: %w", c.Name, err)
		}
		out.Imported++
		out.IDs = append(out.IDs, c.ID)
		out.Names = append(out.Names, c.Name)
	}
	return out, nil
}

// ---- queries ----

func (a *App) RunQuery(id, sql string, limit int) (*db.Result, error) {
	return a.RunQueryIn(id, "", sql, limit)
}

// ScriptResult is what a console run returns: one result per statement, the failing statement
// (if any), and the state of the manual transaction afterwards.
type ScriptResult struct {
	Results   []*db.Result `json:"results"`
	Error     string       `json:"error,omitempty"`
	ErrorIdx  int          `json:"errorIdx"`
	ErrorSQL  string       `json:"errorSql,omitempty"`
	Cancelled bool         `json:"cancelled"`
	Tx        TxState      `json:"tx"`
}

type TxState struct {
	Mode  string `json:"mode"`  // auto | manual
	Open  bool   `json:"open"`  // a transaction is open (manual mode)
	Count int    `json:"count"` // statements in it
}

func (a *App) txState(id string) TxState {
	a.mu.Lock()
	defer a.mu.Unlock()
	mode := a.txMode[id]
	if mode == "" {
		mode = "auto"
	}
	st := TxState{Mode: mode}
	if s, ok := a.sessions[id]; ok && s.Open() {
		st.Open, st.Count = true, s.Count
	}
	return st
}

// RunScript runs every statement of script (split on ';') with schema as the default database.
// runID lets the frontend cancel it. In manual transaction mode statements run inside the open
// transaction, which is started on first use.
func (a *App) RunScript(id, schema, script string, limit int, runID string) (*ScriptResult, error) {
	c, err := a.conn(id)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 500
	}
	ctx, cancel := context.WithTimeout(a.ctx, 12*time.Hour)
	defer cancel()
	if runID != "" {
		a.mu.Lock()
		a.running[runID] = cancel
		a.mu.Unlock()
		defer func() { a.mu.Lock(); delete(a.running, runID); a.mu.Unlock() }()
	}

	var sess *db.Session
	a.mu.Lock()
	if a.txMode[id] == "manual" {
		sess = a.sessions[id]
		if !sess.Open() {
			a.mu.Unlock()
			ns, err := c.Begin(ctx, schema)
			if err != nil {
				return nil, err
			}
			a.mu.Lock()
			a.sessions[id] = ns
			sess = ns
		}
	}
	a.mu.Unlock()

	results, err := c.RunScript(ctx, sess, schema, script, limit)
	out := &ScriptResult{Results: results, ErrorIdx: -1}
	cfg, _ := a.store.Get(id)
	for _, r := range results {
		h := config.HistoryEntry{At: time.Now(), SQL: r.SQL, Ms: r.Ms, Rows: len(r.Rows)}
		if len(r.Columns) == 0 {
			h.Rows = int(r.RowsAffected)
		}
		a.store.AppendHistory(id, h)
		if t := db.DetectTable(r.SQL); t != "" && len(r.Columns) > 0 {
			if !strings.Contains(t, ".") {
				if schema != "" {
					t = schema + "." + t
				} else if cfg.Database != "" {
					t = cfg.Database + "." + t
				}
			}
			r.Table = t
			r.Keys, _ = c.PrimaryKey(ctx, t)
		}
	}
	if err != nil {
		var se *db.StatementError
		if errors.As(err, &se) {
			out.ErrorIdx, out.ErrorSQL = se.Index, se.SQL
			a.store.AppendHistory(id, config.HistoryEntry{At: time.Now(), SQL: se.SQL, Err: se.Err.Error()})
		}
		if ctx.Err() == context.Canceled {
			out.Cancelled = true
			out.Error = "cancelled"
		} else {
			out.Error = err.Error()
		}
	}
	if out.Results == nil {
		out.Results = []*db.Result{}
	}
	out.Tx = a.txState(id)
	return out, nil
}

// CancelQuery aborts a run started with RunScript(…, runID).
func (a *App) CancelQuery(runID string) {
	a.mu.Lock()
	cancel := a.running[runID]
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// SetTxMode switches a connection between auto-commit and manual transactions.
// Switching to auto with an open transaction asks nothing: it commits, matching DataGrip.
func (a *App) SetTxMode(id, mode string) (TxState, error) {
	if mode != "manual" {
		mode = "auto"
	}
	a.mu.Lock()
	a.txMode[id] = mode
	s := a.sessions[id]
	a.mu.Unlock()
	if mode == "auto" && s.Open() {
		if err := s.Commit(); err != nil {
			return a.txState(id), err
		}
	}
	return a.txState(id), nil
}

func (a *App) GetTxState(id string) TxState { return a.txState(id) }

func (a *App) Commit(id string) (TxState, error) {
	a.mu.Lock()
	s := a.sessions[id]
	delete(a.sessions, id)
	a.mu.Unlock()
	err := s.Commit()
	a.store.AppendHistory(id, config.HistoryEntry{At: time.Now(), SQL: "COMMIT"})
	return a.txState(id), err
}

func (a *App) Rollback(id string) (TxState, error) {
	a.mu.Lock()
	s := a.sessions[id]
	delete(a.sessions, id)
	a.mu.Unlock()
	err := s.Rollback()
	a.store.AppendHistory(id, config.HistoryEntry{At: time.Now(), SQL: "ROLLBACK"})
	return a.txState(id), err
}

// RunQueryIn runs sql with schema as the default database (empty = the connection's default).
func (a *App) RunQueryIn(id, schema, sql string, limit int) (*db.Result, error) {
	c, err := a.conn(id)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 500
	}
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Minute)
	defer cancel()
	res, err := c.QueryIn(ctx, schema, sql, limit)
	h := config.HistoryEntry{At: time.Now(), SQL: sql}
	if err != nil {
		h.Err = err.Error()
	} else {
		h.Ms, h.Rows = res.Ms, len(res.Rows)
		if t := db.DetectTable(sql); t != "" && len(res.Columns) > 0 {
			// qualify with the console's database so grid edits always target the right one
			if !strings.Contains(t, ".") {
				if schema != "" {
					t = schema + "." + t
				} else if cfg, ok := a.store.Get(id); ok && cfg.Database != "" {
					t = cfg.Database + "." + t
				}
			}
			res.Table = t
			res.Keys, _ = c.PrimaryKey(ctx, t) // best effort; without a key the grid matches on all columns
		}
	}
	a.store.AppendHistory(id, h)
	return res, err
}

func (a *App) PreviewTable(id, table string, limit int) (*db.Result, error) {
	c, err := a.conn(id)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 200
	}
	return a.RunQuery(id, fmt.Sprintf("SELECT * FROM %s LIMIT %d", c.Quote(table), limit), limit)
}

// ApplyChanges writes a batch of grid edits to table in one transaction and returns the
// number of affected rows.
func (a *App) ApplyChanges(id, table string, changes []db.Change) (int64, error) {
	c, err := a.conn(id)
	if err != nil {
		return 0, err
	}
	if len(changes) == 0 {
		return 0, nil
	}
	ctx, cancel := context.WithTimeout(a.ctx, 2*time.Minute)
	defer cancel()
	a.mu.Lock()
	sess := a.sessions[id]
	manual := a.txMode[id] == "manual"
	a.mu.Unlock()
	var n int64
	if manual {
		if !sess.Open() {
			ns, err := c.Begin(ctx, "")
			if err != nil {
				return 0, err
			}
			a.mu.Lock()
			a.sessions[id] = ns
			a.mu.Unlock()
			sess = ns
		}
		n, err = c.ApplyIn(ctx, sess, table, changes)
	} else {
		n, err = c.Apply(ctx, table, changes)
	}
	h := config.HistoryEntry{At: time.Now(), SQL: fmt.Sprintf("-- grid: %d change(s) on %s", len(changes), table), Rows: int(n)}
	if err != nil {
		h.Err = err.Error()
	}
	a.store.AppendHistory(id, h)
	return n, err
}

// ---- schema ----

func (a *App) ListDatabases(id string) ([]string, error) {
	c, err := a.conn(id)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()
	return c.Databases(ctx)
}

func (a *App) ListSchemaObjects(id, schema string) (*db.SchemaObjects, error) {
	c, err := a.conn(id)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 60*time.Second)
	defer cancel()
	return c.SchemaObjects(ctx, schema)
}

func (a *App) ListSchemaColumns(id, schema string) (map[string][]db.ColumnRef, error) {
	c, err := a.conn(id)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 60*time.Second)
	defer cancel()
	return c.SchemaColumns(ctx, schema)
}

// SearchObjects searches one connection's server for tables, views, routines and columns.
func (a *App) SearchObjects(id, term string, limit int) ([]db.SearchHit, error) {
	c, err := a.conn(id)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 20*time.Second)
	defer cancel()
	hits, err := c.Search(ctx, term, limit)
	if hits == nil {
		hits = []db.SearchHit{}
	}
	return hits, err
}

func (a *App) ListDatabaseObjects(id string) (*db.DatabaseObjects, error) {
	c, err := a.conn(id)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()
	return c.DatabaseObjectsList(ctx)
}

func (a *App) TableDetails(id, table string) (*db.TableDetails, error) {
	c, err := a.conn(id)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 60*time.Second)
	defer cancel()
	return c.TableDetails(ctx, table)
}

func (a *App) ListTables(id, schema string) ([]string, error) {
	c, err := a.conn(id)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()
	return c.Tables(ctx, schema)
}

func (a *App) ListColumns(id, table string) ([]db.Column, error) {
	c, err := a.conn(id)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()
	return c.Columns(ctx, table)
}

// ---- table operations: truncate, DDL, CSV export, dumps ----

func (a *App) TruncateTable(id, table string) error {
	c, err := a.conn(id)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 10*time.Minute)
	defer cancel()
	err = c.Truncate(ctx, table)
	h := config.HistoryEntry{At: time.Now(), SQL: "TRUNCATE TABLE " + table}
	if err != nil {
		h.Err = err.Error()
	}
	a.store.AppendHistory(id, h)
	return err
}

func (a *App) TableDDL(id, table string) (string, error) {
	c, err := a.conn(id)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(a.ctx, time.Minute)
	defer cancel()
	return c.DDL(ctx, table)
}

func (a *App) ViewDDL(id, view string) (string, error) {
	c, err := a.conn(id)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(a.ctx, time.Minute)
	defer cancel()
	return c.ViewDDL(ctx, view)
}

func (a *App) RoutineSource(id, schema, name, kind string) (string, error) {
	c, err := a.conn(id)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(a.ctx, time.Minute)
	defer cancel()
	return c.RoutineSource(ctx, schema, name, kind)
}

// TableDDLAny returns CREATE TABLE for MySQL (SHOW CREATE TABLE) and, for PostgreSQL,
// the pg_dump --schema-only output of the table.
func (a *App) TableDDLAny(id, table string) (string, error) {
	cfg, ok := a.store.Get(id)
	if !ok {
		return "", errors.New("connection not found")
	}
	if cfg.Driver != "postgres" && cfg.Driver != "postgresql" {
		return a.TableDDL(id, table)
	}
	schema, name := "public", table
	if i := strings.LastIndexByte(table, '.'); i > 0 {
		schema, name = table[:i], table[i+1:]
	}
	o := dump.Defaults(true)
	o.Database, o.Tables, o.SchemaOnly = schema, []string{name}, true
	ctx, cancel := context.WithTimeout(a.ctx, 2*time.Minute)
	defer cancel()
	return dump.RunToString(ctx, cfg, o)
}

// ExportQuery asks for a file and writes the query result as csv, json or xlsx.
func (a *App) ExportQuery(id, schema, sql, format, suggestedName string) (*FileResult, error) {
	c, err := a.conn(id)
	if err != nil {
		return nil, err
	}
	ext := map[string]string{"csv": "csv", "json": "json", "xlsx": "xlsx"}[format]
	if ext == "" {
		return nil, fmt.Errorf("unknown format %q", format)
	}
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "Export as " + strings.ToUpper(ext),
		DefaultFilename: suggestedName + "." + ext,
		Filters:         []runtime.FileFilter{{DisplayName: strings.ToUpper(ext) + " files", Pattern: "*." + ext}},
	})
	if err != nil || path == "" {
		return &FileResult{}, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 2*time.Hour)
	defer cancel()
	var n int64
	switch format {
	case "xlsx":
		n, err = c.ExportXLSX(ctx, schema, sql, suggestedName, path)
	default:
		f, err2 := os.Create(path)
		if err2 != nil {
			return nil, err2
		}
		defer f.Close()
		if format == "json" {
			n, err = c.ExportJSON(ctx, schema, sql, f)
		} else {
			n, err = c.ExportCSV(ctx, schema, sql, f)
		}
	}
	if err != nil {
		return nil, err
	}
	return &FileResult{Path: path, Rows: n}, nil
}

type FileResult struct {
	Path string `json:"path"`
	Rows int64  `json:"rows"`
	Info string `json:"info"`
}

// ExportCSV asks for a file and streams the query result (with schema as default database)
// into it. An empty path means the user cancelled.
func (a *App) ExportCSV(id, schema, sql, suggestedName string) (*FileResult, error) {
	c, err := a.conn(id)
	if err != nil {
		return nil, err
	}
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "Export to CSV",
		DefaultFilename: suggestedName,
		Filters:         []runtime.FileFilter{{DisplayName: "CSV files (*.csv)", Pattern: "*.csv"}},
	})
	if err != nil || path == "" {
		return &FileResult{}, err
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	ctx, cancel := context.WithTimeout(a.ctx, 2*time.Hour)
	defer cancel()
	n, err := c.ExportCSV(ctx, schema, sql, f)
	if err != nil {
		return nil, err
	}
	return &FileResult{Path: path, Rows: n}, nil
}

// ---- dumps (mysqldump / mariadb-dump / pg_dump), DataGrip-style dialog ----

type DumpSetup struct {
	Driver   string       `json:"driver"`
	Options  dump.Options `json:"options"`
	Home     string       `json:"home"`
	Database string       `json:"database"`
}

// DumpDefaults returns pre-filled options for the dialog.
func (a *App) DumpDefaults(id, database, table string) (*DumpSetup, error) {
	cfg, ok := a.store.Get(id)
	if !ok {
		return nil, errors.New("connection not found")
	}
	pg := cfg.Driver == "postgres" || cfg.Driver == "postgresql"
	o := dump.Defaults(pg)
	o.Database = database
	if table != "" {
		o.Tables = []string{table}
	}
	home, _ := os.UserHomeDir()
	o.OutPath = filepath.Join(home, "durusql-dumps", "{data_source}_{database}_{table}_{timestamp}.sql")
	d := "mysql"
	if pg {
		d = "postgres"
	}
	return &DumpSetup{Driver: d, Options: o, Home: home, Database: cfg.Database}, nil
}

// DumpPreview renders the command line for the dialog.
func (a *App) DumpPreview(id string, o dump.Options) (string, error) {
	cfg, ok := a.store.Get(id)
	if !ok {
		return "", errors.New("connection not found")
	}
	return dump.Preview(cfg, o), nil
}

// RunDump executes the dump with the dialog's options.
func (a *App) RunDump(id string, o dump.Options) (*FileResult, error) {
	cfg, ok := a.store.Get(id)
	if !ok {
		return nil, errors.New("connection not found")
	}
	ctx, cancel := context.WithTimeout(a.ctx, 12*time.Hour)
	defer cancel()
	path, info, err := dump.Run(ctx, cfg, o)
	h := config.HistoryEntry{At: time.Now(), SQL: "-- dump " + o.Database + " " + strings.Join(o.Tables, " ") + " -> " + path}
	if err != nil {
		h.Err = err.Error()
		a.store.AppendHistory(id, h)
		return nil, err
	}
	a.store.AppendHistory(id, h)
	st, _ := os.Stat(path)
	var size int64
	if st != nil {
		size = st.Size()
	}
	return &FileResult{Path: path, Rows: size, Info: info}, nil
}

// ---- restore (mysql / psql) ----

type RestoreSetup struct {
	Driver  string              `json:"driver"`
	Options dump.RestoreOptions `json:"options"`
	Home    string              `json:"home"`
}

func (a *App) RestoreDefaults(id, database string) (*RestoreSetup, error) {
	cfg, ok := a.store.Get(id)
	if !ok {
		return nil, errors.New("connection not found")
	}
	pg := cfg.Driver == "postgres" || cfg.Driver == "postgresql"
	o := dump.RestoreDefaults(pg)
	o.Database = database
	if o.Database == "" {
		o.Database = cfg.Database
	}
	home, _ := os.UserHomeDir()
	d := "mysql"
	if pg {
		d = "postgres"
	}
	return &RestoreSetup{Driver: d, Options: o, Home: home}, nil
}

func (a *App) RestorePreview(id string, o dump.RestoreOptions) (string, error) {
	cfg, ok := a.store.Get(id)
	if !ok {
		return "", errors.New("connection not found")
	}
	return dump.RestorePreview(cfg, o), nil
}

type RestoreResult struct {
	Ms       int64  `json:"ms"`
	Warnings string `json:"warnings"`
}

func (a *App) RunRestore(id string, o dump.RestoreOptions) (*RestoreResult, error) {
	cfg, ok := a.store.Get(id)
	if !ok {
		return nil, errors.New("connection not found")
	}
	ctx, cancel := context.WithTimeout(a.ctx, 12*time.Hour)
	defer cancel()
	createDB := func(name string) error {
		c, err := a.conn(id)
		if err != nil {
			return err
		}
		_, err = c.DB.ExecContext(ctx, "CREATE DATABASE IF NOT EXISTS "+c.Quote(name))
		return err
	}
	warnings, took, err := dump.RestoreRun(ctx, cfg, o, createDB)
	h := config.HistoryEntry{At: time.Now(), SQL: "-- restore " + o.InPath + " -> " + o.Database, Ms: took.Milliseconds()}
	if err != nil {
		h.Err = err.Error()
		a.store.AppendHistory(id, h)
		return nil, err
	}
	a.store.AppendHistory(id, h)
	a.Disconnect(id) // pooled connections may hold stale metadata / USE state after a big import
	return &RestoreResult{Ms: took.Milliseconds(), Warnings: warnings}, nil
}

// PickOpenFile opens a native file chooser for input files.
func (a *App) PickOpenFile(title string, patterns string) (string, error) {
	return runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{Title: title, Filters: []runtime.FileFilter{{DisplayName: "SQL dumps", Pattern: patterns}}})
}

// PickSavePath / PickFile open native dialogs for the "…" buttons.
func (a *App) PickSavePath(title, defaultPath string) (string, error) {
	return runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{Title: title, DefaultDirectory: filepath.Dir(defaultPath), DefaultFilename: filepath.Base(defaultPath)})
}
func (a *App) PickFile(title string) (string, error) {
	return runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{Title: title})
}

// ---- history ----

func (a *App) ListHistory(id string, limit int) ([]config.HistoryEntry, error) {
	return a.store.ListHistory(id, limit)
}
func (a *App) ClearHistory(id string) error { return a.store.ClearHistory(id) }

// ---- saved queries & favorites ----

func (a *App) ListQueries(id string) ([]config.SavedQuery, error) { return a.store.ListQueries(id) }
func (a *App) SaveQuery(id, name, sql string) error               { return a.store.SaveQuery(id, name, sql) }
func (a *App) DeleteQuery(id, name string) error                  { return a.store.DeleteQuery(id, name) }
func (a *App) GetFavorites(id string) ([]string, error)           { return a.store.Favorites(id) }
func (a *App) ToggleFavorite(id, table string) ([]string, error) {
	return a.store.ToggleFavorite(id, table)
}
