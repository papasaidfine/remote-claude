package webui

import (
	"bytes"
	"encoding/json"
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

type fakeMgr struct {
	mu      sync.Mutex
	started []string
	stopped []string
}

func (f *fakeMgr) Start(s bridge.Spec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = append(f.started, s.Alias)
	return nil
}
func (f *fakeMgr) Stop(a string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, a)
}
func (f *fakeMgr) Status(a string) bridge.Status {
	return bridge.Status{Alias: a, State: bridge.StateStopped}
}

type fakeProv struct{}

func (fakeProv) EnsureKey() error { return nil }
func (fakeProv) ServerBootstrap(alias, clientAlias string, port int, pw string) (provision.ServerResult, error) {
	return provision.ServerResult{Alias: clientAlias, Authorized: true, ServerPubKey: "k"}, nil
}
func (fakeProv) EnsureLocalSSHD(bool) error { return nil }

type fakePlat struct{}

func (fakePlat) Name() string            { return "TestOS" }
func (fakePlat) SupportsXray() bool      { return true }
func (fakePlat) StatusIncomingSSH() bool { return false }

func newTestServer(t *testing.T) (*Server, *fakeMgr) {
	t.Helper()
	dir := t.TempDir()
	ssh := filepath.Join(dir, ".ssh")
	p := paths.Paths{
		SSHDir:      ssh,
		SSHConfig:   filepath.Join(ssh, "config"),
		RCConfigDir: filepath.Join(dir, "rc"),
		VlessNodes:  filepath.Join(dir, "rc", "vless-nodes.txt"),
	}
	fm := &fakeMgr{}
	app := core.New(&store.Config{}, filepath.Join(dir, "config.json"), p, fm, fakeProv{}, fakePlat{})
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

// state returns the hosts array from /api/state.
func hosts(t *testing.T, s *Server) []any {
	t.Helper()
	_, resp := do(t, s, "GET", "/api/state", nil)
	hs, _ := resp["hosts"].([]any)
	return hs
}

func addHost(t *testing.T, s *Server, alias string) {
	t.Helper()
	code, resp := do(t, s, "POST", "/api/hosts", map[string]any{
		"alias": alias, "hostname": "srv.example.com", "user": "dev", "port": 22,
	})
	if code != 200 {
		t.Fatalf("add host: code %d resp %v", code, resp)
	}
}

func TestStateEmpty(t *testing.T) {
	s, _ := newTestServer(t)
	code, resp := do(t, s, "GET", "/api/state", nil)
	if code != 200 || resp["platform"] != "TestOS" {
		t.Fatalf("state code %d resp %v", code, resp)
	}
	if len(hosts(t, s)) != 0 {
		t.Errorf("expected no hosts")
	}
}

func TestAddRemoveHost(t *testing.T) {
	s, fm := newTestServer(t)
	addHost(t, s, "workbox")
	if len(hosts(t, s)) != 1 {
		t.Fatalf("host not listed")
	}
	code, _ := do(t, s, "DELETE", "/api/hosts/workbox", nil)
	if code != 200 {
		t.Fatalf("delete code %d", code)
	}
	if len(fm.stopped) != 1 || fm.stopped[0] != "workbox" {
		t.Errorf("tunnel not stopped on delete: %v", fm.stopped)
	}
	if len(hosts(t, s)) != 0 {
		t.Errorf("host not removed")
	}
}

func TestAddHostValidation(t *testing.T) {
	s, _ := newTestServer(t)
	code, _ := do(t, s, "POST", "/api/hosts", map[string]any{"alias": "", "hostname": "h"})
	if code != 400 {
		t.Fatalf("expected 400, got %d", code)
	}
}

func TestReverseAndProxyAreConfig(t *testing.T) {
	s, _ := newTestServer(t)
	addHost(t, s, "wb")
	if code, _ := do(t, s, "POST", "/api/hosts/wb/reverse", map[string]int{"port": 2222}); code != 200 {
		t.Fatalf("set reverse code %d", code)
	}
	if code, _ := do(t, s, "POST", "/api/hosts/wb/proxy", map[string]bool{"on": true}); code != 200 {
		t.Fatalf("set proxy code %d", code)
	}
	h := hosts(t, s)[0].(map[string]any)
	if h["has_reverse"] != true || h["reverse_port"] != float64(2222) || h["has_proxy"] != true {
		t.Errorf("reverse/proxy not reflected: %v", h)
	}
}

func TestSetParamAndList(t *testing.T) {
	s, _ := newTestServer(t)
	addHost(t, s, "wb")
	if code, _ := do(t, s, "POST", "/api/hosts/wb/param", map[string]string{"key": "ForwardAgent", "value": "no"}); code != 200 {
		t.Fatalf("set param code %d", code)
	}
	code, _ := do(t, s, "GET", "/api/hosts/wb/params", nil)
	if code != 200 {
		t.Fatalf("params code %d", code)
	}
}

func TestStartUnknownHost(t *testing.T) {
	s, _ := newTestServer(t)
	code, _ := do(t, s, "POST", "/api/hosts/nope/start", nil)
	if code != 404 {
		t.Fatalf("expected 404, got %d", code)
	}
}

func TestStartSuccess(t *testing.T) {
	s, fm := newTestServer(t)
	do(t, s, "POST", "/api/alias", map[string]string{"alias": "lisa"})
	addHost(t, s, "wb")
	do(t, s, "POST", "/api/hosts/wb/reverse", map[string]int{"port": 2222}) // required to start
	code, _ := do(t, s, "POST", "/api/hosts/wb/start", nil)
	if code != 200 {
		t.Fatalf("start code %d", code)
	}
	if len(fm.started) != 1 || fm.started[0] != "wb" {
		t.Errorf("bridge not started for alias: %v", fm.started)
	}
}

func TestSetupServer(t *testing.T) {
	s, _ := newTestServer(t)
	do(t, s, "POST", "/api/alias", map[string]string{"alias": "lisa"})
	addHost(t, s, "wb")
	do(t, s, "POST", "/api/hosts/wb/reverse", map[string]int{"port": 2223})
	code, resp := do(t, s, "POST", "/api/hosts/wb/setup-server", map[string]string{"password": ""})
	if code != 200 || resp["authorized"] != true {
		t.Fatalf("setup-server code %d resp %v", code, resp)
	}
}

func TestAliasSanitized(t *testing.T) {
	s, _ := newTestServer(t)
	code, resp := do(t, s, "POST", "/api/alias", map[string]string{"alias": "Lisa Laptop!!"})
	if code != 200 || resp["alias"] != "lisalaptop" {
		t.Fatalf("alias not sanitized: code %d resp %v", code, resp)
	}
}

func TestNodesValidation(t *testing.T) {
	s, _ := newTestServer(t)
	if code, _ := do(t, s, "POST", "/api/nodes", map[string]string{"raw": "garbage"}); code != 400 {
		t.Fatalf("expected 400 for bad node")
	}
	if code, _ := do(t, s, "POST", "/api/nodes", map[string]string{"raw": "# ok\n"}); code != 200 {
		t.Fatalf("expected 200 for comment")
	}
}
