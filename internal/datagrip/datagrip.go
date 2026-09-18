// Package datagrip imports data sources from a local JetBrains DataGrip installation.
//
// DataGrip keeps:
//   - ~/DataGripProjects/<project>/.idea/dataSources.xml        name, uuid, jdbc url
//   - ~/DataGripProjects/<project>/.idea/dataSources.local.xml  user name, ssh config ref, schemas
//   - ~/.config/JetBrains/DataGrip<ver>/options/sshConfigs.xml  ssh hosts/keys
//   - passwords in the system keyring, as Secret Service items whose "service" attribute is
//     "IntelliJ Platform DB — <uuid>" / "IntelliJ Platform SshConfigPassword — host:port <id>" /
//     "IntelliJ Platform SshConfigPassphrase — host:port <id>".
package datagrip

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"durusql/internal/config"
)

// SecretLookup resolves a keyring "service" label to its secret. ok=false when absent.
type SecretLookup func(service string) (value string, ok bool, err error)

type Result struct {
	Connections []config.Connection `json:"connections"`
	Warnings    []string            `json:"warnings"`
}

// ---- XML shapes ----

type dataSourcesXML struct {
	Sources []struct {
		Name string `xml:"name,attr"`
		UUID string `xml:"uuid,attr"`
		URL  string `xml:"jdbc-url"`
		User string `xml:"user-name"`
	} `xml:"component>data-source"`
}

type localXML struct {
	Sources []struct {
		UUID string `xml:"uuid,attr"`
		User string `xml:"user-name"`
		SSH  struct {
			Enabled  bool   `xml:"enabled"`
			ConfigID string `xml:"ssh-config-id"`
		} `xml:"ssh-properties"`
		Schemas []struct {
			Kind  string `xml:"kind,attr"`
			Names []struct {
				Q string `xml:"qname,attr"`
			} `xml:"name"`
		} `xml:"schema-mapping>introspection-scope>node"`
	} `xml:"component>data-source"`
}

type sshXML struct {
	Configs []sshConfig `xml:"component>configs>sshConfig"`
}

type sshConfig struct {
	ID       string `xml:"id,attr"`
	Host     string `xml:"host,attr"`
	Port     int    `xml:"port,attr"`
	User     string `xml:"username,attr"`
	KeyPath  string `xml:"keyPath,attr"`
	AuthType string `xml:"authType,attr"` // "" (key) | PASSWORD | OPEN_SSH
}

// ---- locating files ----

// ProjectFiles returns every dataSources.xml under ~/DataGripProjects (or $DATAGRIP_PROJECTS).
func ProjectFiles() ([]string, error) {
	root := os.Getenv("DATAGRIP_PROJECTS")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		root = filepath.Join(home, "DataGripProjects")
	}
	files, _ := filepath.Glob(filepath.Join(root, "*", ".idea", "dataSources.xml"))
	sort.Strings(files)
	return files, nil
}

// SSHConfigFiles returns every sshConfigs.xml of every JetBrains IDE (DataGrip, PhpStorm,
// IntelliJ, GoLand, …), oldest first so newer versions win when merged.
func SSHConfigFiles() []string {
	base, err := os.UserConfigDir()
	if err != nil {
		return nil
	}
	files, _ := filepath.Glob(filepath.Join(base, "JetBrains", "*", "options", "sshConfigs.xml"))
	sort.Slice(files, func(i, j int) bool {
		si, _ := os.Stat(files[i])
		sj, _ := os.Stat(files[j])
		if si == nil || sj == nil {
			return files[i] < files[j]
		}
		return si.ModTime().Before(sj.ModTime())
	})
	return files
}

func sshConfigs(extra ...string) (map[string]sshConfig, error) {
	out := map[string]sshConfig{}
	for _, f := range append(SSHConfigFiles(), extra...) {
		var sx sshXML
		if err := readXML(f, &sx); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		for _, c := range sx.Configs {
			out[c.ID] = c
		}
	}
	return out, nil
}

