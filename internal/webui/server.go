// Package webui is the local web front-end: a background daemon that serves a
// simple settings page on 127.0.0.1 and exposes a small JSON API. It is a thin
// HTTP adapter over the front-end-agnostic core.App — handlers only decode
// JSON, call the façade, and map error kinds to status codes. A native shell
// (cmd/remote-claude-gui) calls the same core methods.
package webui

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"

	"github.com/papasaidfine/remote-claude/internal/core"
)

//go:embed static
var staticFS embed.FS

// Server serves the app UI and JSON API over a core.App.
type Server struct {
	app *core.App
}

// New builds a Server over the app façade.
func New(app *core.App) *Server { return &Server{app: app} }

// Handler returns the http.Handler for the app (API + static SPA).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/state", s.handleState)
	mux.HandleFunc("POST /api/alias", s.handleSetAlias)
	mux.HandleFunc("POST /api/hosts", s.handleAddHost)
	mux.HandleFunc("DELETE /api/hosts/{alias}", s.handleRemoveHost)
	mux.HandleFunc("GET /api/hosts/{alias}/params", s.handleParams)
	mux.HandleFunc("POST /api/hosts/{alias}/param", s.handleSetParam)
	mux.HandleFunc("POST /api/hosts/{alias}/reverse", s.handleReverse)
	mux.HandleFunc("POST /api/hosts/{alias}/proxy", s.handleProxy)
	mux.HandleFunc("POST /api/hosts/{alias}/autostart", s.handleAutoStart)
	mux.HandleFunc("POST /api/hosts/{alias}/start", s.handleStart)
	mux.HandleFunc("POST /api/hosts/{alias}/stop", s.handleStop)
	mux.HandleFunc("POST /api/hosts/{alias}/setup-server", s.handleSetupServer)
	mux.HandleFunc("GET /api/hosts/{alias}/usage", s.handleUsage)
	mux.HandleFunc("GET /api/nodes", s.handleGetNodes)
	mux.HandleFunc("POST /api/nodes", s.handleSetNodes)
	mux.HandleFunc("POST /api/xray/install", s.handleInstallXray)
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

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.app.State())
}

func (s *Server) handleSetAlias(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Alias string `json:"alias"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	alias, err := s.app.SetAlias(body.Alias)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"alias": alias})
}

func (s *Server) handleAddHost(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Alias    string `json:"alias"`
		HostName string `json:"hostname"`
		User     string `json:"user"`
		Port     int    `json:"port"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if err := s.app.AddHost(body.Alias, body.HostName, body.User, body.Port); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleRemoveHost(w http.ResponseWriter, r *http.Request) {
	if err := s.app.RemoveHost(r.PathValue("alias")); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleParams(w http.ResponseWriter, r *http.Request) {
	params, err := s.app.HostParams(r.PathValue("alias"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, params)
}

func (s *Server) handleSetParam(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if err := s.app.SetParam(r.PathValue("alias"), body.Key, body.Value); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleReverse(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Port int `json:"port"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if err := s.app.SetReverseTunnel(r.PathValue("alias"), body.Port); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleProxy(w http.ResponseWriter, r *http.Request) {
	var body struct {
		On bool `json:"on"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if err := s.app.SetProxy(r.PathValue("alias"), body.On); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleAutoStart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		On bool `json:"on"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if err := s.app.SetAutoStart(r.PathValue("alias"), body.On); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	st, err := s.app.StartTunnel(r.PathValue("alias"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.app.StopTunnel(r.PathValue("alias")))
}

func (s *Server) handleSetupServer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	json.NewDecoder(r.Body).Decode(&body) // body optional
	res, err := s.app.SetupServer(r.PathValue("alias"), body.Password)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleLocalSSHD(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DisablePassword bool `json:"disable_password"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	running, err := s.app.EnsureLocalSSHD(body.DisablePassword)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true, "running": running})
}

func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	rep, err := s.app.HostUsage(r.PathValue("alias"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (s *Server) handleInstallXray(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Proxy string `json:"proxy"` // optional one-shot download proxy, not persisted
	}
	json.NewDecoder(r.Body).Decode(&body)
	if err := s.app.InstallXray(body.Proxy); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleGetNodes(w http.ResponseWriter, r *http.Request) {
	raw, count := s.app.Nodes()
	writeJSON(w, http.StatusOK, map[string]any{"raw": raw, "count": count})
}

func (s *Server) handleSetNodes(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Raw string `json:"raw"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	count, err := s.app.SetNodes(body.Raw)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"count": count})
}

// ---- helpers ----

func fail(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	switch core.KindOf(err) {
	case core.ErrInvalid:
		code = http.StatusBadRequest
	case core.ErrNotFound:
		code = http.StatusNotFound
	case core.ErrUnavailable:
		code = http.StatusNotImplemented
	case core.ErrRemote:
		code = http.StatusBadGateway
	}
	httpErr(w, code, err.Error())
}

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
