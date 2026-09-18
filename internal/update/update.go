// Package update implements the release-channel check and self-update.
//
// A channel is a JSON document at <UpdateURL>/<channel>.json:
//
//	{ "version": "0.6.0", "date": "2026-09-18", "notes": "…markdown…",
//	  "files": { "linux-deb": {"url": "…", "sha256": "…", "size": 123},
//	             "linux-tar": {...}, "windows-zip": {...}, "windows-installer": {...}, "macos-dmg": {...} } }
//
// package.sh writes it; publish.sh uploads dist/. Packages are verified by SHA-256 before use.
package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type File struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type Release struct {
	Version string          `json:"version"`
	Date    string          `json:"date"`
	Notes   string          `json:"notes"`
	Files   map[string]File `json:"files"`
}

// Status is what the UI shows.
type Status struct {
	Current     string   `json:"current"`
	Latest      string   `json:"latest"`
	Available   bool     `json:"available"`
	Notes       string   `json:"notes"`
	Date        string   `json:"date"`
	Kind        string   `json:"kind"`    // package kind this install would use
	CanSelf     bool     `json:"canSelf"` // the app can replace itself (tar / zip installs)
	InstallPath string   `json:"installPath"`
	Error       string   `json:"error,omitempty"`
	Channel     string   `json:"channel"`
	Files       []string `json:"files"`
}

// Check resolves the channel (GitHub Releases or a JSON base URL) and compares versions.
func Check(ctx context.Context, source, channel, current string) (*Status, error) {
	st := &Status{Current: current, Channel: channel, Kind: InstallKind(), InstallPath: exePath()}
	st.CanSelf = st.Kind == "linux-tar" || st.Kind == "windows-zip"
	if strings.TrimSpace(source) == "" {
		return st, fmt.Errorf("no update source configured")
	}
	rel, err := FetchRelease(ctx, source, channel)
	if err != nil {
		return st, err
	}
	st.Latest, st.Notes, st.Date = rel.Version, rel.Notes, rel.Date
	for k := range rel.Files {
		st.Files = append(st.Files, k)
	}
	st.Available = Newer(rel.Version, current) && current != "dev"
	return st, nil
}

// Newer reports whether a is a higher version than b (semver-ish: numbers, then pre-release tag).
func Newer(a, b string) bool {
	pa, ta := parse(a)
	pb, tb := parse(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	// same numbers: a release beats a pre-release; otherwise compare tags lexically
	if ta == "" && tb != "" {
		return true
	}
	if ta != "" && tb == "" {
		return false
	}
	return ta > tb
}

func parse(v string) ([3]int, string) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	tag := ""
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v, tag = v[:i], v[i+1:]
	}
	var out [3]int
	for i, p := range strings.SplitN(v, ".", 3) {
		n, _ := strconv.Atoi(strings.TrimFunc(p, func(r rune) bool { return r < '0' || r > '9' }))
		out[i] = n
	}
	return out, tag
}

func exePath() string {
	p, err := os.Executable()
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return p
}

// InstallKind guesses which package this installation came from.
func InstallKind() string {
	switch runtime.GOOS {
	case "windows":
		return "windows-zip"
	case "darwin":
		return "macos-dmg"
	}
	p := exePath()
	if strings.HasPrefix(p, "/usr/") || strings.HasPrefix(p, "/opt/") {
		return "linux-deb"
	}
	return "linux-tar"
}

// Download fetches f into the cache dir, verifying its checksum. progress gets bytes done/total.
func Download(ctx context.Context, f File, name string, progress func(done, total int64)) (string, error) {
	dir := filepath.Join(os.TempDir(), "durusql-update")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dest := filepath.Join(dir, name)
	req, err := http.NewRequestWithContext(ctx, "GET", f.URL, nil)
	if err != nil {
		return "", err
	}
	res, err := (&http.Client{Timeout: 30 * time.Minute}).Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", fmt.Errorf("%s: HTTP %d", f.URL, res.StatusCode)
	}
	out, err := os.Create(dest)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	var done int64
	buf := make([]byte, 256*1024)
	total := res.ContentLength
	if total <= 0 {
		total = f.Size
	}
	for {
		n, rerr := res.Body.Read(buf)
		if n > 0 {
			if _, err := out.Write(buf[:n]); err != nil {
				out.Close()
				return "", err
			}
			h.Write(buf[:n])
			done += int64(n)
			if progress != nil {
				progress(done, total)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			out.Close()
			return "", rerr
		}
	}
	out.Close()
	if f.SHA256 != "" && !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), f.SHA256) {
		os.Remove(dest)
		return "", fmt.Errorf("checksum mismatch for %s", name)
	}
	return dest, nil
}

// Install applies a downloaded package. Returns true when the app should restart itself.
func Install(kind, path string) (restart bool, err error) {
	switch kind {
	case "linux-tar":
		return true, replaceFromTar(path)
	case "windows-zip":
		return true, replaceFromZip(path)
	case "linux-deb":
		// needs root: polkit prompt via pkexec
		// --allow-downgrades: packages before 0.6.0-beta.2 used a hyphen version that apt sorts above
		// the tilde form used since, so the first update from them looks like a downgrade
		cmd := exec.Command("pkexec", "apt-get", "install", "-y", "--allow-downgrades", path)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return false, fmt.Errorf("apt-get install failed: %s", strings.TrimSpace(string(out)))
		}
		return true, nil
	case "windows-installer":
		return false, exec.Command(path).Start()
	case "macos-dmg":
		return false, exec.Command("open", path).Start()
	}
	return false, fmt.Errorf("unknown package kind %q", kind)
}

