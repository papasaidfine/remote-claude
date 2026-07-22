package webui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"github.com/papasaidfine/remote-claude/internal/bridge"
	"github.com/papasaidfine/remote-claude/internal/core"
	"github.com/papasaidfine/remote-claude/internal/paths"
	"github.com/papasaidfine/remote-claude/internal/provision"
	"github.com/papasaidfine/remote-claude/internal/store"
)

type fakeManager struct {
	mu      sync.Mutex
	started []bridge.Spec
	stopped []string
}

func (f *fakeManager) Start(s bridge.Spec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = append(f.started, s)
	return nil
}
func (f *fakeManager) Stop(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, id)
}
func (f *fakeManager) Status(id string) bridge.Status {
	return bridge.Status{HostID: id, State: bridge.StateStopped}
}

type fakePlat struct{}

func (fakePlat) Name() string            { return "TestOS" }
func (fakePlat) SupportsXray() bool      { return true }
func (fakePlat) StatusIncomingSSH() bool { return false }

type fakeProv struct {
	err       error
	calls     int
	lastAlias string
	lastHost  store.Host
}

func (f *fakeProv) EnsureClient(h store.Host, alias string) error {
	f.calls++
	f.lastHost = h
	f.lastAlias = alias
	return f.err
}

func (f *fakeProv) ServerBootstrap(h store.Host, alias, password string) (provision.ServerResult, error) {
	if f.err != nil {
		return provision.ServerResult{}, f.err
	}
	return provision.ServerResult{ServerPubKey: "ssh-ed25519 AAAAkey server", Authorized: true, Alias: alias}, nil
}

func (f *fakeProv) EnsureLocalSSHD(disablePassword bool) error { return f.err }

func newTestServer(t *testing.T, prov core.Provisioner) (*Server, *fakeManager) {
	t.Helper()
	dir := t.TempDir()
	p := paths.Paths{RCConfigDir: dir, VlessNodes: filepath.Join(dir, "vless-nodes.txt")}
	fm := &fakeManager{}
	app := core.New(&store.Config{}, filepath.Join(dir, "config.json"), p, fm, prov, fakePlat{})
	return New(app), fm
}

func do(t *testing.T, s *Server, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var buf *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		buf = bytes.NewReader(b)
	} else {
		buf = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, buf)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	var m map[string]any
	json.Unmarshal(rec.Body.Bytes(), &m)
	return rec.Code, m
}

func addHost(t *testing.T, s *Server) string {
	t.Helper()
	code, resp := do(t, s, "POST", "/api/hosts", map[string]any{
		"name": "workbox", "hostname": "srv.example.com", "user": "dev", "port": 22, "reverse_port": 2222,
	})
	if code != 200 {
		t.Fatalf("add host: code %d resp %v", code, resp)
	}
	id, _ := resp["id"].(string)
	if id == "" {
		t.Fatalf("add host returned no id: %v", resp)
	}
	return id
}

func TestStateEmpty(t *testing.T) {
	s, _ := newTestServer(t, nil)
	code, resp := do(t, s, "GET", "/api/state", nil)
	if code != 200 {
		t.Fatalf("state code %d", code)
	}
	if resp["platform"] != "TestOS" {
		t.Errorf("platform = %v", resp["platform"])
	}
	if hs, _ := resp["hosts"].([]any); len(hs) != 0 {
		t.Errorf("expected no hosts, got %v", resp["hosts"])
	}
}

func TestAddAndListHost(t *testing.T) {
	s, _ := newTestServer(t, nil)
	addHost(t, s)
	_, resp := do(t, s, "GET", "/api/state", nil)
	hs, _ := resp["hosts"].([]any)
	if len(hs) != 1 {
		t.Fatalf("expected 1 host, got %d", len(hs))
	}
}

func TestAddHostValidation(t *testing.T) {
	s, _ := newTestServer(t, nil)
	code, resp := do(t, s, "POST", "/api/hosts", map[string]any{"name": "x"})
	if code != 400 {
		t.Fatalf("expected 400 for invalid host, got %d (%v)", code, resp)
	}
}

func TestDeleteHostStopsTunnel(t *testing.T) {
	s, fm := newTestServer(t, nil)
	id := addHost(t, s)
	code, _ := do(t, s, "DELETE", "/api/hosts/"+id, nil)
	if code != 200 {
		t.Fatalf("delete code %d", code)
	}
	if len(fm.stopped) != 1 || fm.stopped[0] != id {
		t.Errorf("expected Stop(%q), got %v", id, fm.stopped)
	}
	_, resp := do(t, s, "GET", "/api/state", nil)
	if hs, _ := resp["hosts"].([]any); len(hs) != 0 {
		t.Errorf("host not removed")
	}
}

