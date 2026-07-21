// Package webui is the local web front-end: a background daemon that serves a
// simple settings page on 127.0.0.1 and exposes a small JSON API over the
// engine (store + bridge + provision). It is one front-end over a front-end-
// agnostic engine; a native tray shell could replace it without touching the
// engine.
package webui

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"sync"

	"github.com/papasaidfine/remote-claude/internal/bridge"
	"github.com/papasaidfine/remote-claude/internal/nodes"
	"github.com/papasaidfine/remote-claude/internal/paths"
	"github.com/papasaidfine/remote-claude/internal/provision"
	"github.com/papasaidfine/remote-claude/internal/store"
	"github.com/papasaidfine/remote-claude/internal/vless"
)

//go:embed static
var staticFS embed.FS

// Provisioner performs the side-effecting setup the UI drives: client setup
// before a tunnel starts, one-shot server bootstrap over the connection, and
// local sshd. Injected so the server stays testable; may be nil.
type Provisioner interface {
	EnsureClient(h store.Host, clientAlias string) error
	ServerBootstrap(h store.Host, clientAlias string) (provision.ServerResult, error)
	EnsureLocalSSHD(disablePassword bool) error
}

// TunnelManager is the subset of *bridge.Manager the server drives (seam for
// tests).
type TunnelManager interface {
	Start(spec bridge.Spec) error
	Stop(hostID string)
	Status(hostID string) bridge.Status
}

// PlatformInfo is the read-only platform surface the UI reports.
type PlatformInfo interface {
	Name() string
	SupportsXray() bool
	StatusIncomingSSH() bool
}

// Server holds the live app state.
type Server struct {
	mu      sync.Mutex
	cfg     *store.Config
	cfgPath string

	paths paths.Paths
	mgr   TunnelManager
	prov  Provisioner
	plat  PlatformInfo
}

// New builds a Server around an already-loaded config.
func New(cfg *store.Config, cfgPath string, p paths.Paths, mgr TunnelManager, prov Provisioner, plat PlatformInfo) *Server {
	cfg.Normalize()
	return &Server{cfg: cfg, cfgPath: cfgPath, paths: p, mgr: mgr, prov: prov, plat: plat}
}

// Handler returns the http.Handler for the app (API + static SPA).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/state", s.handleState)
	mux.HandleFunc("POST /api/alias", s.handleSetAlias)
	mux.HandleFunc("POST /api/hosts", s.handleAddHost)
	mux.HandleFunc("PUT /api/hosts/{id}", s.handleUpdateHost)
	mux.HandleFunc("DELETE /api/hosts/{id}", s.handleDeleteHost)
	mux.HandleFunc("POST /api/hosts/{id}/start", s.handleStart)
	mux.HandleFunc("POST /api/hosts/{id}/stop", s.handleStop)
	mux.HandleFunc("POST /api/hosts/{id}/setup-server", s.handleSetupServer)
	mux.HandleFunc("GET /api/nodes", s.handleGetNodes)
	mux.HandleFunc("POST /api/nodes", s.handleSetNodes)
	mux.HandleFunc("POST /api/local-sshd", s.handleLocalSSHD)

	sub, _ := fs.Sub(staticFS, "static")
	fileServer := http.FileServerFS(sub)
	mux.Handle("GET /static/", http.StripPrefix("/static/", fileServer))
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		b, _ := staticFS.ReadFile("static/index.html")
		w.Write(b)
	})
	return mux
}

// ---- state ----

type stateResp struct {
	ClientAlias   string                   `json:"client_alias"`
	Hosts         []store.Host             `json:"hosts"`
	Statuses      map[string]bridge.Status `json:"statuses"`
	Platform      string                   `json:"platform"`
	XraySupported bool                     `json:"xray_supported"`
	NodeCount     int                      `json:"node_count"`
	LocalSSHOK    bool                     `json:"local_ssh_ok"`
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	resp := stateResp{
		ClientAlias:   s.cfg.ClientAlias,
		Hosts:         append([]store.Host(nil), s.cfg.Hosts...),
		Statuses:      map[string]bridge.Status{},
		Platform:      s.plat.Name(),
		XraySupported: s.plat.SupportsXray(),
		NodeCount:     nodes.Count(s.paths.VlessNodes),
		LocalSSHOK:    s.plat.StatusIncomingSSH(),
	}
	for _, h := range s.cfg.Hosts {
		resp.Statuses[h.ID] = s.mgr.Status(h.ID)
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, resp)
}

// ---- alias ----