// FindProjectFiles walks root (skipping node_modules, vendor, .git) for .idea/dataSources.xml
// files, i.e. data sources of PhpStorm / IntelliJ / GoLand projects.
func FindProjectFiles(root string, maxDepth int) []string {
	var out []string
	root = filepath.Clean(root)
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == "node_modules" || name == "vendor" || name == ".git" || name == ".cache" || (strings.HasPrefix(name, ".") && name != ".idea" && p != root) {
				return filepath.SkipDir
			}
			if strings.Count(strings.TrimPrefix(p, root), string(filepath.Separator)) > maxDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == "dataSources.xml" && filepath.Base(filepath.Dir(p)) == ".idea" {
			out = append(out, p)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// ---- import ----

// Import reads every DataGrip project and converts its data sources. lookup may be nil,
// in which case passwords are left empty.
func Import(lookup SecretLookup) (*Result, error) {
	files, err := ProjectFiles()
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no DataGrip data sources found (looked for ~/DataGripProjects/*/.idea/dataSources.xml)")
	}
	return ImportFiles(files, lookup)
}

// ImportFiles converts the data sources of the given dataSources.xml files (DataGrip, PhpStorm,
// IntelliJ, … all use the same format). Project-level sources are grouped by project name.
func ImportFiles(files []string, lookup SecretLookup) (*Result, error) {
	return importFiles(files, nil, lookup)
}

// ImportArchive imports from a settings export (File → Manage IDE Settings → Export Settings,
// a .zip) or any zip that contains dataSources*.xml / sshConfigs.xml files.
func ImportArchive(zipPath string, lookup SecretLookup) (*Result, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	tmp, err := os.MkdirTemp("", "durusql-import-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	var files, ssh []string
	for _, f := range zr.File {
		base := filepath.Base(f.Name)
		if base != "dataSources.xml" && base != "dataSources.local.xml" && base != "sshConfigs.xml" {
			continue
		}
		dst := filepath.Join(tmp, filepath.FromSlash(f.Name))
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return nil, err
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		w, err := os.Create(dst)
		if err != nil {
			rc.Close()
			return nil, err
		}
		_, err = io.Copy(w, rc)
		w.Close()
		rc.Close()
		if err != nil {
			return nil, err
		}
		switch base {
		case "dataSources.xml":
			files = append(files, dst)
		case "sshConfigs.xml":
			ssh = append(ssh, dst)
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("%s contains no dataSources.xml", filepath.Base(zipPath))
	}
	return importFiles(files, ssh, lookup)
}

func importFiles(files, extraSSH []string, lookup SecretLookup) (*Result, error) {
	sshByID, err := sshConfigs(extraSSH...)
	if err != nil {
		return nil, err
	}
	res := &Result{Connections: []config.Connection{}, Warnings: []string{}}
	seen := map[string]bool{}
	for _, f := range files {
		conns, warns, err := importProject(f, sshByID, lookup)
		if err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s: %v", f, err))
			continue
		}
		group := projectGroup(f)
		for _, c := range conns {
			if seen[c.ID] {
				continue
			}
			seen[c.ID] = true
			if c.Group == "" && group != "" {
				c.Group = group
			}
			res.Connections = append(res.Connections, c)
		}
		res.Warnings = append(res.Warnings, warns...)
	}
	return res, nil
}

// projectGroup names the group for project-level data sources ("" for DataGrip's own projects).
func projectGroup(file string) string {
	if filepath.Base(filepath.Dir(file)) != ".idea" {
		return "" // e.g. options/dataSources.xml inside a settings export
	}
	dir := filepath.Dir(filepath.Dir(file)) // …/<project>/.idea/dataSources.xml
	if strings.Contains(filepath.ToSlash(dir), "/DataGripProjects/") || strings.HasPrefix(dir, os.TempDir()) {
		return ""
	}
	return filepath.Base(dir)
}

// ImportXML converts data sources pasted from an IDE ("Copy Settings" on a data source puts
// this XML on the clipboard; a whole dataSources.xml works too).
func ImportXML(text string, lookup SecretLookup) (*Result, error) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "<") {
		return nil, fmt.Errorf("not XML: expected <data-source …> elements")
	}
	if !strings.Contains(text, "<project") && !strings.Contains(text, "<component") && !strings.Contains(text, "<application") {
		text = "<project><component>" + text + "</component></project>"
	}
	var ds dataSourcesXML
	if err := xml.Unmarshal([]byte(text), &ds); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	var loc localXML
	_ = xml.Unmarshal([]byte(text), &loc)
	sshByID, err := sshConfigs()
	if err != nil {
		return nil, err
	}
	conns, warns, err := convert(ds, loc, sshByID, lookup)
	if err != nil {
		return nil, err
	}
	if len(conns) == 0 {
		return nil, fmt.Errorf("no <data-source> elements found")
	}
	return &Result{Connections: conns, Warnings: warns}, nil
}

func importProject(file string, sshByID map[string]sshConfig, lookup SecretLookup) ([]config.Connection, []string, error) {
	var ds dataSourcesXML
	if err := readXML(file, &ds); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", file, err)
	}
	var loc localXML
	localFile := strings.TrimSuffix(file, ".xml") + ".local.xml"
	if err := readXML(localFile, &loc); err != nil && !os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("%s: %w", localFile, err)
	}
	return convert(ds, loc, sshByID, lookup)
}

func convert(ds dataSourcesXML, loc localXML, sshByID map[string]sshConfig, lookup SecretLookup) ([]config.Connection, []string, error) {
	localByUUID := map[string]int{}
	for i, l := range loc.Sources {
		localByUUID[l.UUID] = i
	}

	var out []config.Connection
	var warns []string
	warn := func(format string, a ...any) { warns = append(warns, fmt.Sprintf(format, a...)) }

	for _, src := range ds.Sources {
		driver, host, port, database, err := parseJDBC(src.URL)
		if err != nil {
			warn("%s: skipped (%v)", src.Name, err)
			continue
		}
		c := config.Connection{
			ID:       "dg-" + src.UUID,
			Name:     src.Name,
			Driver:   driver,
			Host:     host,
			Port:     port,
			Database: database,
		}
		if i := strings.LastIndex(src.Name, "/"); i > 0 {
			c.Group, c.Name = src.Name[:i], src.Name[i+1:]
		}

		if src.User != "" {
			c.User = src.User
		}
		if li, ok := localByUUID[src.UUID]; ok {
			l := loc.Sources[li]
			if l.User != "" {
				c.User = l.User
			}
			if c.Database == "" {
				c.Database = pickSchema(l.Schemas, driver)
			}
			if l.SSH.Enabled && l.SSH.ConfigID != "" {
				sc, ok := sshByID[l.SSH.ConfigID]
				if !ok {
					warn("%s: ssh config %s not found in DataGrip, imported without tunnel", src.Name, l.SSH.ConfigID)
				} else {
					c.SSH = toSSH(sc, lookup, src.Name, warn)
				}
			}
		}

		if lookup != nil {
			pw, ok, err := lookup("IntelliJ Platform DB — " + src.UUID)
			switch {
			case err != nil:
				warn("%s: password not read from keyring: %v", src.Name, err)
			case !ok:
				warn("%s: no saved password in DataGrip", src.Name)
			default:
				u, p := decodeSecret(pw)
				c.Password = p
				if c.User == "" {
					c.User = u
				}
			}
		}
		out = append(out, c)
	}
	return out, warns, nil
}

func toSSH(sc sshConfig, lookup SecretLookup, name string, warn func(string, ...any)) *config.SSHConfig {
	s := &config.SSHConfig{Host: sc.Host, Port: sc.Port, User: sc.User}
	if s.Port == 0 {
		s.Port = 22
	}
	if home, err := os.UserHomeDir(); err == nil {
		s.KeyPath = strings.ReplaceAll(sc.KeyPath, "$USER_HOME$", home)
	} else {
		s.KeyPath = sc.KeyPath
	}
	label := fmt.Sprintf("%s:%d %s", sc.Host, sc.Port, sc.ID)
	switch {
	case sc.AuthType == "PASSWORD":
		s.KeyPath = ""
		if lookup != nil {
			if pw, ok, err := lookup("IntelliJ Platform SshConfigPassword — " + label); err != nil {
				warn("%s: ssh password not read from keyring: %v", name, err)
			} else if !ok {
				warn("%s: no saved ssh password in DataGrip", name)
			} else {
				_, s.Password = decodeSecret(pw)
			}
		}
	case s.KeyPath != "":
		if lookup != nil {
			if pp, ok, _ := lookup("IntelliJ Platform SshConfigPassphrase — " + label); ok {
				_, s.Password = decodeSecret(pp) // durusql uses SSH.Password as the key passphrase when KeyPath is set
			}
		}
	default:
		s.UseAgent = true // OpenSSH-config style: rely on the agent
	}
	return s
}

// pickSchema chooses a sensible default database from DataGrip's introspection scope.
func pickSchema(nodes []struct {
	Kind  string `xml:"kind,attr"`
	Names []struct {
		Q string `xml:"qname,attr"`
	} `xml:"name"`
}, driver string) string {
	skip := map[string]bool{"@": true, "mysql": true, "information_schema": true, "performance_schema": true, "sys": true, "test": true, "postgres": true, "public": true}
	for _, n := range nodes {
		if n.Kind != "schema" && n.Kind != "database" {
			continue
		}
		for _, nm := range n.Names {
			if !skip[nm.Q] && nm.Q != "" {
				return nm.Q
			}
		}
	}
	return ""
}

// parseJDBC understands jdbc:mysql://, jdbc:mariadb:// and jdbc:postgresql:// URLs.
func parseJDBC(raw string) (driver, host string, port int, database string, err error) {
	if !strings.HasPrefix(raw, "jdbc:") {
		return "", "", 0, "", fmt.Errorf("not a jdbc url: %q", raw)
	}
	u, err := url.Parse(strings.TrimPrefix(raw, "jdbc:"))
	if err != nil {
		return "", "", 0, "", err
	}
	switch u.Scheme {
	case "mysql", "mariadb":
		driver, port = "mysql", 3306
	case "postgresql":
		driver, port = "postgres", 5432
	default:
		return "", "", 0, "", fmt.Errorf("unsupported driver %q", u.Scheme)
	}
	host = u.Hostname()
	if host == "" {
		host = "localhost"
	}
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err == nil {
			port = n
		}
	}
	database = strings.Trim(u.Path, "/")
	return driver, host, port, database, nil
}

// decodeSecret unpacks the IntelliJ credential-store value format "<user>@<password>", where
// '\\' and '@' inside the user part are backslash-escaped and the password is raw. DataGrip
// stores DB/SSH secrets with an empty user, so values typically look like "@hunter2".
func decodeSecret(raw string) (user, password string) {
	var u strings.Builder
	for i := 0; i < len(raw); i++ {
		switch raw[i] {
		case '\\':
			if i+1 < len(raw) {
				i++
				u.WriteByte(raw[i])
			}
		case '@':
			return u.String(), raw[i+1:]
		default:
			u.WriteByte(raw[i])
		}
	}
	return u.String(), ""
}

func readXML(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return xml.Unmarshal(b, v)
}
