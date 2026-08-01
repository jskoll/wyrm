package selfupdate

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newReleaseServer(t *testing.T, tag string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wantLatest := "/repos/" + owner + "/" + repo + "/releases/latest"
		wantTag := "/repos/" + owner + "/" + repo + "/releases/tags/" + tag
		if r.URL.Path != wantLatest && r.URL.Path != wantTag {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
			"tag_name": %q,
			"assets": [
				{"name": "checksums.txt", "browser_download_url": "https://example.invalid/checksums.txt"},
				{"name": "wyrm_%s_linux_amd64.tar.gz", "browser_download_url": "https://example.invalid/wyrm_linux_amd64.tar.gz"}
			]
		}`, tag, tag[1:])
	}))
	t.Cleanup(srv.Close)
	return srv
}

// withTestAPI points fetch's requests at srv instead of api.github.com by
// wrapping the client with a RoundTripper that rewrites the host, so Latest
// and Tag can be exercised without their apiBase being configurable.
func withTestAPI(srv *httptest.Server) HTTPDoer {
	return roundTripDoer{srv: srv}
}

type roundTripDoer struct{ srv *httptest.Server }

func (d roundTripDoer) Do(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme = "http"
	req.URL.Host = d.srv.Listener.Addr().String()
	return http.DefaultClient.Do(req)
}

func TestLatestParsesReleaseAndAssets(t *testing.T) {
	srv := newReleaseServer(t, "v1.2.3")
	rel, err := Latest(withTestAPI(srv))
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if rel.Version != "1.2.3" {
		t.Errorf("Version = %q, want 1.2.3", rel.Version)
	}
	if _, ok := rel.Assets["checksums.txt"]; !ok {
		t.Errorf("Assets missing checksums.txt: %v", rel.Assets)
	}
	if _, ok := rel.Assets["wyrm_1.2.3_linux_amd64.tar.gz"]; !ok {
		t.Errorf("Assets missing the linux/amd64 archive: %v", rel.Assets)
	}
}

func TestTagAcceptsWithOrWithoutLeadingV(t *testing.T) {
	srv := newReleaseServer(t, "v0.6.2")
	for _, pin := range []string{"0.6.2", "v0.6.2"} {
		rel, err := Tag(withTestAPI(srv), pin)
		if err != nil {
			t.Fatalf("Tag(%q): %v", pin, err)
		}
		if rel.Version != "0.6.2" {
			t.Errorf("Tag(%q).Version = %q, want 0.6.2", pin, rel.Version)
		}
	}
}

func TestFetchNotFoundReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()
	if _, err := Latest(withTestAPI(srv)); err == nil {
		t.Fatal("Latest: want error on 404, got nil")
	}
}

func TestAssetName(t *testing.T) {
	got := AssetName("0.6.2", "linux", "arm64")
	want := "wyrm_0.6.2_linux_arm64.tar.gz"
	if got != want {
		t.Errorf("AssetName = %q, want %q", got, want)
	}
}

func TestGetDownloadsBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("archive-bytes"))
	}))
	defer srv.Close()
	data, err := Get(http.DefaultClient, srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(data) != "archive-bytes" {
		t.Errorf("Get = %q, want %q", data, "archive-bytes")
	}
}

func TestGetErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	if _, err := Get(http.DefaultClient, srv.URL); err == nil {
		t.Fatal("Get: want error on 500, got nil")
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"1.2.3", "1.2.3", 0},
		{"v1.2.3", "1.2.3", 0},
		{"1.2", "1.2.0", 0},
		{"1.2.3", "1.2.4", -1},
		{"1.3.0", "1.2.9", 1},
		{"2.0.0", "1.9.9", 1},
		{"0.6.2-rc1", "0.6.2", 0},
	}
	for _, tt := range tests {
		if got := CompareVersions(tt.a, tt.b); got != tt.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}
