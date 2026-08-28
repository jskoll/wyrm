package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jskoll/wyrm/internal/selfupdate"
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
			{"name": "checksums.txt.minisig", "browser_download_url": %q},
			{"name": %q, "browser_download_url": %q}
		]
	}`, tag, srvURL+"/assets/checksums.txt", srvURL+"/assets/checksums.sig",
		asset, srvURL+"/assets/archive")
}

// writeUnsignedRelease is writeRelease without the signature asset, for the
// case a build with a key embedded must refuse.
func writeUnsignedRelease(w http.ResponseWriter, srvURL, tag, asset string) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{
		"tag_name": %q,
		"assets": [
			{"name": "checksums.txt", "browser_download_url": %q},
			{"name": %q, "browser_download_url": %q}
		]
	}`, tag, srvURL+"/assets/checksums.txt", asset, srvURL+"/assets/archive")
}

// useTestSigningKey points DefaultSigningKey at a throwaway keypair for one
// test and returns the private half, so a test can serve a release signed by
// the key the binary will check against.
//
// Real releases are signed with the key whose public half is committed at
// internal/selfupdate/signing.pub; a test cannot have that secret, so it
// substitutes its own pair rather than shipping a fixture signed by a key
// nobody can reproduce.
func useTestSigningKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	prev := selfupdate.DefaultSigningKey
	selfupdate.DefaultSigningKey = selfupdate.SigningKeyFromEd25519(pub)
	t.Cleanup(func() { selfupdate.DefaultSigningKey = prev })
	return priv
}

// selfupdateServer serves a fake GitHub release for tag, plus an archive
// containing binaryContents and a matching checksums.txt, wiring together
// everything installRelease needs to run its download-and-verify flow
// end to end.
func selfupdateServer(t *testing.T, tag, binaryContents string) *httptest.Server {
	t.Helper()
	return selfupdateServerSigned(t, tag, binaryContents, useTestSigningKey(t))
}

// selfupdateServerSigned serves a release whose checksums.txt carries a
// signature made by priv. Pass a nil key to serve the release unsigned.
func selfupdateServerSigned(t *testing.T, tag, binaryContents string, priv ed25519.PrivateKey) *httptest.Server {
	t.Helper()
	ver := strings.TrimPrefix(tag, "v")
	asset := assetName(ver)
	archive := buildTestArchive(t, binaryContents)
	sum := sha256.Sum256(archive)
	checksums := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), asset)

	// A bare base64 Ed25519 signature over checksums.txt — one of the forms
	// ParseSignature accepts, and the one a test can produce without
	// reimplementing minisign's envelope. The minisign envelope itself is
	// covered in internal/selfupdate against artifacts from the real binary.
	var signature string
	if priv != nil {
		signature = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(checksums)))
	}

	mux := http.NewServeMux()
	var srv *httptest.Server
	release := writeRelease
	if priv == nil {
		release = writeUnsignedRelease
	}
	mux.HandleFunc("/repos/jskoll/wyrm/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		release(w, srv.URL, tag, asset)
	})
	mux.HandleFunc("/repos/jskoll/wyrm/releases/tags/"+tag, func(w http.ResponseWriter, _ *http.Request) {
		release(w, srv.URL, tag, asset)
	})
	mux.HandleFunc("/assets/archive", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	})
	mux.HandleFunc("/assets/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(checksums))
	})
	mux.HandleFunc("/assets/checksums.sig", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(signature))
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

