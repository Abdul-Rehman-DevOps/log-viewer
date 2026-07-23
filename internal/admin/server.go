package admin

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Abdul-Rehman-DevOps/log-viewer/internal/auth"
	"github.com/Abdul-Rehman-DevOps/log-viewer/internal/config"
	"github.com/Abdul-Rehman-DevOps/log-viewer/internal/engine"
	"github.com/Abdul-Rehman-DevOps/log-viewer/internal/k8s"
)

type Server struct {
	Store    *config.Store
	K8s      *k8s.Client
	Engine   *engine.Manager
	Sessions *auth.Sessions
	Password string
	Version  string
}

func (s *Server) Routes(mux *http.ServeMux) {
	mux.HandleFunc("/admin", s.handleUI)
	mux.HandleFunc("/admin/", s.handleUI)
	mux.HandleFunc("/admin/app.js", s.handleAdminJS)
	mux.HandleFunc("/api/admin/login", s.handleLogin)
	mux.HandleFunc("/api/admin/logout", s.handleLogout)
	mux.HandleFunc("/api/admin/config", s.auth(s.handleConfig))
	mux.HandleFunc("/api/admin/namespaces", s.auth(s.handleNamespaces))
	mux.HandleFunc("/api/admin/cluster-workloads", s.auth(s.handleClusterWorkloads))
	mux.HandleFunc("/api/admin/apply", s.auth(s.handleApply))
	mux.HandleFunc("/api/admin/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true, "version": s.Version, "product": "Log Viewer", "author": "Abdul Rehman",
		})
	})
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Password == "" {
			next(w, r)
			return
		}
		id := auth.CookieValue(r, auth.AdminCookie)
		sess, st := s.Sessions.Lookup(id)
		if st == auth.StatusOK && sess.Role == "admin" {
			auth.SlideCookie(w, s.Sessions, auth.AdminCookie, sess)
			next(w, r)
			return
		}
		if st == auth.StatusExpired {
			log.Printf("admin session timeout path=%s", r.URL.Path)
			auth.ClearCookie(w, auth.AdminCookie)
			auth.WriteUnauthorized(w, auth.StatusExpired)
			return
		}
		// legacy bearer = raw password (compat)
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(s.Password)) == 1 {
			next(w, r)
			return
		}
		auth.WriteUnauthorized(w, st)
	}
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if s.Password != "" && subtle.ConstantTimeCompare([]byte(body.Password), []byte(s.Password)) != 1 {
		log.Printf("admin login failed")
		http.Error(w, `{"error":"invalid password"}`, http.StatusUnauthorized)
		return
	}
	id, err := s.Sessions.Create("admin", "admin", auth.SessionTTL)
	if err != nil {
		log.Printf("admin login session error: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	auth.SetCookie(w, auth.AdminCookie, id, auth.CookieMaxAge())
	log.Printf("admin login ok idle_timeout=%s", auth.SessionTTL)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "expiresIn": int(auth.SessionTTL.Seconds())})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	id := auth.CookieValue(r, auth.AdminCookie)
	s.Sessions.Delete(id)
	auth.ClearCookie(w, auth.AdminCookie)
	log.Printf("admin logout")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.Store.GetForAdmin())
	case http.MethodPut:
		var next config.Settings
		if err := json.NewDecoder(r.Body).Decode(&next); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := s.Store.Update(next); err != nil {
			log.Printf("admin config update rejected: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		log.Printf("admin config saved mode=%s users=%d allowedApps=%d", next.Mode, len(next.Users), len(next.AllowedWorkloads))
		// Realtime: refresh engine namespace scope immediately
		if err := s.applyCurrent(r.Context()); err != nil {
			log.Printf("admin config apply warning: %v", err)
			// still saved — report warning but return 200 with note
			writeJSON(w, http.StatusOK, map[string]any{
				"config":  s.Store.GetForAdmin(),
				"warning": err.Error(),
				"applied": false,
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"config":  s.Store.GetForAdmin(),
			"applied": true,
		})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleNamespaces(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	all, err := s.K8s.ListNamespaces(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	cfg := s.Store.GetForAdmin()
	active := config.ResolveNamespaces(s.Store.Get(), all)
	writeJSON(w, http.StatusOK, map[string]any{"all": all, "active": active, "config": cfg})
}

func (s *Server) handleClusterWorkloads(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	ns := r.URL.Query().Get("namespace")
	cfg := s.Store.Get()
	all, err := s.K8s.ListNamespaces(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	namespaces := config.ResolveNamespaces(cfg, all)
	if ns != "" {
		namespaces = []string{ns}
	}
	workloads, err := s.K8s.ListWorkloadsRaw(ctx, cfg, namespaces)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"workloads": workloads,
		"allowed":   cfg.AllowedWorkloads,
	})
}

func (s *Server) handleApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := s.applyCurrent(r.Context()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "applied": true})
}

func (s *Server) ApplyOnStart(ctx context.Context) error {
	return s.applyCurrent(ctx)
}

func (s *Server) applyCurrent(ctx context.Context) error {
	cfg := s.Store.Get()
	all, err := s.K8s.ListNamespaces(ctx)
	if err != nil {
		return err
	}
	ns := config.ResolveNamespaces(cfg, all)
	if len(ns) == 0 {
		return nil
	}
	if s.Engine != nil {
		return s.Engine.Apply(cfg, ns)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func AdminPasswordFromEnv() string {
	return os.Getenv("LOG_VIEWER_ADMIN_PASSWORD")
}
