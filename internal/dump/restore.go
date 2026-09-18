package dump

import (
	"bufio"
	"bytes"
	"compress/gzip"
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

// RestoreOptions mirrors DataGrip's "Restore with mysql / psql" dialog.
type RestoreOptions struct {
	Executable string `json:"executable"`
	InPath     string `json:"inPath"` // .sql or .sql.gz
	Database   string `json:"database"`
	Extra      string `json:"extra"`
	// MySQL
	Force           bool `json:"force"`           // keep going after SQL errors
	DisableFKChecks bool `json:"disableFKChecks"` // SET FOREIGN_KEY_CHECKS=0 for the session
	CreateDatabase  bool `json:"createDatabase"`  // CREATE DATABASE IF NOT EXISTS first
	// PostgreSQL
	OnErrorStop       bool `json:"onErrorStop"`
	SingleTransaction bool `json:"singleTransaction"`
}

func RestoreDefaults(pg bool) RestoreOptions {
	o := RestoreOptions{DisableFKChecks: true, OnErrorStop: true}
	names := []string{"mariadb", "mysql"}
	if pg {
		names = []string{"psql"}
	}
	o.Executable = names[len(names)-1]
	for _, n := range names {
		if p, err := exec.LookPath(n); err == nil {
			o.Executable = p
			break
		}
	}
	return o
}

// RestoreArgs builds the client's command line (password goes through the environment).
func RestoreArgs(cfg config.Connection, o RestoreOptions, host string, port int) []string {
	var a []string
	if isPG(cfg) {
		dbName := o.Database
		if dbName == "" {
			dbName = cfg.Database
		}
		a = []string{"-h", host, "-p", fmt.Sprint(port), "-U", cfg.User, "-d", dbName, "-q"}
		if o.OnErrorStop {
			a = append(a, "-v", "ON_ERROR_STOP=1")
		}
		if o.SingleTransaction {
			a = append(a, "--single-transaction")
		}
		if !strings.HasSuffix(o.InPath, ".gz") {
			a = append(a, "-f", o.InPath)
		}
	} else {
		a = []string{"--host=" + host, "--port=" + fmt.Sprint(port), "--user=" + cfg.User, "--default-character-set=utf8mb4"}
		if o.Force {
			a = append(a, "--force")
		}
		if o.DisableFKChecks {
			a = append(a, "--init-command=SET FOREIGN_KEY_CHECKS=0, UNIQUE_CHECKS=0")
		}
		if o.Database != "" {
			a = append(a, "--database="+o.Database)
		}
	}
	if o.Extra != "" {
		a = append(a, strings.Fields(o.Extra)...)
	}
	return a
}

// RestorePreview renders the command as it will run.
func RestorePreview(cfg config.Connection, o RestoreOptions) string {
	host, port := cfg.Host, cfg.Port
	if cfg.SSH != nil && cfg.SSH.Host != "" {
		host, port = "127.0.0.1", 0
	}
	parts := []string{o.Executable}
	for _, x := range RestoreArgs(cfg, o, host, port) {
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
	if isPG(cfg) {
		if strings.HasSuffix(o.InPath, ".gz") {
			s = "gzip -dc " + q(o.InPath) + " | " + s
		}
	} else {
		if strings.HasSuffix(o.InPath, ".gz") {
			s = "gzip -dc " + q(o.InPath) + " | " + s
		} else {
			s += " < " + q(o.InPath)
		}
	}
	if o.CreateDatabase && !isPG(cfg) && o.Database != "" {
		s = "CREATE DATABASE IF NOT EXISTS `" + o.Database + "`;  then  " + s
	}
	return s
}

func q(s string) string {
	if strings.ContainsAny(s, " \t\"'") {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return s
}

// RestoreRun executes the import. createDB is called first when requested (MySQL).
func RestoreRun(ctx context.Context, cfg config.Connection, o RestoreOptions, createDB func(name string) error) (string, time.Duration, error) {
	start := time.Now()
	if _, err := os.Stat(o.InPath); err != nil {
		return "", 0, fmt.Errorf("input file: %w", err)
	}
	if o.CreateDatabase && !isPG(cfg) && o.Database != "" && createDB != nil {
		if err := createDB(o.Database); err != nil {
			return "", 0, fmt.Errorf("create database: %w", err)
		}
	}
	host, port := cfg.Host, cfg.Port
	if cfg.SSH != nil && cfg.SSH.Host != "" {
		client, err := tunnel.Dial(cfg.SSH)
		if err != nil {
			return "", 0, err
		}
		defer client.Close()
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return "", 0, err
		}
		defer ln.Close()
		go forward(ln, client, fmt.Sprintf("%s:%d", cfg.Host, cfg.Port))
		host, port = "127.0.0.1", ln.Addr().(*net.TCPAddr).Port
	}
	exe := o.Executable
	if _, err := exec.LookPath(exe); err != nil {
		return "", 0, fmt.Errorf("%s not found: install mariadb-client / mysql-client / postgresql-client", exe)
	}
	cmd := exec.CommandContext(ctx, exe, RestoreArgs(cfg, o, host, port)...)
	if isPG(cfg) {
		cmd.Env = append(os.Environ(), "PGPASSWORD="+cfg.Password)
	} else {
		cmd.Env = append(os.Environ(), "MYSQL_PWD="+cfg.Password)
	}
	// feed the file on stdin (decompressing .gz), except psql with a plain file which takes -f
	if !isPG(cfg) || strings.HasSuffix(o.InPath, ".gz") {
		f, err := os.Open(o.InPath)
		if err != nil {
			return "", 0, err
		}
		defer f.Close()
		var in io.Reader = f
		if strings.HasSuffix(o.InPath, ".gz") {
			gz, err := gzip.NewReader(f)
			if err != nil {
				return "", 0, fmt.Errorf("gzip: %w", err)
			}
			defer gz.Close()
			in = gz
		}
		if !isPG(cfg) {
			in = compatFilter(in)
		}
		cmd.Stdin = in
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	err := cmd.Run()
	msg := strings.TrimSpace(stderr.String())
	if err != nil {
		if msg == "" {
			msg = err.Error()
		}
		return msg, time.Since(start), fmt.Errorf("%s failed: %s", filepath.Base(exe), lastLine(msg))
	}
	return msg, time.Since(start), nil
}

// compatFilter drops executable comments that newer MariaDB dump clients emit and older servers
// reject (e.g. "/*M!100616 SET ... NOTE_VERBOSITY ... */" fails with "Unknown system variable").
func compatFilter(r io.Reader) io.Reader {
	pr, pw := io.Pipe()
	go func() {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 1024*1024), 256*1024*1024) // dumps have very long INSERT lines
		for sc.Scan() {
			line := sc.Bytes()
			if bytes.HasPrefix(line, []byte("/*M!")) && bytes.Contains(line, []byte("NOTE_VERBOSITY")) {
				continue
			}
			if _, err := pw.Write(append(line, '\n')); err != nil {
				return
			}
		}
		pw.CloseWithError(sc.Err())
	}()
	return pr
}
