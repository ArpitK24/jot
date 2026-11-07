package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// releasesURL lists the repo's releases. Desktop builds are tagged
// desktop/vX.Y.Z; plain vX.Y.Z tags are CLI-only releases and are skipped.
var releasesURL = "https://api.github.com/repos/ArpitK24/jot/releases?per_page=30"

const desktopTagPrefix = "desktop/v"

// updateOS picks which installer to look for; tests override it.
var updateOS = runtime.GOOS

// Update describes the newest desktop release, for the update banner.
type Update struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	Available bool   `json:"available"`
	Page      string `json:"page"`      // release page on GitHub
	CanApply  bool   `json:"canApply"`  // the app can download and install it itself
	Size      int64  `json:"size"`      // installer size in bytes
	Checked   bool   `json:"checked"`   // false when the check couldn't reach GitHub
	Detail    string `json:"detail"`    // why the check failed
	Dismissed bool   `json:"dismissed"` // the user hid the banner for this version

	setupURL, sumsURL, setupName string
}

type ghAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

type ghRelease struct {
	Tag        string    `json:"tag_name"`
	Draft      bool      `json:"draft"`
	Prerelease bool      `json:"prerelease"`
	Page       string    `json:"html_url"`
	Assets     []ghAsset `json:"assets"`
}

// parseVersion reads "1.2.3" (an optional leading v and any -suffix are
// ignored). ok is false for anything else, such as "dev".
func parseVersion(s string) (v [3]int, ok bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

// newer reports whether version a is newer than b. A dev or pre-release
// build (0.3.1-dev.abc) counts as its base version.
func newer(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

// pickRelease returns the newest published desktop release for this OS.
func pickRelease(rels []ghRelease, goos string) (ghRelease, [3]int, bool) {
	var best ghRelease
	var bestV [3]int
	found := false
	for _, r := range rels {
		if r.Draft || r.Prerelease || !strings.HasPrefix(r.Tag, desktopTagPrefix) {
			continue
		}
		v, ok := parseVersion(strings.TrimPrefix(r.Tag, desktopTagPrefix))
		if !ok {
			continue
		}
		if !found || newer(v, bestV) {
			best, bestV, found = r, v, true
		}
	}
	return best, bestV, found
}

func installerAsset(r ghRelease, goos string) (setup, sums ghAsset) {
	for _, a := range r.Assets {
		switch {
		case goos == "windows" && strings.HasSuffix(a.Name, "-setup.exe"):
			setup = a
		case a.Name == "SHA256SUMS.txt":
			sums = a
		}
	}
	return
}

var httpClient = &http.Client{Timeout: 20 * time.Second}

func getJSON(ctx context.Context, url string, v any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "jot-desktop/"+version)
	res, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub answered %s", res.Status)
	}
	return json.NewDecoder(res.Body).Decode(v)
}

func (a *App) checkUpdate(ctx context.Context) Update {
	u := Update{Current: version}
	var rels []ghRelease
	if err := getJSON(ctx, releasesURL, &rels); err != nil {
		u.Detail = "Couldn't reach GitHub"
		return u
	}
	u.Checked = true
	r, latest, ok := pickRelease(rels, updateOS)
	if !ok {
		return u
	}
	u.Latest = fmt.Sprintf("%d.%d.%d", latest[0], latest[1], latest[2])
	u.Page = r.Page
	cur, curOK := parseVersion(version)
	u.Available = curOK && newer(latest, cur)
	setup, sums := installerAsset(r, updateOS)
	if setup.URL != "" && sums.URL != "" {
		u.CanApply, u.Size = true, setup.Size
		u.setupURL, u.sumsURL, u.setupName = setup.URL, sums.URL, setup.Name
	}
	u.Dismissed = a.prefs.SkipVersion == u.Latest
	return u
}

// CheckUpdate looks for a newer desktop release on GitHub.
func (a *App) CheckUpdate() Update {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return a.checkUpdate(ctx)
}

// SkipUpdate hides the update banner until a newer version than this one.
func (a *App) SkipUpdate(v string) error {
	a.prefs.SkipVersion = v
	return a.prefs.save()
}

// AutoUpdateCheck reports whether the app looks for updates on launch.
func (a *App) AutoUpdateCheck() bool { return !a.prefs.NoUpdateCheck }

// SetAutoUpdateCheck turns the launch-time update check on or off.
func (a *App) SetAutoUpdateCheck(on bool) error {
	a.prefs.NoUpdateCheck = !on
	return a.prefs.save()
}

// download fetches url into path and returns its SHA-256.
func download(ctx context.Context, url, path string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "jot-desktop/"+version)
	client := &http.Client{Timeout: 10 * time.Minute}
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed: %s", res.Status)
	}
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), res.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return hex.EncodeToString(h.Sum(nil)), err
}

// expectedSum finds name's hash in a sha256sum-style file.
func expectedSum(sums, name string) string {
	for _, line := range strings.Split(sums, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			return strings.ToLower(f[0])
		}
	}
	return ""
}

var errNoInstaller = errors.New("this update has to be installed from the release page")

// fetchInstaller downloads the newest installer into dir and checks it
// against the release's SHA256SUMS.txt. It returns the installer's path.
func (a *App) fetchInstaller(ctx context.Context, dir string) (string, Update, error) {
	u := a.checkUpdate(ctx)
	if !u.Available {
		return "", u, errors.New("jot is already up to date")
	}
	if !u.CanApply {
		return "", u, errNoInstaller
	}
	sumsPath := filepath.Join(dir, "SHA256SUMS.txt")
	if _, err := download(ctx, u.sumsURL, sumsPath); err != nil {
		return "", u, err
	}
	sums, err := os.ReadFile(sumsPath)
	if err != nil {
		return "", u, err
	}
	want := expectedSum(string(sums), u.setupName)
	if want == "" {
		return "", u, errors.New("the release has no checksum for its installer")
	}
	path := filepath.Join(dir, u.setupName)
	got, err := download(ctx, u.setupURL, path)
	if err != nil {
		return "", u, err
	}
	if got != want {
		os.Remove(path)
		return "", u, errors.New("the download didn't match its checksum, so it wasn't installed")
	}
	return path, u, nil
}

// InstallUpdate downloads the newest release, verifies it and starts its
// installer in the background. The installer closes jot, upgrades it in
// place and opens it again; the window should quit right after this.
func (a *App) InstallUpdate() (Update, error) {
	if updateOS != "windows" {
		return Update{}, errNoInstaller
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	dir, err := os.MkdirTemp("", "jot-update-")
	if err != nil {
		return Update{}, err
	}
	path, u, err := a.fetchInstaller(ctx, dir)
	if err != nil {
		return u, err
	}
	return u, runInstaller(path)
}
