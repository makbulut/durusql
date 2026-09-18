package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// AppName / AppDir are the single place the product name lives on the Go side.
const (
	AppName = "DuruSQL"
	AppDir  = "durusql"
)

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

type SSHConfig struct {
	Host     string `yaml:"host" json:"host"`
	Port     int    `yaml:"port" json:"port"`
	User     string `yaml:"user" json:"user"`
	KeyPath  string `yaml:"keyPath,omitempty" json:"keyPath"`
	Password string `yaml:"password,omitempty" json:"password"`
	UseAgent bool   `yaml:"useAgent" json:"useAgent"`
}

type Connection struct {
	ID       string     `yaml:"id" json:"id"`
	Name     string     `yaml:"name" json:"name"`
	Group    string     `yaml:"group,omitempty" json:"group"`
	Driver   string     `yaml:"driver" json:"driver"` // mysql | postgres | opensearch | elasticsearch
	Host     string     `yaml:"host" json:"host"`
	Port     int        `yaml:"port" json:"port"`
	User     string     `yaml:"user" json:"user"`
	Password string     `yaml:"password,omitempty" json:"password"`
	Database string     `yaml:"database" json:"database"`
	TLS      bool       `yaml:"tls,omitempty" json:"tls"`           // OpenSearch/Elasticsearch: https
	Insecure bool       `yaml:"insecure,omitempty" json:"insecure"` // OpenSearch/Elasticsearch: skip certificate verification
	Favorite bool       `yaml:"favorite" json:"favorite"`
	Color    string     `yaml:"color,omitempty" json:"color"`
	SSH      *SSHConfig `yaml:"ssh,omitempty" json:"ssh"`
}

type file struct {
	Connections []Connection `yaml:"connections"`
}

type SavedQuery struct {
	Name string `json:"name"`
	SQL  string `json:"sql"`
}

type Store struct {
	mu   sync.Mutex
	root string
	data file
}

func Open() (*Store, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	s := &Store{root: filepath.Join(base, AppDir)}
	// migrate the pre-rename config directory (earlier names: dbtool, rowdy)
	if _, err := os.Stat(s.root); os.IsNotExist(err) {
		if old := filepath.Join(base, "rowdy"); dirExists(old) {
			_ = os.Rename(old, s.root)
		}
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(s.path())
	if err == nil {
		if err := yaml.Unmarshal(b, &s.data); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *Store) path() string             { return filepath.Join(s.root, "connections.yaml") }
func (s *Store) connDir(id string) string { return filepath.Join(s.root, id) }

func (s *Store) flush() error {
	b, err := yaml.Marshal(&s.data)
	if err != nil {
		return err
	}
	return os.WriteFile(s.path(), b, 0o600)
}

func (s *Store) Connections() []Connection {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Connection, len(s.data.Connections))
	copy(out, s.data.Connections)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Favorite != out[j].Favorite {
			return out[i].Favorite
		}
		if out[i].Group != out[j].Group {
			return out[i].Group < out[j].Group
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

func (s *Store) Get(id string) (Connection, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.data.Connections {
		if c.ID == id {
			return c, true
		}
	}
	return Connection{}, false
}

func (s *Store) Save(c Connection) (Connection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.ID == "" {
		c.ID = time.Now().UTC().Format("20060102-150405.000")
		c.ID = strings.ReplaceAll(c.ID, ".", "-")
	}
	if c.Port == 0 {
		if c.Driver == "postgres" {
			c.Port = 5432
		} else if c.Driver == "opensearch" || c.Driver == "elasticsearch" {
			c.Port = 9200
		} else {
			c.Port = 3306
		}
	}
	if c.SSH != nil && c.SSH.Port == 0 {
		c.SSH.Port = 22
	}
	found := false
	for i := range s.data.Connections {
		if s.data.Connections[i].ID == c.ID {
			s.data.Connections[i] = c
			found = true
		}
	}
	if !found {
		s.data.Connections = append(s.data.Connections, c)
	}
	if err := os.MkdirAll(filepath.Join(s.connDir(c.ID), "queries"), 0o700); err != nil {
		return c, err
	}
	return c, s.flush()
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.data.Connections[:0]
	for _, c := range s.data.Connections {
		if c.ID != id {
			kept = append(kept, c)
		}
	}
	s.data.Connections = kept
	_ = os.RemoveAll(s.connDir(id))
	return s.flush()
}

// ---- saved queries (plain .sql files, git-friendly) ----

func safeName(name string) (string, error) {
	name = strings.TrimSpace(strings.TrimSuffix(name, ".sql"))
	if name == "" || strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, ".") {
		return "", errors.New("invalid query name")
	}
	return name + ".sql", nil
}

func (s *Store) ListQueries(id string) ([]SavedQuery, error) {
	dir := filepath.Join(s.connDir(id), "queries")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []SavedQuery{}, nil
		}
		return nil, err
	}
	out := []SavedQuery{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		out = append(out, SavedQuery{Name: strings.TrimSuffix(e.Name(), ".sql"), SQL: string(b)})
	}
	return out, nil
}

