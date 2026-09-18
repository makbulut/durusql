// Package dump runs mysqldump / mariadb-dump / pg_dump for a connection, through the SSH
// tunnel when the connection has one (a local port is forwarded to the database host).
package dump

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"durusql/internal/config"
	"durusql/internal/tunnel"
)

// Options mirrors DataGrip's "Export with mysqldump" dialog.
type Options struct {
	Executable string   `json:"executable"`
	OutPath    string   `json:"outPath"` // may contain {timestamp} {data_source} {database} {table}
	Database   string   `json:"database"`
	Tables     []string `json:"tables"`
	Extra      string   `json:"extra"` // extra raw arguments

	// MySQL / MariaDB
	AddDropTable      bool `json:"addDropTable"`
	DisableKeys       bool `json:"disableKeys"`
	LockTables        bool `json:"lockTables"`
	AddDropTrigger    bool `json:"addDropTrigger"`
	SchemaOnly        bool `json:"schemaOnly"`
	NoTablespaces     bool `json:"noTablespaces"`
	DataOnly          bool `json:"dataOnly"`
	CompleteInsert    bool `json:"completeInsert"`
	CreateOptions     bool `json:"createOptions"`
	Routines          bool `json:"routines"`
	Events            bool `json:"events"`
	Triggers          bool `json:"triggers"`
	LockAllTables     bool `json:"lockAllTables"`
	ExtendedInsert    bool `json:"extendedInsert"`
	SingleTransaction bool `json:"singleTransaction"`
	Quick             bool `json:"quick"`
	AddDropDatabase   bool `json:"addDropDatabase"`

	// PostgreSQL
	Clean         bool `json:"clean"`
	IfExists      bool `json:"ifExists"`
	Inserts       bool `json:"inserts"`
	ColumnInserts bool `json:"columnInserts"`
	NoOwner       bool `json:"noOwner"`
}

// Defaults returns the options DataGrip pre-selects.
func Defaults(pg bool) Options {
	o := Options{AddDropTable: true, DisableKeys: true, NoTablespaces: true, CreateOptions: true, Triggers: true,
		ExtendedInsert: true, SingleTransaction: true, Quick: true, NoOwner: true}
	o.Executable = FindExecutable(pg)
	return o
}

func FindExecutable(pg bool) string {
	names := []string{"mariadb-dump", "mysqldump"}
	if pg {
		names = []string{"pg_dump"}
	}
	for _, n := range names {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	return names[len(names)-1]
}

func isPG(cfg config.Connection) bool { return cfg.Driver == "postgres" || cfg.Driver == "postgresql" }

// ExpandPath fills the substitution patterns of the output path.
func ExpandPath(p string, cfg config.Connection, o Options) string {
	table := strings.Join(o.Tables, "_")
	r := strings.NewReplacer("{timestamp}", time.Now().Format("20060102_150405"), "{data_source}", cfg.Name, "{database}", o.Database, "{table}", table)
	p = r.Replace(p)
	for strings.Contains(p, "__") {
		p = strings.ReplaceAll(p, "__", "_")
	}
	p = strings.ReplaceAll(p, "_.", ".")
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[2:])
		}
	}
	return p
}

// Args builds the command line (without the password, which goes through the environment).
func Args(cfg config.Connection, o Options, host string, port int, outPath string) []string {
	var a []string
	if isPG(cfg) {
		a = []string{"-h", host, "-p", fmt.Sprint(port), "-U", cfg.User, "-d", cfg.Database, "--format=plain", "--file=" + outPath}
		if o.Database != "" {
			a = append(a, "-n", o.Database)
		}
		for _, t := range o.Tables {
			a = append(a, "-t", o.Database+"."+t)
		}
		if o.SchemaOnly {
			a = append(a, "--schema-only")
		}
		if o.DataOnly {
			a = append(a, "--data-only")
		}
		if o.Clean {
			a = append(a, "--clean")
		}
		if o.IfExists {
			a = append(a, "--if-exists")
		}
		if o.Inserts {
			a = append(a, "--inserts")
		}
		if o.ColumnInserts {
			a = append(a, "--column-inserts")
		}
		if o.NoOwner {
			a = append(a, "--no-owner", "--no-privileges")
		}
	} else {
		a = []string{"--host=" + host, "--port=" + fmt.Sprint(port), "--user=" + cfg.User, "--result-file=" + outPath, "--default-character-set=utf8mb4"}
		flag := func(on bool, yes, no string) {
			if on {
				a = append(a, yes)
			} else if no != "" {
				a = append(a, no)
			}
		}
		flag(o.AddDropTable, "--add-drop-table", "--skip-add-drop-table")
		flag(o.DisableKeys, "--disable-keys", "--skip-disable-keys")
		flag(o.LockTables, "--lock-tables", "--skip-lock-tables")
		flag(o.AddDropTrigger, "--add-drop-trigger", "")
		flag(o.SchemaOnly, "--no-data", "")
		flag(o.NoTablespaces, "--no-tablespaces", "")
		flag(o.DataOnly, "--no-create-info", "")
		flag(o.CompleteInsert, "--complete-insert", "")
		flag(o.CreateOptions, "--create-options", "--skip-create-options")
		flag(o.Routines, "--routines", "")
		flag(o.Events, "--events", "")
		flag(o.Triggers, "--triggers", "--skip-triggers")
		flag(o.LockAllTables, "--lock-all-tables", "")
		flag(o.ExtendedInsert, "--extended-insert", "--skip-extended-insert")
		flag(o.SingleTransaction, "--single-transaction", "")
		flag(o.Quick, "--quick", "")
		flag(o.AddDropDatabase, "--add-drop-database", "")
		if isMySQL8(o.Executable) {
			a = append(a, "--column-statistics=0")
		}
		if o.Extra != "" {
			a = append(a, strings.Fields(o.Extra)...)
		}
		a = append(a, o.Database)
		a = append(a, o.Tables...)
		return a
	}
	if o.Extra != "" {
		a = append(a, strings.Fields(o.Extra)...)
	}
	return a
}