func (s *Server) handleSetAlias(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Alias string `json:"alias"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	alias := sanitizeAlias(body.Alias)
	if alias == "" {
		httpErr(w, http.StatusBadRequest, "alias must be non-empty (letters, digits, -, _)")
		return
	}
	s.mu.Lock()
	s.cfg.ClientAlias = alias
	err := s.save()
	s.mu.Unlock()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"alias": alias})
}

// ---- host CRUD ----

func (s *Server) handleAddHost(w http.ResponseWriter, r *http.Request) {
	var h store.Host
	if !readJSON(w, r, &h) {
		return
	}
	s.mu.Lock()
	stored, err := s.cfg.Add(h)
	if err == nil {
		err = s.save()
	}
	s.mu.Unlock()
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stored)
}

func (s *Server) handleUpdateHost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var h store.Host
	if !readJSON(w, r, &h) {
		return
	}
	h.ID = id
	s.mu.Lock()
	err := s.cfg.Update(h)
	if err == nil {
		err = s.save()
	}
	s.mu.Unlock()
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, h)
}

func (s *Server) handleDeleteHost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.mgr.Stop(id)
	s.mu.Lock()
	ok := s.cfg.Remove(id)
	var err error
	if ok {
		err = s.save()
	}
	s.mu.Unlock()
	if !ok {
		httpErr(w, http.StatusNotFound, "no such host")
		return
	}
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---- tunnel start/stop ----

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.mu.Lock()
	h := s.cfg.Find(id)
	var host store.Host
	if h != nil {
		host = *h
	}
	alias := s.cfg.ClientAlias
	s.mu.Unlock()
	if h == nil {
		httpErr(w, http.StatusNotFound, "no such host")
		return
	}
	if s.prov != nil {
		if err := s.prov.EnsureClient(host, alias); err != nil {
			httpErr(w, http.StatusInternalServerError, "client setup failed: "+err.Error())
			return
		}
	}
	spec := bridge.Spec{HostID: host.ID, Alias: paths.Alias, ReversePort: host.ReversePort}
	if err := s.mgr.Start(spec); err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.mgr.Status(host.ID))
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.mgr.Stop(id)
	writeJSON(w, http.StatusOK, s.mgr.Status(id))
}

// handleSetupServer configures the server side over the outbound connection and
// authorizes the returned connect-back key locally.
func (s *Server) handleSetupServer(w http.ResponseWriter, r *http.Request) {
	if s.prov == nil {
		httpErr(w, http.StatusNotImplemented, "provisioning unavailable")
		return
	}
	id := r.PathValue("id")
	s.mu.Lock()
	h := s.cfg.Find(id)
	var host store.Host
	if h != nil {
		host = *h
	}
	alias := s.cfg.ClientAlias
	s.mu.Unlock()
	if h == nil {
		httpErr(w, http.StatusNotFound, "no such host")
		return
	}
	res, err := s.prov.ServerBootstrap(host, alias)
	if err != nil {
		httpErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleLocalSSHD installs/ensures the local ssh server (may need sudo).
func (s *Server) handleLocalSSHD(w http.ResponseWriter, r *http.Request) {
	if s.prov == nil {
		httpErr(w, http.StatusNotImplemented, "provisioning unavailable")
		return
	}
	var body struct {
		DisablePassword bool `json:"disable_password"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if err := s.prov.EnsureLocalSSHD(body.DisablePassword); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true, "running": s.plat.StatusIncomingSSH()})
}

// ---- xray nodes (raw text of vless-nodes.txt) ----

func (s *Server) handleGetNodes(w http.ResponseWriter, r *http.Request) {
	raw := ""
	if b, err := readFileString(s.paths.VlessNodes); err == nil {
		raw = b
	}
	writeJSON(w, http.StatusOK, map[string]any{"raw": raw, "count": nodes.Count(s.paths.VlessNodes)})
}

func (s *Server) handleSetNodes(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Raw string `json:"raw"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	// Validate every non-comment, non-blank line.
	for i, line := range strings.Split(body.Raw, "\n") {
		l := strings.TrimSpace(line)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		if err := vless.Validate(l); err != nil {
			httpErr(w, http.StatusBadRequest, fmt.Sprintf("line %d: %v", i+1, err))
			return
		}
	}
	if err := writeNodesFile(s.paths, body.Raw); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"count": nodes.Count(s.paths.VlessNodes)})
}

// ---- helpers ----

// save persists cfg; caller holds s.mu.
func (s *Server) save() error { return store.Save(s.cfgPath, s.cfg) }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		httpErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return false
	}
	return true
}

func httpErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// sanitizeAlias keeps letters, digits, dash, underscore; lowercases the rest.
func sanitizeAlias(in string) string {
	in = strings.TrimSpace(in)
	var b strings.Builder
	for _, r := range in {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + 32)
		}
	}
	return b.String()
}