func TestSelfupdateTimesOut(t *testing.T) {
	// A server that hangs without responding
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer srv.Close()

	client := &http.Client{
		Transport: rewriteTransport{addr: srv.Listener.Addr().String()},
		Timeout:   50 * time.Millisecond,
	}
	a := &app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, httpClient: client}
	if err := a.selfupdate([]string{"-check"}); err == nil {
		t.Fatal("selfupdate: want error on stalled/timed out request, got nil")
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

func TestInstallReleaseSignatureVerification(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	tag, ver := "v2.0.0", "2.0.0"
	asset := assetName(ver)
	archive := buildTestArchive(t, "signed binary contents")
	sum := sha256.Sum256(archive)
	checksums := []byte(fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), asset))
	sig := ed25519.Sign(priv, checksums)

	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/repos/jskoll/wyrm/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
			"tag_name": %q,
			"assets": [
				{"name": "checksums.txt", "browser_download_url": %q},
				{"name": "checksums.txt.sig", "browser_download_url": %q},
				{"name": %q, "browser_download_url": %q}
			]
		}`, tag, srv.URL+"/assets/checksums.txt", srv.URL+"/assets/checksums.txt.sig", asset, srv.URL+"/assets/archive")
	})
	mux.HandleFunc("/assets/archive", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	})
	mux.HandleFunc("/assets/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(checksums)
	})
	mux.HandleFunc("/assets/checksums.txt.sig", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(sig)
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	withSigningKey(t, pub)

	a, stdout, _ := testApp(srv)
	rel, err := fetchRelease(a.httpClient, "")
	if err != nil {
		t.Fatalf("fetchRelease: %v", err)
	}
	path, mode := writeFakeBinary(t, "old binary contents")

	if err := a.installRelease(a.httpClient, rel, path, mode, "1.0.0"); err != nil {
		t.Fatalf("installRelease with valid signature: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "signed binary contents" {
		t.Errorf("installed content = %q, want signed binary contents", data)
	}
	if !strings.Contains(stdout.String(), "updated wyrm") {
		t.Errorf("stdout = %q, want updated message", stdout.String())
	}
}

func TestInstallReleaseInvalidSignatureFails(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, anotherPriv, _ := ed25519.GenerateKey(rand.Reader)

	tag, ver := "v2.0.0", "2.0.0"
	asset := assetName(ver)
	archive := buildTestArchive(t, "signed binary contents")
	sum := sha256.Sum256(archive)
	checksums := []byte(fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), asset))
	// Sign with a different private key
	badSig := ed25519.Sign(anotherPriv, checksums)

	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/repos/jskoll/wyrm/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
			"tag_name": %q,
			"assets": [
				{"name": "checksums.txt", "browser_download_url": %q},
				{"name": "checksums.txt.sig", "browser_download_url": %q},
				{"name": %q, "browser_download_url": %q}
			]
		}`, tag, srv.URL+"/assets/checksums.txt", srv.URL+"/assets/checksums.txt.sig", asset, srv.URL+"/assets/archive")
	})
	mux.HandleFunc("/assets/archive", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	})
	mux.HandleFunc("/assets/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(checksums)
	})
	mux.HandleFunc("/assets/checksums.txt.sig", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(badSig)
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	withSigningKey(t, pub)

	a, _, _ := testApp(srv)
	rel, err := fetchRelease(a.httpClient, "")
	if err != nil {
		t.Fatalf("fetchRelease: %v", err)
	}
	path, mode := writeFakeBinary(t, "old binary contents")

	if err := a.installRelease(a.httpClient, rel, path, mode, "1.0.0"); err == nil {
		t.Fatal("installRelease with invalid signature: want error, got nil")
	}
}

// withSigningKey compiles a signing key into selfupdate for one test.
func withSigningKey(t *testing.T, pub ed25519.PublicKey) {
	t.Helper()
	prev := selfupdate.DefaultSigningKey
	selfupdate.DefaultSigningKey = selfupdate.SigningKeyFromEd25519(pub)
	t.Cleanup(func() { selfupdate.DefaultSigningKey = prev })
}