// Preview renders the command line as it will run (password omitted).
func Preview(cfg config.Connection, o Options) string {
	host, port := cfg.Host, cfg.Port
	if cfg.SSH != nil && cfg.SSH.Host != "" {
		host, port = "127.0.0.1", 0 // forwarded port is chosen at run time
	}
	args := Args(cfg, o, host, port, ExpandPath(o.OutPath, cfg, o))
	parts := []string{o.Executable}
	for _, x := range args {
		if strings.ContainsAny(x, " \t\"'") {
			x = `"` + strings.ReplaceAll(x, `"`, `\"`) + `"`
		}
		parts = append(parts, x)
	}
	s := strings.Join(parts, " ")
	if port == 0 {
		s = strings.Replace(s, "--port=0", "--port=<ssh-forwarded>", 1)
		s = strings.Replace(s, "-p 0", "-p <ssh-forwarded>", 1)
	}
	return s
}

// Run executes the dump and returns the output path and the tool's version line.
func Run(ctx context.Context, cfg config.Connection, o Options) (string, string, error) {
	host, port := cfg.Host, cfg.Port
	if cfg.SSH != nil && cfg.SSH.Host != "" {
		client, err := tunnel.Dial(cfg.SSH)
		if err != nil {
			return "", "", err
		}
		defer client.Close()
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return "", "", err
		}
		defer ln.Close()
		go forward(ln, client, fmt.Sprintf("%s:%d", cfg.Host, cfg.Port))
		host, port = "127.0.0.1", ln.Addr().(*net.TCPAddr).Port
	}
	outPath := ExpandPath(o.OutPath, cfg, o)
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return "", "", err
	}
	exe := o.Executable
	if exe == "" {
		exe = FindExecutable(isPG(cfg))
	}
	if _, err := exec.LookPath(exe); err != nil {
		return "", "", fmt.Errorf("%s not found: install mariadb-client / mysql-client / postgresql-client", exe)
	}
	cmd := exec.CommandContext(ctx, exe, Args(cfg, o, host, port, outPath)...)
	if isPG(cfg) {
		cmd.Env = append(os.Environ(), "PGPASSWORD="+cfg.Password)
	} else {
		cmd.Env = append(os.Environ(), "MYSQL_PWD="+cfg.Password)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return outPath, "", fmt.Errorf("%s failed: %s", filepath.Base(exe), lastLine(msg))
	}
	ver, _ := exec.Command(exe, "--version").Output()
	return outPath, strings.TrimSpace(strings.Split(string(ver), "\n")[0]), nil
}

func forward(ln net.Listener, client interface {
	Dial(network, addr string) (net.Conn, error)
}, target string) {
	for {
		local, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer local.Close()
			remote, err := client.Dial("tcp", target)
			if err != nil {
				return
			}
			defer remote.Close()
			done := make(chan struct{}, 2)
			go func() { io.Copy(remote, local); done <- struct{}{} }()
			go func() { io.Copy(local, remote); done <- struct{}{} }()
			<-done
		}()
	}
}

// isMySQL8 reports whether the dump tool is Oracle's mysqldump 8+, which needs
// --column-statistics=0 when talking to MariaDB / older servers.
func isMySQL8(bin string) bool {
	out, _ := exec.Command(bin, "--version").Output()
	s := string(out)
	return strings.Contains(s, "Ver 8") && !strings.Contains(strings.ToLower(s), "mariadb")
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

// RunToString runs the dump tool and returns its stdout (used for DDL of PostgreSQL tables:
// pg_dump --schema-only -t schema.table).
func RunToString(ctx context.Context, cfg config.Connection, o Options) (string, error) {
	host, port := cfg.Host, cfg.Port
	if cfg.SSH != nil && cfg.SSH.Host != "" {
		client, err := tunnel.Dial(cfg.SSH)
		if err != nil {
			return "", err
		}
		defer client.Close()
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return "", err
		}
		defer ln.Close()
		go forward(ln, client, fmt.Sprintf("%s:%d", cfg.Host, cfg.Port))
		host, port = "127.0.0.1", ln.Addr().(*net.TCPAddr).Port
	}
	exe := o.Executable
	if exe == "" {
		exe = FindExecutable(isPG(cfg))
	}
	if _, err := exec.LookPath(exe); err != nil {
		return "", fmt.Errorf("%s not found: install postgresql-client / mariadb-client", exe)
	}
	args := Args(cfg, o, host, port, "")
	// drop the file options so output goes to stdout
	var clean []string
	for _, a := range args {
		if strings.HasPrefix(a, "--file=") || strings.HasPrefix(a, "--result-file=") {
			continue
		}
		clean = append(clean, a)
	}
	cmd := exec.CommandContext(ctx, exe, clean...)
	if isPG(cfg) {
		cmd.Env = append(os.Environ(), "PGPASSWORD="+cfg.Password)
	} else {
		cmd.Env = append(os.Environ(), "MYSQL_PWD="+cfg.Password)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s failed: %s", filepath.Base(exe), lastLine(msg))
	}
	// strip pg_dump's SET/comment preamble noise but keep the statements
	var lines []string
	for _, l := range strings.Split(stdout.String(), "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "--") || t == "" || strings.HasPrefix(t, "SET ") || strings.HasPrefix(t, "SELECT pg_catalog.set_config") {
			continue
		}
		lines = append(lines, l)
	}
	return strings.Join(lines, "\n"), nil
}
