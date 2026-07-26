package selfupdate

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withAPI points the package at a stub GitHub for one test.
func withAPI(t *testing.T, base string) {
	t.Helper()
	prev := apiBase
	apiBase = base
	t.Cleanup(func() { apiBase = prev })
}

func TestCheckReadsTheLatestTag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"tag_name":"v9.9.9"}`)
	}))
	defer srv.Close()
	withAPI(t, srv.URL)

	rel, err := Check("v0.0.1")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if rel.Version != "v9.9.9" {
		t.Errorf("Version = %q, want v9.9.9", rel.Version)
	}
	if !rel.HasUpdate {
		t.Error("HasUpdate = false for a newer tag")
	}
}

// The repository is public, so the update check carries no credentials — there
// is nothing to authenticate with and nothing to leak.
func TestCheckSendsNoAuthorizationHeader(t *testing.T) {
	var hadAuth bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hadAuth = r.Header["Authorization"]
		fmt.Fprint(w, `{"tag_name":"v9.9.9"}`)
	}))
	defer srv.Close()
	withAPI(t, srv.URL)

	if _, err := Check("v0.0.1"); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if hadAuth {
		t.Error("Check sent an Authorization header")
	}
}

func TestCheckSurfacesAnHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	withAPI(t, srv.URL)

	_, err := Check("v0.0.1")
	if err == nil {
		t.Fatal("Check against a 404 returned no error")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error %q does not mention the status code", err)
	}
}

func TestDownloadWritesTheBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "BINARY")
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "out")
	if err := downloadWith(http.DefaultClient, srv.URL+"/asset", dest); err != nil {
		t.Fatalf("downloadWith: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("reading the download: %v", err)
	}
	if string(got) != "BINARY" {
		t.Errorf("downloaded %q, want %q", got, "BINARY")
	}
}

// A partial or empty download must never survive to be swapped in as the running
// executable.
func TestDownloadLeavesNoFileBehindWhenTheBodyIsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "out")
	if err := downloadWith(http.DefaultClient, srv.URL+"/asset", dest); err == nil {
		t.Fatal("downloadWith accepted an empty body")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Error("an empty download left a file behind")
	}
}

func TestDownloadRejectsANonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "out")
	err := downloadWith(http.DefaultClient, srv.URL+"/asset", dest)
	if err == nil {
		t.Fatal("downloadWith accepted a 403")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("error %q does not mention the status code", err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Error("a failed download left a file behind")
	}
}
