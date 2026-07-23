package admin

import (
	_ "embed"
	"net/http"
)

//go:embed ui.html
var uiHTML []byte

//go:embed app.js
var appJS []byte

func (s *Server) handleUI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(uiHTML)
}

func (s *Server) handleAdminJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	_, _ = w.Write(appJS)
}
