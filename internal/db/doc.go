package db

// Document-store backend: OpenSearch and Elasticsearch over their REST API. Indices are
// presented as tables under one pseudo database (the cluster name), mappings as columns, and
// aliases as views. The console accepts SQL (sent to the engine's SQL endpoint) and raw REST
// requests in Dev Tools style ("GET /index/_search" followed by a JSON body).

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"durusql/internal/config"
)

type docStore struct {
	base    string // scheme://host:port, no trailing slash
	http    *http.Client
	user    string
	pass    string
	cluster string // pseudo database shown in the explorer
	flavor  string // opensearch | elasticsearch
	version string
}

// IsDoc reports whether this connection is a document store (OpenSearch / Elasticsearch).
func (c *Conn) IsDoc() bool { return c.doc != nil }

func isDocDriver(d string) bool { return d == "opensearch" || d == "elasticsearch" }

func openDoc(c config.Connection, dialer func(ctx context.Context, network, addr string) (net.Conn, error)) (*docStore, error) {
	scheme := "http"
	if c.TLS {
		scheme = "https"
	}
	port := c.Port
	if port == 0 {
		port = 9200
	}
	tr := &http.Transport{
		MaxIdleConnsPerHost:   poolSize,
		IdleConnTimeout:       5 * time.Minute,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 0,
	}
	if c.Insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	if dialer != nil {
		tr.DialContext = dialer
	} else {
		tr.DialContext = (&net.Dialer{Timeout: 10 * time.Second}).DialContext
	}
	d := &docStore{
		base:    fmt.Sprintf("%s://%s:%d", scheme, c.Host, port),
		http:    &http.Client{Transport: tr},
		user:    c.User,
		pass:    c.Password,
		cluster: "cluster",
		flavor:  "opensearch",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var info struct {
		ClusterName string `json:"cluster_name"`
		Version     struct {
			Number       string `json:"number"`
			Distribution string `json:"distribution"`
		} `json:"version"`
		Tagline string `json:"tagline"`
	}
	if _, err := d.call(ctx, "GET", "/", nil, &info); err != nil {
		return nil, err
	}
	if info.ClusterName != "" {
		d.cluster = info.ClusterName
	}
	d.version = info.Version.Number
	if info.Version.Distribution == "" && !strings.Contains(strings.ToLower(info.Tagline), "open") {
		d.flavor = "elasticsearch"
	} else if info.Version.Distribution != "" && info.Version.Distribution != "opensearch" {
		d.flavor = "elasticsearch"
	}
	if c.Driver == "elasticsearch" && info.Version.Distribution != "opensearch" {
		d.flavor = "elasticsearch"
	}
	return d, nil
}

func (d *docStore) close() {
	if t, ok := d.http.Transport.(*http.Transport); ok {
		t.CloseIdleConnections()
	}
}

// docError is what the engine returned for a failed request.
type docError struct {
	Status int
	Msg    string
}

func (e *docError) Error() string { return e.Msg }

// call performs one request; a JSON body is encoded from body (any) or sent raw ([]byte /
// string). When out is non-nil the response is decoded into it; the raw bytes are returned too.
func (d *docStore) call(ctx context.Context, method, path string, body any, out any) ([]byte, error) {
	var rd io.Reader
	ct := ""
	switch b := body.(type) {
	case nil:
	case []byte:
		rd, ct = bytes.NewReader(b), "application/json"
	case string:
		rd, ct = strings.NewReader(b), "application/json"
	default:
		buf, err := json.Marshal(b)
		if err != nil {
			return nil, err
		}
		rd, ct = bytes.NewReader(buf), "application/json"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	req, err := http.NewRequestWithContext(ctx, method, d.base+path, rd)
	if err != nil {
		return nil, err
	}
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	req.Header.Set("Accept", "application/json")
	if d.user != "" {
		req.SetBasicAuth(d.user, d.pass)
	}
	res, err := d.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode >= 400 {
		return raw, &docError{Status: res.StatusCode, Msg: docErrorMessage(raw, res.StatusCode)}
	}
	if out != nil && len(raw) > 0 {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(out); err != nil {
			return raw, err
		}
	}
	return raw, nil
}

func docErrorMessage(raw []byte, status int) string {
	var e struct {
		Error any `json:"error"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Error != nil {
		switch v := e.Error.(type) {
		case string:
			return v
		case map[string]any:
			parts := []string{}
			for _, k := range []string{"type", "reason", "details"} {
				if s, ok := v[k].(string); ok && s != "" {
					parts = append(parts, s)
				}
			}
			if len(parts) > 0 {
				msg := strings.Join(parts, ": ")
				if i := strings.Index(msg, "\nFor more details"); i > 0 {
					msg = msg[:i]
				}
				return msg
			}
		}
	}
	msg := strings.TrimSpace(string(raw))
	if msg == "" {
		msg = http.StatusText(status)
	}
	return fmt.Sprintf("HTTP %d: %s", status, msg)
}

// ---- console: SQL and REST ----

var restLine = regexp.MustCompile(`^\s*(GET|POST|PUT|DELETE|HEAD|PATCH)\s+(\S+)\s*$`)

type docRequest struct {
	Text   string // original text (shown in history / result tabs)
	Method string
	Path   string
	Body   string
	SQL    string // set for SQL statements
}

// splitDocScript separates a console script into SQL statements and REST requests. A line
// starting with an HTTP method opens a REST request whose JSON body runs until its braces
// balance; everything else is SQL split on semicolons.
func splitDocScript(script string) []docRequest {
	var out []docRequest
	var sqlBuf []string
	flushSQL := func() {
		if len(sqlBuf) == 0 {
			return
		}
		for _, s := range SplitStatements(strings.Join(sqlBuf, "\n")) {
			out = append(out, docRequest{Text: s, SQL: s})
		}
		sqlBuf = nil
	}
	lines := strings.Split(script, "\n")
	for i := 0; i < len(lines); i++ {
		m := restLine.FindStringSubmatch(lines[i])
		if m == nil {
			sqlBuf = append(sqlBuf, lines[i])
			continue
		}
		flushSQL()
		r := docRequest{Method: m[1], Path: m[2]}
		text := []string{strings.TrimSpace(lines[i])}
		// body: following lines until braces/brackets balance (or the next request line)
		depth, started, inStr, esc := 0, false, false, false
		var body []string
		j := i + 1
		for ; j < len(lines); j++ {
			l := lines[j]
			if !started {
				t := strings.TrimSpace(l)
				if t == "" {
					continue
				}
				if !strings.HasPrefix(t, "{") && !strings.HasPrefix(t, "[") {
					break
				}
				started = true
			}
			body = append(body, l)
			for _, ch := range l {
				if inStr {
					if esc {
						esc = false
					} else if ch == '\\' {
						esc = true
					} else if ch == '"' {
						inStr = false
					}
					continue
				}
				switch ch {
				case '"':
					inStr = true
				case '{', '[':
					depth++
				case '}', ']':
					depth--
				}
			}
			if depth <= 0 {
				j++
				break
			}
		}
		if started {
			r.Body = strings.TrimSpace(strings.Join(body, "\n"))
			text = append(text, r.Body)
		}
		r.Text = strings.Join(text, "\n")
		out = append(out, r)
		i = j - 1
	}
	flushSQL()
	return out
}

func (d *docStore) runScript(ctx context.Context, script string, limit int) ([]*Result, error) {
	reqs := splitDocScript(script)
	var out []*Result
	for _, r := range reqs {
		res, err := d.runRequest(ctx, r, limit)
		if err != nil {
			return out, &StatementError{Index: len(out), SQL: r.Text, Err: err}
		}
		out = append(out, res)
	}
	return out, nil
}

// run executes a single console entry (SQL or one REST request).
func (d *docStore) run(ctx context.Context, text string, limit int) (*Result, error) {
	reqs := splitDocScript(text)
	if len(reqs) == 0 {
		return &Result{Columns: []string{}, Rows: [][]any{}}, nil
	}
	return d.runRequest(ctx, reqs[0], limit)
}

func (d *docStore) runRequest(ctx context.Context, r docRequest, limit int) (*Result, error) {
	start := time.Now()
	var res *Result
	var err error
	if r.SQL != "" {
		res, err = d.runSQL(ctx, r.SQL, limit)
	} else {
		res, err = d.runREST(ctx, r, limit)
	}
	if err != nil {
		return nil, err
	}
	res.SQL = r.Text
	res.Ms = time.Since(start).Milliseconds()
	return res, nil
}

func (d *docStore) sqlPath() string {
	if d.flavor == "elasticsearch" {
		return "/_sql?format=json"
	}
	return "/_plugins/_sql?format=jdbc"
}

func (d *docStore) runSQL(ctx context.Context, q string, limit int) (*Result, error) {
	q = strings.TrimSuffix(strings.TrimSpace(q), ";")
	res := &Result{Columns: []string{}, Rows: [][]any{}}
	if d.flavor == "elasticsearch" {
		// Elasticsearch SQL wants double-quoted identifiers and has no OFFSET: rewrite the
		// backticks the UI generates and emulate OFFSET by skipping rows, paging with the cursor.
		q, skip := esSQL(q)
		want := 0
		if limit > 0 {
			want = skip + limit + 1
		}
		body := map[string]any{"query": q}
		if want > 0 && want < 1000 {
			body["fetch_size"] = want
		}
		var r struct {
			Columns []struct {
				Name string `json:"name"`
			} `json:"columns"`
			Rows   [][]any `json:"rows"`
			Cursor string  `json:"cursor"`
		}
		if _, err := d.call(ctx, "POST", d.sqlPath(), body, &r); err != nil {
			return nil, err
		}
		for _, c := range r.Columns {
			res.Columns = append(res.Columns, c.Name)
		}
		rows := r.Rows
		cursor := r.Cursor
		for cursor != "" && (want == 0 || len(rows) < want) {
			var more struct {
				Rows   [][]any `json:"rows"`
				Cursor string  `json:"cursor"`
			}
			if _, err := d.call(ctx, "POST", d.sqlPath(), map[string]any{"cursor": cursor}, &more); err != nil {
				return nil, err
			}
			rows = append(rows, more.Rows...)
			if len(more.Rows) == 0 {
				break
			}
			cursor = more.Cursor
		}
		if cursor != "" {
			_, _ = d.call(ctx, "POST", "/_sql/close", map[string]any{"cursor": cursor}, nil)
		}
		if skip >= len(rows) {
			rows = nil
		} else {
			rows = rows[skip:]
		}
		res.Rows = rows
	} else {
		var r struct {
			Schema []struct {
				Name  string `json:"name"`
				Alias string `json:"alias"`
			} `json:"schema"`
			Datarows [][]any `json:"datarows"`
			Total    int64   `json:"total"`
		}
		if _, err := d.call(ctx, "POST", d.sqlPath(), map[string]any{"query": q}, &r); err != nil {
			return nil, err
		}
		for _, c := range r.Schema {
			name := c.Alias
			if name == "" {
				name = c.Name
			}
			res.Columns = append(res.Columns, name)
		}
		res.Rows = r.Datarows
	}
	for i, row := range res.Rows {
		for j, v := range row {
			row[j] = docValue(v)
		}
		res.Rows[i] = row
	}
	if limit > 0 && len(res.Rows) > limit {
		res.Rows = res.Rows[:limit]
		res.Truncated = true
	}
	return res, nil
}

func (d *docStore) runREST(ctx context.Context, r docRequest, limit int) (*Result, error) {
	path := r.Path
	// _cat endpoints answer in plain text unless asked for JSON
	if strings.HasPrefix(strings.TrimPrefix(path, "/"), "_cat") && !strings.Contains(path, "format=") {
		if strings.Contains(path, "?") {
			path += "&format=json"
		} else {
			path += "?format=json"
		}
	}
	var body any
	if r.Body != "" {
		body = r.Body
	}
	raw, err := d.call(ctx, r.Method, path, body, nil)
	if err != nil {
		return nil, err
	}
	res := &Result{Columns: []string{}, Rows: [][]any{}}
	if r.Method == "HEAD" || len(bytes.TrimSpace(raw)) == 0 {
		res.Columns = []string{"status"}
		res.Rows = [][]any{{"OK"}}
		return res, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		res.Columns = []string{"response"}
		res.Rows = [][]any{{string(raw)}}
		return res, nil
	}
	tabulate(res, v, limit)
	return res, nil
}

// tabulate turns a JSON response into grid rows: search hits become one row per document,
// arrays of objects one row per element, and any other object a key/value listing.
func tabulate(res *Result, v any, limit int) {
	if obj, ok := v.(map[string]any); ok {
		if hits, ok := obj["hits"].(map[string]any); ok {
			if list, ok := hits["hits"].([]any); ok {
				rows := make([]map[string]any, 0, len(list))
				for _, h := range list {
					hm, _ := h.(map[string]any)
					row := map[string]any{}
					for _, k := range []string{"_index", "_id", "_score"} {
						if x, ok := hm[k]; ok {
							row[k] = x
						}
					}
					if src, ok := hm["_source"].(map[string]any); ok {
						flatten("", src, row)
					}
					if f, ok := hm["fields"].(map[string]any); ok && len(row) <= 3 {
						flatten("", f, row)
					}
					rows = append(rows, row)
				}
				objectsToRows(res, rows, []string{"_index", "_id", "_score"}, limit)
				if t, ok := hits["total"].(map[string]any); ok {
					if n, ok := t["value"].(json.Number); ok {
						res.RowsAffected, _ = n.Int64()
					}
				}
				return
			}
		}
		// counts of bulk-style operations
		for _, k := range []string{"deleted", "updated", "count"} {
			if n, ok := obj[k].(json.Number); ok && len(obj) <= 12 {
				res.RowsAffected, _ = n.Int64()
			}
		}
		flat := map[string]any{}
		flatten("", obj, flat)
		keys := make([]string, 0, len(flat))
		for k := range flat {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		res.Columns = []string{"key", "value"}
		for _, k := range keys {
			res.Rows = append(res.Rows, []any{k, docValue(flat[k])})
		}
		return
	}
	if list, ok := v.([]any); ok {
		rows := make([]map[string]any, 0, len(list))
		allObjects := true
		for _, it := range list {
			m, ok := it.(map[string]any)
			if !ok {
				allObjects = false
				break
			}
			row := map[string]any{}
			flatten("", m, row)
			rows = append(rows, row)
		}
		if allObjects {
			objectsToRows(res, rows, nil, limit)
			return
		}
		res.Columns = []string{"value"}
		for _, it := range list {
			res.Rows = append(res.Rows, []any{docValue(it)})
		}
		return
	}
	res.Columns = []string{"value"}
	res.Rows = [][]any{{docValue(v)}}
}

// objectsToRows builds columns from the union of keys (first-seen order, preferred first).
func objectsToRows(res *Result, rows []map[string]any, preferred []string, limit int) {
	seen := map[string]bool{}
	var cols []string
	for _, p := range preferred {
		for _, r := range rows {
			if _, ok := r[p]; ok {
				cols = append(cols, p)
				seen[p] = true
				break
			}
		}
	}
	for _, r := range rows {
		keys := make([]string, 0, len(r))
		for k := range r {
			if !seen[k] {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			seen[k] = true
			cols = append(cols, k)
		}
	}
	res.Columns = cols
	for i, r := range rows {
		if limit > 0 && i >= limit {
			res.Truncated = true
			break
		}
		row := make([]any, len(cols))
		for j, c := range cols {
			row[j] = docValue(r[c])
		}
		res.Rows = append(res.Rows, row)
	}
}

// flatten writes nested objects as dotted keys; arrays and scalars are kept as values.
func flatten(prefix string, m map[string]any, out map[string]any) {
	for k, v := range m {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		if sub, ok := v.(map[string]any); ok && len(sub) > 0 {
			flatten(key, sub, out)
			continue
		}
		out[key] = v
	}
}

// docValue converts decoded JSON into grid-friendly scalars: numbers keep integer precision,
// arrays and objects become compact JSON text.
func docValue(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return i
		}
		if f, err := t.Float64(); err == nil {
			return f
		}
		return t.String()
	case float64:
		if t == float64(int64(t)) && t < 1e15 && t > -1e15 {
			return int64(t)
		}
		return t
	case string, bool, int64, int:
		return t
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprint(t)
		}
		return string(b)
	}
}

// ---- metadata ----

// indexName strips the pseudo database ("cluster.index" → "index").
func (d *docStore) indexName(table string) string {
	if strings.HasPrefix(table, d.cluster+".") {
		return table[len(d.cluster)+1:]
	}
	return table
}

func (d *docStore) quote(ident string) string {
	return "`" + strings.ReplaceAll(d.indexName(ident), "`", "``") + "`"
}

type catIndex struct {
	Index     string `json:"index"`
	Health    string `json:"health"`
	Status    string `json:"status"`
	DocsCount string `json:"docs.count"`
	StoreSize string `json:"store.size"`
	Pri       string `json:"pri"`
	Rep       string `json:"rep"`
}

func (d *docStore) indices(ctx context.Context) ([]catIndex, error) {
	var list []catIndex
	if _, err := d.call(ctx, "GET", "/_cat/indices?format=json&h=index,health,status,docs.count,store.size,pri,rep&s=index", nil, &list); err != nil {
		return nil, err
	}
	out := list[:0]
	for _, ix := range list {
		if strings.HasPrefix(ix.Index, ".") { // hidden / system indices
			continue
		}
		out = append(out, ix)
	}
	return out, nil
}

func (d *docStore) aliases(ctx context.Context) (map[string][]string, error) {
	var list []struct {
		Alias string `json:"alias"`
		Index string `json:"index"`
	}
	if _, err := d.call(ctx, "GET", "/_cat/aliases?format=json&h=alias,index&s=alias", nil, &list); err != nil {
		return nil, err
	}
	out := map[string][]string{}
	for _, a := range list {
		if strings.HasPrefix(a.Alias, ".") {
			continue
		}
		out[a.Alias] = append(out[a.Alias], a.Index)
	}
	return out, nil
}

func (d *docStore) schemaObjects(ctx context.Context) (*SchemaObjects, error) {
	out := &SchemaObjects{Tables: []string{}, Views: []string{}, Routines: []Routine{}, Events: []string{}, Sequences: []Sequence{}, Types: []TypeInfo{}}
	ixs, err := d.indices(ctx)
	if err != nil {
		return nil, err
	}
	for _, ix := range ixs {
		out.Tables = append(out.Tables, ix.Index)
	}
	if al, err := d.aliases(ctx); err == nil {
		for a := range al {
			out.Views = append(out.Views, a)
		}
		sort.Strings(out.Views)
	}
	return out, nil
}

// fields flattens an index mapping into (path, type) pairs in mapping order.
func mappingFields(props map[string]any, prefix string, out *[]ColumnInfo) {
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		p, _ := props[k].(map[string]any)
		name := k
		if prefix != "" {
			name = prefix + "." + k
		}
		typ, _ := p["type"].(string)
		sub, hasSub := p["properties"].(map[string]any)
		if typ == "" && hasSub {
			typ = "object"
		}
		col := ColumnInfo{Name: name, Type: typ, Nullable: true}
		var extra []string
		if f, ok := p["fields"].(map[string]any); ok {
			names := make([]string, 0, len(f))
			for fn := range f {
				names = append(names, fn)
			}
			sort.Strings(names)
			extra = append(extra, "fields: "+strings.Join(names, ", "))
		}
		if fmtS, ok := p["format"].(string); ok {
			extra = append(extra, "format: "+fmtS)
		}
		if idx, ok := p["index"].(bool); ok && !idx {
			extra = append(extra, "not indexed")
		}
		if an, ok := p["analyzer"].(string); ok {
			extra = append(extra, "analyzer: "+an)
		}
		col.Comment = strings.Join(extra, " · ")
		*out = append(*out, col)
		if hasSub {
			mappingFields(sub, name, out)
		}
	}
}

func (d *docStore) mapping(ctx context.Context, index string) ([]ColumnInfo, error) {
	var m map[string]struct {
		Mappings struct {
			Properties map[string]any `json:"properties"`
		} `json:"mappings"`
	}
	if _, err := d.call(ctx, "GET", "/"+url.PathEscape(index)+"/_mapping", nil, &m); err != nil {
		return nil, err
	}
	var cols []ColumnInfo
	for _, im := range m { // one entry (an alias resolves to its indices; first wins)
		mappingFields(im.Mappings.Properties, "", &cols)
		break
	}
	return cols, nil
}

func (d *docStore) tableDetails(ctx context.Context, table string) (*TableDetails, error) {
	index := d.indexName(table)
	td := &TableDetails{Keys: []KeyInfo{}, ForeignKeys: []ForeignKeyInfo{}, Indexes: []IndexInfo{}, Triggers: []TriggerInfo{}, Partitions: []PartitionInfo{}}
	start := time.Now()
	cols, err := d.mapping(ctx, index)
	if err != nil {
		return nil, err
	}
	td.Columns = append([]ColumnInfo{{Name: "_id", Type: "keyword", Key: "PRI", Comment: "document id"}}, cols...)
	td.Keys = []KeyInfo{{Name: "_id", Type: "PRIMARY KEY", Columns: []string{"_id"}}}
	var list []catIndex
	if _, err := d.call(ctx, "GET", "/_cat/indices/"+url.PathEscape(index)+"?format=json&h=index,health,status,docs.count,store.size,pri,rep", nil, &list); err == nil && len(list) > 0 {
		ix := list[0]
		td.Info.Engine = "index"
		td.Info.Comment = fmt.Sprintf("%s docs · %s · %s shards × %s replicas · health %s · %s", ix.DocsCount, ix.StoreSize, ix.Pri, ix.Rep, ix.Health, ix.Status)
		if n, err := strconv.ParseInt(ix.DocsCount, 10, 64); err == nil {
			td.Info.AutoIncrement = n
		}
	}
	td.Ms = time.Since(start).Milliseconds()
	return td, nil
}

func (d *docStore) columns(ctx context.Context, table string) ([]Column, error) {
	cols, err := d.mapping(ctx, d.indexName(table))
	if err != nil {
		return nil, err
	}
	out := []Column{{Name: "_id", Type: "keyword", Key: "PRI"}}
	for _, c := range cols {
		out = append(out, Column{Name: c.Name, Type: c.Type, Nullable: true})
	}
	return out, nil
}

// schemaColumns returns every field of every index (one _mapping call).
func (d *docStore) schemaColumns(ctx context.Context) (map[string][]ColumnRef, error) {
	var m map[string]struct {
		Mappings struct {
			Properties map[string]any `json:"properties"`
		} `json:"mappings"`
	}
	if _, err := d.call(ctx, "GET", "/_mapping", nil, &m); err != nil {
		return nil, err
	}
	out := map[string][]ColumnRef{}
	for index, im := range m {
		if strings.HasPrefix(index, ".") {
			continue
		}
		var cols []ColumnInfo
		mappingFields(im.Mappings.Properties, "", &cols)
		refs := []ColumnRef{{Name: "_id", Type: "keyword"}}
		for _, c := range cols {
			refs = append(refs, ColumnRef{Name: c.Name, Type: c.Type})
		}
		out[index] = refs
	}
	return out, nil
}

func (d *docStore) search(ctx context.Context, term string, limit int) ([]SearchHit, error) {
	term = strings.ToLower(term)
	var hits []SearchHit
	ixs, err := d.indices(ctx)
	if err != nil {
		return nil, err
	}
	for _, ix := range ixs {
		if strings.Contains(strings.ToLower(ix.Index), term) {
			hits = append(hits, SearchHit{Kind: "table", Schema: d.cluster, Name: ix.Index, Detail: ix.DocsCount + " docs"})
		}
	}
	if al, err := d.aliases(ctx); err == nil {
		for a, targets := range al {
			if strings.Contains(strings.ToLower(a), term) {
				hits = append(hits, SearchHit{Kind: "view", Schema: d.cluster, Name: a, Detail: "alias of " + strings.Join(targets, ", ")})
			}
		}
	}
	if cols, err := d.schemaColumns(ctx); err == nil {
		n := 0
		for index, refs := range cols {
			for _, r := range refs {
				if strings.Contains(strings.ToLower(r.Name), term) {
					hits = append(hits, SearchHit{Kind: "column", Schema: d.cluster, Name: r.Name, Table: index, Detail: r.Type})
					if n++; n >= limit {
						break
					}
				}
			}
		}
	}
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

// ddl returns the index definition (settings + mappings) as pretty JSON.
func (d *docStore) ddl(ctx context.Context, table string) (string, error) {
	index := d.indexName(table)
	raw, err := d.call(ctx, "GET", "/"+url.PathEscape(index)+"?flat_settings=false", nil, nil)
	if err != nil {
		return "", err
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return string(raw), nil
	}
	if m, ok := v.(map[string]any); ok {
		if im, ok := m[index].(map[string]any); ok {
			// drop server-assigned settings so the output can be replayed as PUT /index
			if s, ok := im["settings"].(map[string]any); ok {
				if ix, ok := s["index"].(map[string]any); ok {
					for _, k := range []string{"creation_date", "uuid", "provided_name", "version"} {
						delete(ix, k)
					}
				}
			}
			v = im
		}
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	return "PUT /" + index + "\n" + string(b), nil
}

func (d *docStore) aliasDDL(ctx context.Context, alias string) (string, error) {
	name := d.indexName(alias)
	raw, err := d.call(ctx, "GET", "/_alias/"+url.PathEscape(name), nil, nil)
	if err != nil {
		return "", err
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	_ = dec.Decode(&v)
	b, _ := json.MarshalIndent(v, "", "  ")
	return "GET /_alias/" + name + "\n" + string(b), nil
}

func (d *docStore) truncate(ctx context.Context, table string) error {
	index := d.indexName(table)
	_, err := d.call(ctx, "POST", "/"+url.PathEscape(index)+"/_delete_by_query?refresh=true&conflicts=proceed", map[string]any{"query": map[string]any{"match_all": map[string]any{}}}, nil)
	return err
}

var errDocUnsupported = fmt.Errorf("not supported for OpenSearch / Elasticsearch connections")

// exportCSVDoc writes a console entry's result as CSV (the SQL LIMIT / search size applies).
func (c *Conn) exportCSVDoc(ctx context.Context, q string, w io.Writer) (int64, error) {
	cw := csv.NewWriter(w)
	n, err := c.stream(ctx, "", q, func(cols []string) error { return cw.Write(cols) }, func(vals []any) error {
		rec := make([]string, len(vals))
		for i, v := range vals {
			switch t := v.(type) {
			case nil:
				rec[i] = ""
			case string:
				rec[i] = t
			default:
				rec[i] = fmt.Sprint(t)
			}
		}
		return cw.Write(rec)
	})
	cw.Flush()
	if err == nil {
		err = cw.Error()
	}
	return n, err
}

var esLimitOffset = regexp.MustCompile(`(?is)\s+LIMIT\s+(\d+)\s+OFFSET\s+(\d+)\s*$`)

// esSQL adapts a statement written for the UI's MySQL-style dialect to Elasticsearch SQL:
// backtick identifiers become double-quoted, and a trailing LIMIT n OFFSET m becomes
// LIMIT n+m with m returned as the number of leading rows to drop.
func esSQL(q string) (string, int) {
	skip := 0
	if m := esLimitOffset.FindStringSubmatchIndex(q); m != nil {
		n, _ := strconv.Atoi(q[m[2]:m[3]])
		off, _ := strconv.Atoi(q[m[4]:m[5]])
		skip = off
		q = q[:m[0]] + " LIMIT " + strconv.Itoa(n+off)
	}
	var b strings.Builder
	inStr := false
	for i := 0; i < len(q); i++ {
		ch := q[i]
		switch {
		case ch == '\'':
			inStr = !inStr
			b.WriteByte(ch)
		case ch == '`' && !inStr:
			b.WriteByte('"')
		default:
			b.WriteByte(ch)
		}
	}
	return b.String(), skip
}
