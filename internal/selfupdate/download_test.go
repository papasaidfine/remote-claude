package selfupdate

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withToken sets the build-stamped token for one test and restores it after.
func withToken(t *testing.T, v string) {
	t.Helper()
	prev := token
	token = v
	t.Cleanup(func() { token = prev })
}

// withAPI points the package at a stub GitHub for one test.
func withAPI(t *testing.T, base string) {
	t.Helper()
	prev := apiBase
	apiBase = base
	t.Cleanup(func() { apiBase = prev })
}

// releaseJSON is a latest-release payload carrying assets for every platform we
// publish, so tests exercise the same "pick mine out of the list" path as
// production.
func releaseJSON(tag string) string {
	type asset struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	body := struct {
		TagName string  `json:"tag_name"`
		Assets  []asset `json:"assets"`
	}{
		TagName: tag,
		Assets: []asset{
			{ID: 11, Name: "remote-claude-gui_linux_amd64"},
			{ID: 22, Name: "remote-claude-gui_darwin_arm64"},
			{ID: 33, Name: "remote-claude-gui_windows_amd64.exe"},
			{ID: 44, Name: "remote-claude-gui_darwin_arm64.dmg"},
		},
	}
	b, _ := json.Marshal(body)
	return string(b)
}

func TestCheckSendsTheBearerTokenWhenOneIsBuiltIn(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		fmt.Fprint(w, releaseJSON("v9.9.9"))
	}))
	defer srv.Close()
	withAPI(t, srv.URL)
	withToken(t, "secret-pat")

	if _, err := Check("v0.0.1"); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if want := "Bearer secret-pat"; gotAuth != want {
		t.Fatalf("Authorization header = %q, want %q", gotAuth, want)
	}
}

func TestCheckOmitsAuthorizationWhenNoTokenIsBuiltIn(t *testing.T) {
	var hadAuth bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hadAuth = r.Header["Authorization"]
		fmt.Fprint(w, releaseJSON("v9.9.9"))
	}))
	defer srv.Close()
	withAPI(t, srv.URL)
	withToken(t, "")

	if _, err := Check("v0.0.1"); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if hadAuth {
		t.Fatal("Check sent an Authorization header despite having no token")
	}
}

func TestCheckPicksTheAssetIDForThisPlatform(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, releaseJSON("v9.9.9"))
	}))
	defer srv.Close()
	withAPI(t, srv.URL)
	withToken(t, "t")

	rel, err := Check("v0.0.1")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	name, err := AssetName()
	if err != nil {
		t.Skip("no GUI build published for this platform")
	}
	want := map[string]int64{
		"remote-claude-gui_linux_amd64":       11,
		"remote-claude-gui_darwin_arm64":      22,
		"remote-claude-gui_windows_amd64.exe": 33,
	}[name]
	if rel.AssetID != want {
		t.Fatalf("AssetID for %s = %d, want %d", name, rel.AssetID, want)
	}
}

func TestCheckReportsNoAssetIDWhenTheReleaseHasNoBuildForUs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"tag_name":"v9.9.9","assets":[{"id":7,"name":"something-else"}]}`)
	}))
	defer srv.Close()
	withAPI(t, srv.URL)
	withToken(t, "t")

	rel, err := Check("v0.0.1")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if rel.AssetID != 0 {
		t.Fatalf("AssetID = %d for a release with no build for us, want 0", rel.AssetID)
	}
	if !rel.HasUpdate {
		t.Fatal("HasUpdate = false; a missing asset must not hide that a newer release exists")
	}
}

func TestCheckSurfacesAnUnauthorizedTokenClearly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound) // what GitHub returns for a private repo you can't read
		fmt.Fprint(w, `{"message":"Not Found"}`)
	}))
	defer srv.Close()
	withAPI(t, srv.URL)
	withToken(t, "bad")

	_, err := Check("v0.0.1")
	if err == nil {
		t.Fatal("Check against a 404 returned no error")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Fatalf("error %q does not mention the status code", err)
	}
}

func TestDownloadAssetAsksForOctetStreamWithTheToken(t *testing.T) {
	var gotAccept, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAccept, gotAuth = r.Header.Get("Accept"), r.Header.Get("Authorization")
		fmt.Fprint(w, "BINARY")
	}))
	defer srv.Close()
	withAPI(t, srv.URL)
	withToken(t, "secret-pat")

	dest := filepath.Join(t.TempDir(), "out")
	if err := downloadAsset(http.DefaultClient, 33, dest); err != nil {
		t.Fatalf("downloadAsset: %v", err)
	}
	if want := "application/octet-stream"; gotAccept != want {
		t.Fatalf("Accept = %q, want %q", gotAccept, want)
	}
	if want := "Bearer secret-pat"; gotAuth != want {
		t.Fatalf("Authorization = %q, want %q", gotAuth, want)
	}
}

// The asset endpoint redirects to a pre-signed CDN URL. That URL authenticates
// via its own query parameters and rejects a request that also carries our
// bearer token, so the token must not travel with the redirect.
func TestDownloadAssetDoesNotSendTheTokenToTheRedirectTarget(t *testing.T) {
	var cdnSawAuth bool
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, cdnSawAuth = r.Header["Authorization"]
		fmt.Fprint(w, "BINARY")
	}))
	defer cdn.Close()

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, cdn.URL+"/signed?sig=abc", http.StatusFound)
	}))
	defer api.Close()
	withAPI(t, api.URL)
	withToken(t, "secret-pat")

	dest := filepath.Join(t.TempDir(), "out")
	if err := downloadAsset(http.DefaultClient, 33, dest); err != nil {
		t.Fatalf("downloadAsset: %v", err)
	}
	if cdnSawAuth {
		t.Fatal("the bearer token was forwarded to the redirect target")
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("reading the downloaded file: %v", err)
	}
	if string(got) != "BINARY" {
		t.Fatalf("downloaded %q, want %q", got, "BINARY")
	}
}

func TestDownloadAssetLeavesNoFileBehindWhenTheBodyIsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	withAPI(t, srv.URL)
	withToken(t, "t")

	dest := filepath.Join(t.TempDir(), "out")
	if err := downloadAsset(http.DefaultClient, 33, dest); err == nil {
		t.Fatal("downloadAsset accepted an empty body")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("an empty download left a file behind")
	}
}

func TestDownloadAssetRefusesAReleaseWithNoBuildForUs(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "out")
	err := downloadAsset(http.DefaultClient, 0, dest)
	if err == nil {
		t.Fatal("downloadAsset with no asset id returned no error")
	}
	if !strings.Contains(err.Error(), "no build") {
		t.Fatalf("error %q does not explain that this platform has no build", err)
	}
}
