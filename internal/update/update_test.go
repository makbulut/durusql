package update

import (
	"context"
	"testing"
	"time"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"0.6.0", "0.5.0", true}, {"0.5.0", "0.6.0", false}, {"0.6.0", "0.6.0", false},
		{"0.6.0", "0.6.0-beta.1", true}, {"0.6.0-beta.2", "0.6.0-beta.1", true}, {"0.6.0-beta.1", "0.6.0", false},
		{"v1.0.0", "0.9.9", true}, {"0.10.0", "0.9.0", true},
	}
	for _, c := range cases {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q,%q)=%v want %v", c.a, c.b, got, c.want)
		}
	}
	for name, kind := range map[string]string{"rowdy_0.6.0_amd64.deb": "linux-deb", "rowdy-0.6.0-linux-amd64.tar.gz": "linux-tar", "rowdy-0.6.0-windows-amd64.zip": "windows-zip", "rowdy-0.6.0-installer.exe": "windows-installer", "rowdy-0.6.0-macos.dmg": "macos-dmg", "SHA256SUMS": ""} {
		if assetKind(name) != kind {
			t.Errorf("assetKind(%s)=%q want %q", name, assetKind(name), kind)
		}
	}
	for src, ok := range map[string]bool{"https://github.com/foo/rowdy": true, "github:foo/rowdy": true, "https://example.com/rowdy/": false, "": false} {
		if IsGitHub(src) != ok {
			t.Errorf("IsGitHub(%q)=%v", src, IsGitHub(src))
		}
	}
}

// Parses a real public repository's releases (network); skipped when offline.
func TestGitHubFetch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	rel, err := FetchRelease(ctx, "https://github.com/wailsapp/wails", "stable")
	if err != nil {
		t.Skipf("offline or rate-limited: %v", err)
	}
	t.Logf("wails latest: %s (%s) notes=%d chars files=%v", rel.Version, rel.Date, len(rel.Notes), len(rel.Files))
	if rel.Version == "" {
		t.Error("empty version")
	}
	st, err := Check(ctx, "https://github.com/wailsapp/wails", "beta", "0.0.1")
	t.Logf("beta check: latest=%s available=%v err=%v kind=%s", st.Latest, st.Available, err, st.Kind)
}