// A release that publishes no signature must be refused by a build that has a
// signing key: that combination means either a downgrade to an unsigned
// release or a stripped signature, and it is exactly what the key is for.
func TestInstallReleaseRequiresSignatureWhenKeyEmbedded(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tag, ver := "v2.0.0", "2.0.0"
	asset := assetName(ver)
	archive := buildTestArchive(t, "unsigned binary")
	sum := sha256.Sum256(archive)
	checksums := []byte(fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), asset))

	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/repos/jskoll/wyrm/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
			"tag_name": %q,
			"assets": [
				{"name": "checksums.txt", "browser_download_url": %q},
				{"name": %q, "browser_download_url": %q}
			]
		}`, tag, srv.URL+"/assets/checksums.txt", asset, srv.URL+"/assets/archive")
	})
	mux.HandleFunc("/assets/archive", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(archive) })
	mux.HandleFunc("/assets/checksums.txt", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(checksums) })
	srv = httptest.NewServer(mux)
	defer srv.Close()

	withSigningKey(t, pub)

	a, _, _ := testApp(srv)
	rel, err := fetchRelease(a.httpClient, "")
	if err != nil {
		t.Fatalf("fetchRelease: %v", err)
	}
	path, mode := writeFakeBinary(t, "old binary contents")
	err = a.installRelease(a.httpClient, rel, path, mode, "1.0.0")
	if err == nil {
		t.Fatal("want an error installing an unsigned release with a key embedded")
	}
	if !strings.Contains(err.Error(), "no signature") {
		t.Errorf("error = %v, want it to name the missing signature", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "old binary contents" {
		t.Errorf("binary was replaced despite the failure: %q", data)
	}
}

// Without a key embedded — the state every build shipped in until now — an
// unsigned release still installs, but the user is told the release was not
// signature-verified instead of being left to assume it was.
func TestInstallReleaseWarnsWhenUnverifiable(t *testing.T) {
	tag, ver := "v2.0.0", "2.0.0"
	asset := assetName(ver)
	archive := buildTestArchive(t, "unsigned binary")
	sum := sha256.Sum256(archive)
	checksums := []byte(fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), asset))

	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/repos/jskoll/wyrm/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
			"tag_name": %q,
			"assets": [
				{"name": "checksums.txt", "browser_download_url": %q},
				{"name": %q, "browser_download_url": %q}
			]
		}`, tag, srv.URL+"/assets/checksums.txt", asset, srv.URL+"/assets/archive")
	})
	mux.HandleFunc("/assets/archive", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(archive) })
	mux.HandleFunc("/assets/checksums.txt", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(checksums) })
	srv = httptest.NewServer(mux)
	defer srv.Close()

	prev := selfupdate.DefaultSigningKey
	selfupdate.DefaultSigningKey = selfupdate.SigningKey{}
	t.Cleanup(func() { selfupdate.DefaultSigningKey = prev })

	a, _, stderr := testApp(srv)
	rel, err := fetchRelease(a.httpClient, "")
	if err != nil {
		t.Fatalf("fetchRelease: %v", err)
	}
	path, mode := writeFakeBinary(t, "old binary contents")
	if err := a.installRelease(a.httpClient, rel, path, mode, "1.0.0"); err != nil {
		t.Fatalf("installRelease: %v", err)
	}
	if !strings.Contains(stderr.String(), "not signed") {
		t.Errorf("stderr = %q, want a warning that the release is unsigned", stderr.String())
	}
}

// A build with a signing key compiled in must refuse a release that carries no
// signature. Before a key was generated this could not be exercised at all —
// the embedded key was a placeholder, so every release took the "unsigned,
// warn and continue" path and the enforcement branch was dead code.
func TestInstallReleaseRefusesUnsignedWhenKeyEmbedded(t *testing.T) {
	useTestSigningKey(t) // a valid key is embedded...
	srv := selfupdateServerSigned(t, "v2.0.0", "new binary contents", nil)
	a, _, _ := testApp(srv)
	rel, err := fetchRelease(a.httpClient, "")
	if err != nil {
		t.Fatalf("fetchRelease: %v", err)
	}

	path, mode := writeFakeBinary(t, "old binary contents")
	err = a.installRelease(a.httpClient, rel, path, mode, "1.0.0")
	if err == nil {
		t.Fatal("want a refusal for an unsigned release when a key is embedded")
	}
	if !strings.Contains(err.Error(), "no signature") {
		t.Errorf("error = %v, want it to name the missing signature", err)
	}

	// The binary on disk must be untouched: refusing has to mean refusing.
	data, rerr := os.ReadFile(path)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if string(data) != "old binary contents" {
		t.Errorf("binary was replaced despite the refusal: %q", data)
	}
}

// A signature made by the wrong key is a tampered release, not a missing one,
// and must fail loudly rather than fall back to checksums.
func TestInstallReleaseRejectsSignatureFromAnotherKey(t *testing.T) {
	useTestSigningKey(t)
	_, wrongKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	srv := selfupdateServerSigned(t, "v2.0.0", "new binary contents", wrongKey)
	a, _, _ := testApp(srv)
	rel, ferr := fetchRelease(a.httpClient, "")
	if ferr != nil {
		t.Fatalf("fetchRelease: %v", ferr)
	}

	path, mode := writeFakeBinary(t, "old binary contents")
	if err := a.installRelease(a.httpClient, rel, path, mode, "1.0.0"); err == nil {
		t.Fatal("want a refusal for a signature made by an unknown key")
	}
	data, rerr := os.ReadFile(path)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if string(data) != "old binary contents" {
		t.Errorf("binary was replaced despite a bad signature: %q", data)
	}
}