// replaceFromTar swaps the running binary with the one inside the tar.gz (same file name).
func replaceFromTar(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("no durusql binary in %s", filepath.Base(path))
		}
		if err != nil {
			return err
		}
		if h.Typeflag == tar.TypeReg && filepath.Base(h.Name) == "durusql" {
			return swapExecutable(tr, 0o755)
		}
	}
}

func replaceFromZip(path string) error {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		if strings.EqualFold(filepath.Base(f.Name), "durusql.exe") {
			rc, err := f.Open()
			if err != nil {
				return err
			}
			defer rc.Close()
			return swapExecutable(rc, 0o755)
		}
	}
	return fmt.Errorf("no durusql.exe in %s", filepath.Base(path))
}

// swapExecutable writes the new binary next to the current one and renames it into place.
// Windows cannot overwrite a running exe but can rename it, so the old one becomes .old.
func swapExecutable(r io.Reader, mode os.FileMode) error {
	exe := exePath()
	if exe == "" {
		return fmt.Errorf("cannot determine executable path")
	}
	tmp := exe + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("%s is not writable (%v); install the package manually", filepath.Dir(exe), err)
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		return err
	}
	out.Close()
	old := exe + ".old"
	os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		return err
	}
	if err := os.Rename(tmp, exe); err != nil {
		os.Rename(old, exe)
		return err
	}
	if runtime.GOOS != "windows" {
		os.Remove(old)
	}
	return nil
}

// Relaunch starts the (new) executable detached; the caller quits afterwards.
func Relaunch() error {
	exe := exePath()
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Stdout, cmd.Stderr = nil, nil
	return cmd.Start()
}

// ---- GitHub Releases as a channel ----
// Source forms: "https://github.com/owner/repo" or "github:owner/repo" → GitHub API;
// anything else is a base URL hosting <channel>.json.

type ghRelease struct {
	TagName    string `json:"tag_name"`
	Name       string `json:"name"`
	Body       string `json:"body"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Published  string `json:"published_at"`
	HTMLURL    string `json:"html_url"`
	Assets     []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
		Size int64  `json:"size"`
	} `json:"assets"`
}

func githubRepo(source string) (owner, repo string, ok bool) {
	s := strings.TrimSpace(source)
	s = strings.TrimPrefix(s, "github:")
	s = strings.TrimPrefix(s, "https://github.com/")
	s = strings.TrimPrefix(s, "http://github.com/")
	s = strings.TrimSuffix(strings.TrimSuffix(s, "/"), ".git")
	parts := strings.Split(s, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.Contains(s, ":") {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// IsGitHub reports whether the update source points at a GitHub repository.
func IsGitHub(source string) bool { _, _, ok := githubRepo(source); return ok }

// FetchRelease resolves the channel document from either provider.
func FetchRelease(ctx context.Context, source, channel string) (*Release, error) {
	owner, repo, ok := githubRepo(source)
	if !ok {
		url := strings.TrimRight(source, "/") + "/" + channel + ".json"
		var rel Release
		if err := getJSON(ctx, url, &rel); err != nil {
			return nil, err
		}
		return &rel, nil
	}
	var rels []ghRelease
	if channel == "stable" {
		var r ghRelease
		if err := getJSON(ctx, fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", owner, repo), &r); err != nil {
			return nil, err
		}
		rels = []ghRelease{r}
	} else {
		if err := getJSON(ctx, fmt.Sprintf("https://api.github.com/repos/%s/%s/releases?per_page=10", owner, repo), &rels); err != nil {
			return nil, err
		}
	}
	for _, r := range rels {
		if r.Draft {
			continue
		}
		rel := &Release{Version: strings.TrimPrefix(r.TagName, "v"), Date: strings.SplitN(r.Published, "T", 2)[0], Notes: r.Body, Files: map[string]File{}}
		if r.Name != "" && !strings.Contains(r.Body, r.Name) {
			rel.Notes = r.Name + "\n\n" + r.Body
		}
		var sums string
		for _, a := range r.Assets {
			if a.Name == "SHA256SUMS" {
				sums, _ = getText(ctx, a.URL)
			}
		}
		for _, a := range r.Assets {
			kind := assetKind(a.Name)
			if kind == "" {
				continue
			}
			rel.Files[kind] = File{URL: a.URL, Size: a.Size, SHA256: sumFor(sums, a.Name)}
		}
		return rel, nil
	}
	return nil, fmt.Errorf("no releases found in %s/%s", owner, repo)
}

func assetKind(name string) string {
	n := strings.ToLower(name)
	switch {
	case strings.HasSuffix(n, ".deb"):
		return "linux-deb"
	case strings.HasSuffix(n, "linux-amd64.tar.gz"), strings.HasSuffix(n, "linux-x86_64.tar.gz"):
		return "linux-tar"
	case strings.HasSuffix(n, "installer.exe"):
		return "windows-installer"
	case strings.HasSuffix(n, "windows-amd64.zip"):
		return "windows-zip"
	case strings.HasSuffix(n, ".dmg"):
		return "macos-dmg"
	case strings.HasSuffix(n, "macos.zip"):
		return "macos-zip"
	}
	return ""
}

func sumFor(sums, name string) string {
	for _, line := range strings.Split(sums, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			return f[0]
		}
	}
	return ""
}

func getJSON(ctx context.Context, url string, v any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "durusql-updater")
	req.Header.Set("Cache-Control", "no-cache")
	res, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == 404 {
		return fmt.Errorf("not found: %s (no release yet?)", url)
	}
	if res.StatusCode != 200 {
		return fmt.Errorf("%s: HTTP %d", url, res.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(v)
}

func getText(ctx context.Context, url string) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("User-Agent", "durusql-updater")
	res, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	return string(b), err
}
