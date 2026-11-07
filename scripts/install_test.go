// Package scripts holds the one-line installers. This test runs
// install.sh against a local fake of GitHub's releases API and downloads.
package scripts

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func fakeArchive(t *testing.T, name string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := []byte("#!/bin/sh\necho jot 1.2.3\n")
	tw.WriteHeader(&tar.Header{Name: name + "/", Typeflag: tar.TypeDir, Mode: 0o755})
	tw.WriteHeader(&tar.Header{Name: name + "/jot", Mode: 0o755, Size: int64(len(body))})
	tw.Write(body)
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestInstallSh(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("install.sh is for macOS and Linux")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	osName := map[string]string{"linux": "linux", "darwin": "darwin"}[runtime.GOOS]
	arch := map[string]string{"amd64": "amd64", "arm64": "arm64"}[runtime.GOARCH]
	if osName == "" || arch == "" {
		t.Skip("platform not covered by the installer")
	}
	name := fmt.Sprintf("jot_1.2.3_%s_%s", osName, arch)
	archive := fakeArchive(t, name)
	sum := fmt.Sprintf("%x", sha256.Sum256(archive))
	badSum := false

	mux := http.NewServeMux()
	mux.HandleFunc("/releases", func(w http.ResponseWriter, r *http.Request) {
		// Desktop releases come first, as they do on GitHub.
		fmt.Fprint(w, `[
  {
    "tag_name": "desktop/v9.0.0",
    "name": "jot desktop"
  },
  {
    "tag_name": "v1.2.3",
    "name": "jot 1.2.3"
  },
  {
    "tag_name": "v1.2.2"
  }
]`)
	})
	mux.HandleFunc("/download/v1.2.3/"+name+".tar.gz", func(w http.ResponseWriter, r *http.Request) { w.Write(archive) })
	mux.HandleFunc("/download/v1.2.3/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		s := sum
		if badSum {
			s = strings.Repeat("0", 64)
		}
		fmt.Fprintf(w, "%s  other.zip\n%s  %s.tar.gz\n", strings.Repeat("a", 64), s, name)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	install := func(dir string) (string, error) {
		cmd := exec.Command("sh", "install.sh")
		cmd.Env = append(os.Environ(),
			"JOT_RELEASES_API="+srv.URL+"/releases",
			"JOT_DOWNLOAD_URL="+srv.URL+"/download",
			"JOT_INSTALL_DIR="+dir,
			"JOT_VERSION=",
		)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	dir := filepath.Join(t.TempDir(), "bin")
	out, err := install(dir)
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	for _, want := range []string{"Installing jot 1.2.3", "Checksum OK", "Installed jot 1.2.3", "jot setup"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if fi, err := os.Stat(filepath.Join(dir, "jot")); err != nil || fi.Mode()&0o111 == 0 {
		t.Fatalf("jot not installed as an executable: %v", err)
	}

	badSum = true
	dir2 := filepath.Join(t.TempDir(), "bin")
	out, err = install(dir2)
	if err == nil || !strings.Contains(out, "checksum mismatch") {
		t.Fatalf("want checksum failure, got %v:\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dir2, "jot")); err == nil {
		t.Fatal("installed despite a bad checksum")
	}
}
