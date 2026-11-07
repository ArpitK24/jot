package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestParseAndCompareVersions(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"0.3.1", "0.3.0", true},
		{"0.10.0", "0.9.9", true},
		{"1.0.0", "0.99.99", true},
		{"0.3.0", "0.3.0", false},
		{"0.3.0", "0.3.1", false},
		{"v0.3.1", "0.3.1-dev.abc1234", false},
	} {
		a, ok1 := parseVersion(c.a)
		b, ok2 := parseVersion(c.b)
		if !ok1 || !ok2 {
			t.Fatalf("parse %q %q", c.a, c.b)
		}
		if got := newer(a, b); got != c.want {
			t.Errorf("newer(%s, %s) = %v", c.a, c.b, got)
		}
	}
	for _, bad := range []string{"dev", "", "1.2", "1.2.x"} {
		if _, ok := parseVersion(bad); ok {
			t.Errorf("parsed %q", bad)
		}
	}
}

func TestPickReleaseSkipsCLIAndDrafts(t *testing.T) {
	rels := []ghRelease{
		{Tag: "v0.9.0"},
		{Tag: "desktop/v0.5.0", Draft: true},
		{Tag: "desktop/v0.4.0", Prerelease: true},
		{Tag: "desktop/v0.3.1"},
		{Tag: "desktop/v0.3.0"},
	}
	r, v, ok := pickRelease(rels, "windows")
	if !ok || r.Tag != "desktop/v0.3.1" || v != [3]int{0, 3, 1} {
		t.Fatalf("picked %+v %v %v", r, v, ok)
	}
}

func TestExpectedSum(t *testing.T) {
	sums := "abc123  jot-0.3.1-windows-x64-setup.exe\nfff *other.exe\n"
	if expectedSum(sums, "jot-0.3.1-windows-x64-setup.exe") != "abc123" || expectedSum(sums, "other.exe") != "fff" || expectedSum(sums, "x") != "" {
		t.Fatal("expectedSum")
	}
}

// fakeGitHub serves a releases list, an installer and its checksum file.
func fakeGitHub(t *testing.T, latest string, installer []byte, sum string) {
	t.Helper()
	name := "jot-" + latest + "-windows-x64-setup.exe"
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases":
			json.NewEncoder(w).Encode([]ghRelease{
				{Tag: "v9.9.9", Page: srv.URL + "/cli"},
				{Tag: "desktop/v" + latest, Page: srv.URL + "/page", Assets: []ghAsset{
					{Name: name, URL: srv.URL + "/setup", Size: int64(len(installer))},
					{Name: "SHA256SUMS.txt", URL: srv.URL + "/sums"},
				}},
			})
		case "/setup":
			w.Write(installer)
		case "/sums":
			w.Write([]byte(sum + "  " + name + "\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	old := releasesURL
	releasesURL = srv.URL + "/releases"
	t.Cleanup(func() { releasesURL = old })
}

func withVersion(t *testing.T, v string) {
	old := version
	version = v
	t.Cleanup(func() { version = old })
}

func TestCheckUpdate(t *testing.T) {
	a, _ := testApp(t)
	fakeGitHub(t, "0.3.1", []byte("x"), "00")
	withVersion(t, "0.3.0")
	u := a.CheckUpdate()
	if !u.Checked || !u.Available || u.Latest != "0.3.1" || !strings.HasSuffix(u.Page, "/page") {
		t.Fatalf("update: %+v", u)
	}
	if u.CanApply != (runtime.GOOS == "windows") {
		t.Fatalf("canApply on %s: %+v", runtime.GOOS, u)
	}
	if err := a.SkipUpdate("0.3.1"); err != nil || !a.CheckUpdate().Dismissed {
		t.Fatal("skip not saved")
	}
	withVersion(t, "0.3.1")
	if a.CheckUpdate().Available {
		t.Fatal("same version offered as an update")
	}
	withVersion(t, "dev")
	if a.CheckUpdate().Available {
		t.Fatal("dev build offered an update")
	}
}

func TestCheckUpdateOffline(t *testing.T) {
	a, _ := testApp(t)
	old := releasesURL
	releasesURL = "http://127.0.0.1:1/releases"
	t.Cleanup(func() { releasesURL = old })
	if u := a.CheckUpdate(); u.Checked || u.Available || u.Detail == "" {
		t.Fatalf("offline: %+v", u)
	}
}

func TestFetchInstallerVerifiesChecksum(t *testing.T) {
	old := updateOS
	updateOS = "windows" // installer updates are Windows-only; test as Windows
	t.Cleanup(func() { updateOS = old })
	a, _ := testApp(t)
	withVersion(t, "0.3.0")
	body := []byte("pretend installer")
	h := sha256.Sum256(body)
	fakeGitHub(t, "0.3.1", body, hex.EncodeToString(h[:]))
	path, _, err := a.fetchInstaller(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != string(body) {
		t.Fatal("installer not saved")
	}
	fakeGitHub(t, "0.3.1", body, strings.Repeat("0", 64))
	if _, _, err := a.fetchInstaller(context.Background(), t.TempDir()); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("bad checksum accepted: %v", err)
	}
}

func TestSetTheme(t *testing.T) {
	a, _ := testApp(t)
	for _, th := range []string{"dark", "light", "system"} {
		if err := a.SetTheme(th); err != nil || a.State().Theme != th {
			t.Fatalf("theme %s: %v %s", th, err, a.State().Theme)
		}
	}
	if a.SetTheme("neon") == nil {
		t.Fatal("bad theme accepted")
	}
	if loadPrefs(a.prefs.path).Theme != "" {
		t.Fatal("system should save as empty")
	}
}
