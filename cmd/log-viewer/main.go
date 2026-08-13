package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Abdul-Rehman-DevOps/log-viewer/internal/admin"
	"github.com/Abdul-Rehman-DevOps/log-viewer/internal/auth"
	"github.com/Abdul-Rehman-DevOps/log-viewer/internal/config"
	"github.com/Abdul-Rehman-DevOps/log-viewer/internal/k8s"
	"github.com/Abdul-Rehman-DevOps/log-viewer/internal/syncer"
	"github.com/Abdul-Rehman-DevOps/log-viewer/internal/viewer"
)

var version = "0.5.4"

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("log-viewer ")

	var (
		listen     = flag.String("listen", envOr("LOG_VIEWER_LISTEN", ":8080"), "public listen address")
		dataDir    = flag.String("data", envOr("LOG_VIEWER_DATA", "/data"), "persistent data directory")
		configPath = flag.String("config", envOr("LOG_VIEWER_CONFIG", ""), "settings JSON path")
		assetsDir  = flag.String("assets", envOr("LOG_VIEWER_ASSETS", "/etc/log-viewer/branding"), "static branding assets")
	)
	flag.Parse()

	cfgPath := *configPath
	if cfgPath == "" {
		cfgPath = filepath.Join(*dataDir, "config.json")
	}

	log.Printf("starting v%s listen=%s data=%s config=%s", version, *listen, *dataDir, cfgPath)

	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		for _, seed := range []string{"/etc/log-viewer/seed.json", filepath.Join(*dataDir, "seed.json")} {
			if b, err := os.ReadFile(seed); err == nil {
				_ = os.MkdirAll(filepath.Dir(cfgPath), 0o755)
				_ = os.WriteFile(cfgPath, b, 0o644)
				log.Printf("seeded config from %s", seed)
				break
			}
		}
	}

	store, err := config.NewStore(cfgPath)
	if err != nil {
		log.Fatalf("config store: %v", err)
	}
	store.StartFileWatch(5 * time.Second)
	log.Printf("config loaded path=%s users=%d", cfgPath, len(store.Get().Users))

	cmSync, err := syncer.NewConfigMapSync(store)
	if err != nil {
		log.Printf("configmap sync disabled: %v", err)
	}
	bootCtx, bootCancel := context.WithTimeout(context.Background(), 20*time.Second)
	if cmSync != nil {
		if err := cmSync.Bootstrap(bootCtx); err != nil {
			log.Printf("configmap bootstrap: %v", err)
		} else {
			log.Printf("configmap sync enabled")
		}
		cmSync.StartWatch(context.Background())
	}
	bootCancel()

	kclient, err := k8s.New()
	if err != nil {
		log.Fatalf("kubernetes client: %v", err)
	}
	log.Printf("kubernetes client ready")

	sessions := auth.NewSessions(*dataDir)
	log.Printf("session epoch rotated (restart invalidates prior logins)")
	adm := &admin.Server{
		Store: store, K8s: kclient, Engine: nil, Sessions: sessions,
		Password: admin.AdminPasswordFromEnv(), Version: version,
	}
	if adm.Password == "" {
		log.Printf("warning: LOG_VIEWER_ADMIN_PASSWORD empty — admin auth disabled")
	} else {
		log.Printf("admin auth enabled")
	}
	view := &viewer.Server{Store: store, K8s: kclient, Sessions: sessions}

	mux := http.NewServeMux()
	adm.Routes(mux)
	view.Routes(mux)
	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir(*assetsDir))))

	srv := &http.Server{Addr: *listen, Handler: requestLog(mux)}
	go func() {
		log.Printf("listening on %s (idle session timeout %s)", *listen, auth.SessionTTL)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	sig := <-stop
	log.Printf("shutdown signal=%v", sig)
	shutdownCtx, c := context.WithTimeout(context.Background(), 10*time.Second)
	defer c()
	_ = srv.Shutdown(shutdownCtx)
	log.Printf("stopped")
}

type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.code = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if shouldSkipLog(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(rec, r)
		log.Printf("%s %s %d %s", r.Method, r.URL.RequestURI(), rec.code, time.Since(start).Round(time.Millisecond))
	})
}

func shouldSkipLog(path string) bool {
	if strings.HasPrefix(path, "/assets/") {
		return true
	}
	if path == "/viewer/app.js" || path == "/admin/app.js" {
		return true
	}
	return false
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
