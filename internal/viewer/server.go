package viewer

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/AIVMNetwork/log-viewer/internal/auth"
	"github.com/AIVMNetwork/log-viewer/internal/config"
	"github.com/AIVMNetwork/log-viewer/internal/k8s"
)

type Server struct {
	Store    *config.Store
	K8s      *k8s.Client
	Sessions *auth.Sessions
}

func (s *Server) Routes(mux *http.ServeMux) {
	mux.HandleFunc("/", s.handleHome)
	mux.HandleFunc("/login", s.handleLoginPage)
	mux.HandleFunc("/setup", s.handleSetupPage)
	mux.HandleFunc("/viewer/app.js", s.handleViewerJS)
	mux.HandleFunc("/api/login", s.handleLogin)
	mux.HandleFunc("/api/logout", s.handleLogout)
	mux.HandleFunc("/api/me", s.handleMe)
	mux.HandleFunc("/api/workloads", s.requireUser(s.handleWorkloads))
	mux.HandleFunc("/api/logs", s.requireUser(s.handleLogs))
	mux.HandleFunc("/api/pods/containers", s.requireUser(s.handleContainers))
}

func (s *Server) requireUser(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Store.NeedsSetup() {
			http.Error(w, `{"error":"setup_required"}`, http.StatusForbidden)
			return
		}
		id := auth.CookieValue(r, auth.UserCookie)
		if _, ok := s.Sessions.Get(id); !ok {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if s.Store.NeedsSetup() {
		http.Redirect(w, r, "/setup", http.StatusFound)
		return
	}
	id := auth.CookieValue(r, auth.UserCookie)
	if _, ok := s.Sessions.Get(id); !ok {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(viewerHTML)
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if s.Store.NeedsSetup() {
		http.Redirect(w, r, "/setup", http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(loginHTML)
}

func (s *Server) handleSetupPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(setupHTML)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.Store.NeedsSetup() {
		http.Error(w, `{"error":"setup_required"}`, http.StatusForbidden)
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if !s.Store.VerifyUser(body.Username, body.Password) {
		http.Error(w, `{"error":"invalid credentials"}`, http.StatusUnauthorized)
		return
	}
	id, err := s.Sessions.Create(body.Username, "user", auth.SessionTTL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	auth.SetCookie(w, auth.UserCookie, id, auth.CookieMaxAge())
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "username": body.Username, "expiresIn": int(auth.SessionTTL.Seconds())})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	auth.ClearCookie(w, auth.UserCookie)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	cfg := s.Store.Get()
	needsSetup := s.Store.NeedsSetup()
	resp := map[string]any{
		"authEnabled":  !needsSetup,
		"needsSetup":   needsSetup,
		"title":        cfg.Title,
		"product":      "Log Viewer",
		"sessionTTL":   int(auth.SessionTTL.Seconds()),
		"authenticated": false,
	}
	if !needsSetup {
		id := auth.CookieValue(r, auth.UserCookie)
		if sess, ok := s.Sessions.Get(id); ok {
			resp["username"] = sess.Username
			resp["authenticated"] = true
			resp["expiresAt"] = sess.Exp
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleWorkloads(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	cfg := s.Store.Get()
	all, err := s.K8s.ListNamespaces(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	ns := config.ResolveNamespaces(cfg, all)
	workloads, err := s.K8s.ListWorkloads(ctx, cfg, ns)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"workloads":  workloads,
		"namespaces": ns,
		"config": map[string]any{
			"title":     cfg.Title,
			"workloads": cfg.Workloads,
		},
	})
}

func (s *Server) handleContainers(w http.ResponseWriter, r *http.Request) {
	ns := r.URL.Query().Get("namespace")
	pod := r.URL.Query().Get("pod")
	if ns == "" || pod == "" {
		http.Error(w, "namespace and pod required", http.StatusBadRequest)
		return
	}
	cfg := s.Store.Get()
	if !config.NamespaceAllowed(cfg, ns) {
		http.Error(w, "namespace not allowed", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	names, err := s.K8s.PodContainers(ctx, ns, pod)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"containers": names})
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	ns := r.URL.Query().Get("namespace")
	pod := r.URL.Query().Get("pod")
	podsParam := r.URL.Query().Get("pods")
	container := r.URL.Query().Get("container")
	tail, _ := strconv.ParseInt(r.URL.Query().Get("tail"), 10, 64)
	if tail <= 0 {
		tail = 100
	}
	if tail > 500 {
		tail = 500
	}
	if ns == "" {
		http.Error(w, "namespace required", http.StatusBadRequest)
		return
	}
	cfg := s.Store.Get()
	if !config.NamespaceAllowed(cfg, ns) {
		http.Error(w, "namespace not allowed", http.StatusForbidden)
		return
	}

	var pods []string
	switch {
	case podsParam != "":
		for _, p := range strings.Split(podsParam, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				pods = append(pods, p)
			}
		}
	case pod != "" && pod != "__all__":
		pods = []string{pod}
	default:
		http.Error(w, "pod or pods required", http.StatusBadRequest)
		return
	}
	if len(pods) == 0 {
		http.Error(w, "no pods selected", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	_ = s.K8s.StreamPodsLogs(r.Context(), ns, pods, container, tail, w)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
