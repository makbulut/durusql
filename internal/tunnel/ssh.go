package tunnel

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"

	"durusql/internal/config"
)

// Dial opens an SSH client using (in order): ssh-agent, key file, password.
func Dial(cfg *config.SSHConfig) (*ssh.Client, error) {
	if cfg == nil {
		return nil, errors.New("no ssh config")
	}
	var auth []ssh.AuthMethod

	if cfg.UseAgent {
		if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
			if conn, err := net.Dial("unix", sock); err == nil {
				auth = append(auth, ssh.PublicKeysCallback(agent.NewClient(conn).Signers))
			}
		}
	}
	if cfg.KeyPath != "" {
		key, err := os.ReadFile(expand(cfg.KeyPath))
		if err != nil {
			return nil, fmt.Errorf("read key: %w", err)
		}
		var signer ssh.Signer
		if cfg.Password != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase(key, []byte(cfg.Password))
		} else {
			signer, err = ssh.ParsePrivateKey(key)
		}
		if err != nil {
			return nil, fmt.Errorf("parse key: %w", err)
		}
		auth = append(auth, ssh.PublicKeys(signer))
	}
	if cfg.Password != "" {
		auth = append(auth, ssh.Password(cfg.Password))
	}
	if len(auth) == 0 {
		return nil, errors.New("no ssh auth method (agent/key/password)")
	}

	hostKey := ssh.InsecureIgnoreHostKey() //nolint:gosec — fallback only
	if kh := expand("~/.ssh/known_hosts"); fileExists(kh) {
		if cb, err := knownhosts.New(kh); err == nil {
			hostKey = cb
		}
	}

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	conf := &ssh.ClientConfig{
		User:            cfg.User,
		Auth:            auth,
		HostKeyCallback: hostKey,
		Timeout:         10 * time.Second,
	}
	client, err := ssh.Dial("tcp", addr, conf)
	if err != nil {
		// known_hosts has this host, but under a different key type than the server offered
		// first. OpenSSH would negotiate the recorded type; do the same and retry once.
		var ke *knownhosts.KeyError
		if errors.As(err, &ke) && len(ke.Want) > 0 {
			conf.HostKeyAlgorithms = nil
			for _, w := range ke.Want {
				t := w.Key.Type()
				if t == ssh.KeyAlgoRSA {
					// modern servers only sign RSA host keys with SHA-2
					conf.HostKeyAlgorithms = append(conf.HostKeyAlgorithms, ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256)
				}
				conf.HostKeyAlgorithms = append(conf.HostKeyAlgorithms, t)
			}
			client, err = ssh.Dial("tcp", addr, conf)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("ssh dial: %w", err)
	}
	return client, nil
}

func expand(p string) string {
	if len(p) > 1 && p[:2] == "~/" {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