func TestStartUnknownHost(t *testing.T) {
	s, _ := newTestServer(t, nil)
	code, _ := do(t, s, "POST", "/api/hosts/nope/start", nil)
	if code != 404 {
		t.Fatalf("expected 404, got %d", code)
	}
}

func TestStartProvisionErrorDoesNotStart(t *testing.T) {
	prov := &fakeProv{err: fmt.Errorf("no key")}
	s, fm := newTestServer(t, prov)
	id := addHost(t, s)
	code, resp := do(t, s, "POST", "/api/hosts/"+id+"/start", nil)
	if code != 500 {
		t.Fatalf("expected 500, got %d (%v)", code, resp)
	}
	if len(fm.started) != 0 {
		t.Errorf("tunnel started despite provision failure: %v", fm.started)
	}
}

func TestStartSuccess(t *testing.T) {
	prov := &fakeProv{}
	s, fm := newTestServer(t, prov)
	// set an alias so we can assert it flows to the provisioner
	do(t, s, "POST", "/api/alias", map[string]string{"alias": "lisa-laptop"})
	id := addHost(t, s)
	code, _ := do(t, s, "POST", "/api/hosts/"+id+"/start", nil)
	if code != 200 {
		t.Fatalf("start code %d", code)
	}
	if prov.calls != 1 || prov.lastAlias != "lisa-laptop" {
		t.Errorf("EnsureClient not called with alias: calls=%d alias=%q", prov.calls, prov.lastAlias)
	}
	if len(fm.started) != 1 {
		t.Fatalf("expected 1 start, got %d", len(fm.started))
	}
	got := fm.started[0]
	if got.Alias != paths.Alias || got.ReversePort != 2222 || got.HostID != id {
		t.Errorf("bad spec: %+v", got)
	}
}

func TestAliasPersistedAndSanitized(t *testing.T) {
	s, _ := newTestServer(t, nil)
	code, resp := do(t, s, "POST", "/api/alias", map[string]string{"alias": "Lisa Laptop!!"})
	if code != 200 {
		t.Fatalf("alias code %d (%v)", code, resp)
	}
	if resp["alias"] != "lisalaptop" {
		t.Errorf("alias not sanitized: %v", resp["alias"])
	}
	_, st := do(t, s, "GET", "/api/state", nil)
	if st["client_alias"] != "lisalaptop" {
		t.Errorf("alias not persisted in state: %v", st["client_alias"])
	}
}

func TestSetupServerSuccess(t *testing.T) {
	s, _ := newTestServer(t, &fakeProv{})
	do(t, s, "POST", "/api/alias", map[string]string{"alias": "lisa"})
	id := addHost(t, s)
	code, resp := do(t, s, "POST", "/api/hosts/"+id+"/setup-server", nil)
	if code != 200 {
		t.Fatalf("setup-server code %d (%v)", code, resp)
	}
	if resp["server_pubkey"] == "" || resp["authorized"] != true {
		t.Errorf("unexpected result: %v", resp)
	}
}

func TestSetupServerUnknownHost(t *testing.T) {
	s, _ := newTestServer(t, &fakeProv{})
	code, _ := do(t, s, "POST", "/api/hosts/nope/setup-server", nil)
	if code != 404 {
		t.Fatalf("expected 404, got %d", code)
	}
}

func TestLocalSSHD(t *testing.T) {
	s, _ := newTestServer(t, &fakeProv{})
	code, _ := do(t, s, "POST", "/api/local-sshd", map[string]bool{"disable_password": true})
	if code != 200 {
		t.Fatalf("local-sshd code %d", code)
	}
}

func TestNodesValidation(t *testing.T) {
	s, _ := newTestServer(t, nil)
	// invalid vless line rejected
	code, _ := do(t, s, "POST", "/api/nodes", map[string]string{"raw": "not-a-vless-url"})
	if code != 400 {
		t.Fatalf("expected 400 for bad node, got %d", code)
	}
	// comments + blanks accepted
	code, _ = do(t, s, "POST", "/api/nodes", map[string]string{"raw": "# just a comment\n\n"})
	if code != 200 {
		t.Fatalf("expected 200 for comments-only, got %d", code)
	}
}