func (s *Store) SaveQuery(id, name, sql string) error {
	fn, err := safeName(name)
	if err != nil {
		return err
	}
	dir := filepath.Join(s.connDir(id), "queries")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, fn), []byte(sql), 0o600)
}

func (s *Store) DeleteQuery(id, name string) error {
	fn, err := safeName(name)
	if err != nil {
		return err
	}
	return os.Remove(filepath.Join(s.connDir(id), "queries", fn))
}

// ---- favorite tables ----

func (s *Store) favPath(id string) string { return filepath.Join(s.connDir(id), "favorites.json") }

func (s *Store) Favorites(id string) ([]string, error) {
	b, err := os.ReadFile(s.favPath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	var out []string
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) ToggleFavorite(id, table string) ([]string, error) {
	favs, err := s.Favorites(id)
	if err != nil {
		return nil, err
	}
	idx := -1
	for i, t := range favs {
		if t == table {
			idx = i
		}
	}
	if idx >= 0 {
		favs = append(favs[:idx], favs[idx+1:]...)
	} else {
		favs = append(favs, table)
		sort.Strings(favs)
	}
	if err := os.MkdirAll(s.connDir(id), 0o700); err != nil {
		return nil, err
	}
	b, _ := json.MarshalIndent(favs, "", "  ")
	return favs, os.WriteFile(s.favPath(id), b, 0o600)
}

// ---- history (append-only jsonl) ----

type HistoryEntry struct {
	At   time.Time `json:"at"`
	SQL  string    `json:"sql"`
	Ms   int64     `json:"ms"`
	Rows int       `json:"rows"`
	Err  string    `json:"err,omitempty"`
}

func (s *Store) AppendHistory(id string, e HistoryEntry) {
	f, err := os.OpenFile(filepath.Join(s.connDir(id), "history.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	b, _ := json.Marshal(e)
	f.Write(append(b, '\n'))
}

// ListHistory returns the newest entries first, at most limit (0 = all).
func (s *Store) ListHistory(id string, limit int) ([]HistoryEntry, error) {
	b, err := os.ReadFile(filepath.Join(s.connDir(id), "history.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return []HistoryEntry{}, nil
		}
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	out := make([]HistoryEntry, 0, len(lines))
	for i := len(lines) - 1; i >= 0; i-- {
		var e HistoryEntry
		if json.Unmarshal([]byte(lines[i]), &e) == nil && e.SQL != "" {
			out = append(out, e)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (s *Store) ClearHistory(id string) error {
	err := os.Remove(filepath.Join(s.connDir(id), "history.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// ---- app settings (settings.yaml) ----

type Settings struct {
	UpdateURL     string `yaml:"updateURL" json:"updateURL"`         // base URL that hosts <channel>.json and the packages
	UpdateChannel string `yaml:"updateChannel" json:"updateChannel"` // stable | beta
	AutoCheck     bool   `yaml:"autoCheck" json:"autoCheck"`
	SkipVersion   string `yaml:"skipVersion,omitempty" json:"skipVersion"`
	LastCheck     string `yaml:"lastCheck,omitempty" json:"lastCheck"`
}

func (s *Store) settingsPath() string { return filepath.Join(s.root, "settings.yaml") }

func (s *Store) Settings() Settings {
	st := Settings{UpdateChannel: "stable", AutoCheck: true}
	if b, err := os.ReadFile(s.settingsPath()); err == nil {
		_ = yaml.Unmarshal(b, &st)
	}
	if st.UpdateChannel == "" {
		st.UpdateChannel = "stable"
	}
	return st
}

func (s *Store) SaveSettings(st Settings) error {
	b, err := yaml.Marshal(&st)
	if err != nil {
		return err
	}
	return os.WriteFile(s.settingsPath(), b, 0o600)
}
