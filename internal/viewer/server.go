package viewer

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Abdul-Rehman-DevOps/log-viewer/internal/auth"
	"github.com/Abdul-Rehman-DevOps/log-viewer/internal/config"
	"github.com/Abdul-Rehman-DevOps/log-viewer/internal/k8s"
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
	mux.HandleFunc("/api/unhealthy-pods", s.requireUser(s.handleUnhealthyPods))
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
		sess, st := s.Sessions.Lookup(id)
		if st != auth.StatusOK {
			if st == auth.StatusExpired || st == auth.StatusRestarted {
				log.Printf("viewer session %s user=%s path=%s", statusName(st), sess.Username, r.URL.Path)
				auth.ClearCookie(w, auth.UserCookie)
			}
			auth.WriteUnauthorized(w, st)
			return
		}
		auth.SlideCookie(w, s.Sessions, auth.UserCookie, sess)
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
	sess, st := s.Sessions.Lookup(id)
	if st != auth.StatusOK {
		if st == auth.StatusExpired || st == auth.StatusRestarted {
			log.Printf("viewer session %s (page) user=%s", statusName(st), sess.Username)
			auth.ClearCookie(w, auth.UserCookie)
			reason := "timeout"
			if st == auth.StatusRestarted {
				reason = "restart"
			}
			http.Redirect(w, r, "/login?reason="+reason, http.StatusFound)
			return
		}
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	auth.SlideCookie(w, s.Sessions, auth.UserCookie, sess)
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
		log.Printf("viewer login failed user=%q", body.Username)
		http.Error(w, `{"error":"invalid credentials"}`, http.StatusUnauthorized)
		return
	}
	id, err := s.Sessions.Create(body.Username, "user", auth.SessionTTL)
	if err != nil {
		log.Printf("viewer login session error user=%q: %v", body.Username, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	auth.SetCookie(w, auth.UserCookie, id, auth.CookieMaxAge())
	log.Printf("viewer login ok user=%q idle_timeout=%s", body.Username, auth.SessionTTL)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "username": body.Username, "expiresIn": int(auth.SessionTTL.Seconds())})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	auth.ClearCookie(w, auth.UserCookie)
	log.Printf("viewer logout")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	cfg := s.Store.Get()
	needsSetup := s.Store.NeedsSetup()
	resp := map[string]any{
		"authEnabled":   !needsSetup,
		"needsSetup":    needsSetup,
		"title":         cfg.Title,
		"product":       "Log Viewer",
		"sessionTTL":    int(auth.SessionTTL.Seconds()),
		"authenticated": false,
		"authorName":    cfg.AuthorName,
		"githubUrl":     cfg.GitHubURL,
		"portfolioUrl":  cfg.PortfolioURL,
		"repoUrl":       cfg.RepoURL,
		"version":       "0.5.4",
	}
	if !needsSetup {
		id := auth.CookieValue(r, auth.UserCookie)
		sess, st := s.Sessions.Lookup(id)
		if st == auth.StatusOK {
			auth.SlideCookie(w, s.Sessions, auth.UserCookie, sess)
			resp["username"] = sess.Username
			resp["authenticated"] = true
			resp["expiresAt"] = time.Now().Add(auth.SessionTTL).Unix()
		} else if st == auth.StatusExpired {
			auth.ClearCookie(w, auth.UserCookie)
			resp["error"] = "session_timeout"
		} else if st == auth.StatusRestarted {
			auth.ClearCookie(w, auth.UserCookie)
			resp["error"] = "session_restarted"
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
		log.Printf("workloads: list namespaces: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	ns := config.ResolveNamespaces(cfg, all)
	workloads, err := s.K8s.ListWorkloads(ctx, cfg, ns)
	if err != nil {
		log.Printf("workloads: list: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("workloads: returned=%d namespaces=%d", len(workloads), len(ns))
	writeJSON(w, http.StatusOK, map[string]any{
		"workloads":  workloads,
		"namespaces": ns,
		"config": map[string]any{
			"title":        cfg.Title,
			"workloads":    cfg.Workloads,
			"authorName":   cfg.AuthorName,
			"githubUrl":    cfg.GitHubURL,
			"portfolioUrl": cfg.PortfolioURL,
			"repoUrl":      cfg.RepoURL,
		},
	})
}

func (s *Server) handleUnhealthyPods(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	cfg := s.Store.Get()
	all, err := s.K8s.ListNamespaces(ctx)
	if err != nil {
		log.Printf("unhealthy-pods: list namespaces: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	ns := config.ResolveNamespaces(cfg, all)
	pods, err := s.K8s.ListUnhealthyPods(ctx, cfg, ns)
	if err != nil {
		log.Printf("unhealthy-pods: list: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("unhealthy-pods: returned=%d", len(pods))
	writeJSON(w, http.StatusOK, map[string]any{
		"pods":  pods,
		"count": len(pods),
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
		log.Printf("containers ns=%s pod=%s: %v", ns, pod, err)
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
	kind := r.URL.Query().Get("kind")
	workload := r.URL.Query().Get("workload")
	previous := r.URL.Query().Get("previous") == "1" || strings.EqualFold(r.URL.Query().Get("previous"), "true")
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

	// Enforce Admin allowlist: every pod must belong to an allowed workload.
	ctxCheck, cancelCheck := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancelCheck()
	for _, p := range pods {
		wKind, wName := kind, workload
		if wKind == "" || wName == "" {
			resolvedKind, resolvedName, err := s.K8s.ResolvePodWorkload(ctxCheck, ns, p)
			if err != nil {
				http.Error(w, "pod not found or not accessible", http.StatusForbidden)
				return
			}
			wKind, wName = resolvedKind, resolvedName
		}
		if wKind == "" || wName == "" || !config.WorkloadAllowed(cfg, ns, wKind, wName) {
			http.Error(w, "workload not allowed", http.StatusForbidden)
			return
		}
	}

	log.Printf("logs stream start ns=%s pods=%d container=%q tail=%d previous=%v", ns, len(pods), container, tail, previous)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	err := s.K8s.StreamPodsLogs(r.Context(), ns, pods, container, tail, previous, w)
	if err != nil && r.Context().Err() == nil {
		log.Printf("logs stream error ns=%s: %v", ns, err)
	} else {
		log.Printf("logs stream end ns=%s", ns)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func statusName(st auth.Status) string {
	switch st {
	case auth.StatusExpired:
		return "timeout"
	case auth.StatusRestarted:
		return "restarted"
	case auth.StatusInvalid:
		return "invalid"
	default:
		return "unauthorized"
	}
}
