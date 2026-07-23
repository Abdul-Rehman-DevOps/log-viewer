package viewer

import (
	_ "embed"
	"net/http"
)

//go:embed viewer.html
var viewerHTML []byte

//go:embed login.html
var loginHTML []byte

//go:embed setup.html
var setupHTML []byte

//go:embed app.js
var appJS []byte

func (s *Server) handleViewerJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	_, _ = w.Write(appJS)
}
