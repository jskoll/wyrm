package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// rewriteTransport sends every request to srv regardless of the request's
// original host, so code that builds requests against a hardcoded host
// (selfupdate's api.github.com URLs) can be pointed at a local httptest
// server without making that host configurable.
type rewriteTransport struct{ addr string }

func (t rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme = "http"
	req.URL.Host = t.addr
	return http.DefaultTransport.RoundTrip(req)
}

func assetName(version string) string {
	return fmt.Sprintf("wyrm_%s_%s_%s.tar.gz", version, runtime.GOOS, runtime.GOARCH)
}

func buildTestArchive(t *testing.T, binaryContents string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	hdr := &tar.Header{Name: "wyrm", Mode: 0o755, Size: int64(len(binaryContents))}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(binaryContents)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeRelease(w http.ResponseWriter, srvURL, tag, asset string) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{
		"tag_name": %q,
		"assets": [
			{"name": "checksums.txt", "browser_download_url": %q},
			{"name": %q, "browser_download_url": %q}
		]
	}`, tag, srvURL+"/assets/checksums.txt", asset, srvURL+"/assets/archive")
}

// selfupdateServer serves a fake GitHub release for tag, plus an archive
// containing binaryContents and a matching checksums.txt, wiring together
// everything installRelease needs to run its download-and-verify flow
// end to end.
func selfupdateServer(t *testing.T, tag, binaryContents string) *httptest.Server {
	t.Helper()
	ver := strings.TrimPrefix(tag, "v")
	asset := assetName(ver)
	archive := buildTestArchive(t, binaryContents)
	sum := sha256.Sum256(archive)
	checksums := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), asset)

	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/repos/jskoll/wyrm/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		writeRelease(w, srv.URL, tag, asset)
	})
	mux.HandleFunc("/repos/jskoll/wyrm/releases/tags/"+tag, func(w http.ResponseWriter, _ *http.Request) {
		writeRelease(w, srv.URL, tag, asset)
	})
	mux.HandleFunc("/assets/archive", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	})
	mux.HandleFunc("/assets/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(checksums))
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// testApp builds an app whose httpClient routes every request to srv.
func testApp(srv *httptest.Server) (a *app, stdout, stderr *bytes.Buffer) {
	stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	client := &http.Client{Transport: rewriteTransport{addr: srv.Listener.Addr().String()}}
	return &app{stdout: stdout, stderr: stderr, httpClient: client}, stdout, stderr
}

func writeFakeBinary(t *testing.T, contents string) (path string, mode os.FileMode) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "wyrm")
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, info.Mode()
}

// setVersion overrides main's package-level `version` var (normally stamped
// at build time via -ldflags) for the duration of the test, so
// versionString()'s comparisons are deterministic here.
func setVersion(t *testing.T, v string) {
	t.Helper()
	old := version
	version = v
	t.Cleanup(func() { version = old })
}

func TestSelfupdateCheckUpToDate(t *testing.T) {
	setVersion(t, "1.2.3")
	srv := selfupdateServer(t, "v1.2.3", "irrelevant")
	a, stdout, _ := testApp(srv)

	if err := a.selfupdate([]string{"-check"}); err != nil {
		t.Fatalf("selfupdate -check: %v", err)
	}
	if got := stdout.String(); !strings.Contains(got, "up to date") {
		t.Errorf("stdout = %q, want mentioning up to date", got)
	}
}

func TestSelfupdateCheckUpdateAvailable(t *testing.T) {
	setVersion(t, "1.0.0")
	srv := selfupdateServer(t, "v2.0.0", "irrelevant")
	a, stdout, _ := testApp(srv)

	if err := a.selfupdate([]string{"-check"}); err != nil {
		t.Fatalf("selfupdate -check: %v", err)
	}
	got := stdout.String()
	if !strings.Contains(got, "2.0.0") || !strings.Contains(got, "1.0.0") {
		t.Errorf("stdout = %q, want mentioning both versions", got)
	}
}

func TestSelfupdateNoOpWhenAlreadyLatest(t *testing.T) {
	setVersion(t, "2.0.0")
	srv := selfupdateServer(t, "v2.0.0", "new binary contents")
	// selfupdate exits before touching os.Executable() in the no-op case,
	// so this is safe to run against the real running binary.
	a, stdout, _ := testApp(srv)

	if err := a.selfupdate(nil); err != nil {
		t.Fatalf("selfupdate: %v", err)
	}
	if got := stdout.String(); !strings.Contains(got, "already up to date") {
		t.Errorf("stdout = %q, want already up to date", got)
	}
}

func TestSelfupdateRejectsPositionalArgs(t *testing.T) {
	a := &app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	if err := a.selfupdate([]string{"extra"}); err == nil {
		t.Fatal("selfupdate: want error for unexpected positional argument, got nil")
	}
}

// The tests below exercise installRelease directly rather than through
// selfupdate, since selfupdate resolves the binary to replace via
// os.Executable() — going through it here would overwrite the compiled
// test binary itself. installRelease is exactly the part of selfupdate
// that does the download/verify/replace; only the path it's given differs.

func TestInstallReleaseInstallsNewerRelease(t *testing.T) {
	srv := selfupdateServer(t, "v2.0.0", "new binary contents")
	a, stdout, _ := testApp(srv)
	rel, err := fetchRelease(a.httpClient, "")
	if err != nil {
		t.Fatalf("fetchRelease: %v", err)
	}

	path, mode := writeFakeBinary(t, "old binary contents")
	if err := a.installRelease(a.httpClient, rel, path, mode, "1.0.0"); err != nil {
		t.Fatalf("installRelease: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new binary contents" {
		t.Errorf("installed content = %q, want %q", data, "new binary contents")
	}
	if got := stdout.String(); !strings.Contains(got, "1.0.0") || !strings.Contains(got, "2.0.0") {
		t.Errorf("stdout = %q, want mentioning both versions", got)
	}
}

func TestInstallReleasePinnedVersionOverridesNewerCurrent(t *testing.T) {
	srv := selfupdateServer(t, "v1.5.0", "pinned binary contents")
	a, stdout, _ := testApp(srv)
	rel, err := fetchRelease(a.httpClient, "1.5.0")
	if err != nil {
		t.Fatalf("fetchRelease: %v", err)
	}

	path, mode := writeFakeBinary(t, "old binary contents")
	if err := a.installRelease(a.httpClient, rel, path, mode, "2.0.0"); err != nil {
		t.Fatalf("installRelease: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "pinned binary contents" {
		t.Errorf("installed content = %q, want the pinned binary", data)
	}
	if got := stdout.String(); !strings.Contains(got, "updated wyrm 2.0.0 -> 1.5.0") {
		t.Errorf("stdout = %q, want it to report the downgrade", got)
	}
}

func TestInstallReleaseChecksumMismatchIsNotInstalled(t *testing.T) {
	tag, ver := "v2.0.0", "2.0.0"
	asset := assetName(ver)
	goodArchive := buildTestArchive(t, "new binary contents")
	sum := sha256.Sum256(goodArchive)
	checksums := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), asset)

	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/repos/jskoll/wyrm/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		writeRelease(w, srv.URL, tag, asset)
	})
	mux.HandleFunc("/assets/archive", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("tampered bytes that do not match checksums.txt"))
	})
	mux.HandleFunc("/assets/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(checksums))
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	a, _, _ := testApp(srv)
	rel, err := fetchRelease(a.httpClient, "")
	if err != nil {
		t.Fatalf("fetchRelease: %v", err)
	}
	path, mode := writeFakeBinary(t, "old binary contents")

	if err := a.installRelease(a.httpClient, rel, path, mode, "1.0.0"); err == nil {
		t.Fatal("installRelease: want a checksum error, got nil")
	} else if !strings.Contains(err.Error(), "checksum") {
		t.Errorf("err = %v, want mentioning checksum", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "old binary contents" {
		t.Errorf("binary was modified despite the checksum failure: %q", data)
	}
}

func TestInstallReleaseMissingAssetForPlatform(t *testing.T) {
	srv := selfupdateServer(t, "v2.0.0", "irrelevant")
	a, _, _ := testApp(srv)
	rel, err := fetchRelease(a.httpClient, "")
	if err != nil {
		t.Fatalf("fetchRelease: %v", err)
	}
	// Simulate a platform goreleaser doesn't build for.
	delete(rel.Assets, assetName("2.0.0"))

	path, mode := writeFakeBinary(t, "old binary contents")
	if err := a.installRelease(a.httpClient, rel, path, mode, "1.0.0"); err == nil {
		t.Fatal("installRelease: want an error for a missing platform asset, got nil")
	}
}
