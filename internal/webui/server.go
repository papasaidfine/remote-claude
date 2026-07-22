// Package webui is the local web front-end: a background daemon that serves a
// simple settings page on 127.0.0.1 and exposes a small JSON API. It is a thin
// HTTP adapter over the front-end-agnostic core.App — handlers only decode
// JSON, call the façade, and map error kinds to status codes. A native shell
// can replace it by calling the same core methods.
package webui

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"

	"github.com/papasaidfine/remote-claude/internal/core"
	"github.com/papasaidfine/remote-claude/internal/store"
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
	var h store.Host
	if !readJSON(w, r, &h) {
		return
	}
	stored, err := s.app.AddHost(h)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, stored)
}

func (s *Server) handleUpdateHost(w http.ResponseWriter, r *http.Request) {
	var h store.Host
	if !readJSON(w, r, &h) {
		return
	}
	h.ID = r.PathValue("id")
	updated, err := s.app.UpdateHost(h)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeleteHost(w http.ResponseWriter, r *http.Request) {
	if err := s.app.DeleteHost(r.PathValue("id")); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	st, err := s.app.StartTunnel(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.app.StopTunnel(r.PathValue("id")))
}

func (s *Server) handleSetupServer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	// Body is optional (no password → key/agent auth).
	json.NewDecoder(r.Body).Decode(&body)
	res, err := s.app.SetupServer(r.PathValue("id"), body.Password)
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

// fail translates a core error kind into an HTTP status.
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
