package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/AIVMNetwork/log-viewer/internal/admin"
	"github.com/AIVMNetwork/log-viewer/internal/auth"
	"github.com/AIVMNetwork/log-viewer/internal/config"
	"github.com/AIVMNetwork/log-viewer/internal/k8s"
	"github.com/AIVMNetwork/log-viewer/internal/syncer"
	"github.com/AIVMNetwork/log-viewer/internal/viewer"
)

var version = "0.4.0"

func main() {
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

	cmSync, err := syncer.NewConfigMapSync(store)
	if err != nil {
		log.Printf("configmap sync disabled: %v", err)
	}
	bootCtx, bootCancel := context.WithTimeout(context.Background(), 20*time.Second)
	if cmSync != nil {
		if err := cmSync.Bootstrap(bootCtx); err != nil {
			log.Printf("configmap bootstrap: %v", err)
		}
		cmSync.StartWatch(context.Background())
	}
	bootCancel()

	kclient, err := k8s.New()
	if err != nil {
		log.Fatalf("kubernetes client: %v", err)
	}

	sessions := auth.NewSessions()
	adm := &admin.Server{
		Store: store, K8s: kclient, Engine: nil, Sessions: sessions,
		Password: admin.AdminPasswordFromEnv(), Version: version,
	}
	view := &viewer.Server{Store: store, K8s: kclient, Sessions: sessions}

	mux := http.NewServeMux()
	adm.Routes(mux)
	view.Routes(mux)
	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir(*assetsDir))))

	srv := &http.Server{Addr: *listen, Handler: mux}
	go func() {
		log.Printf("Log Viewer v%s on %s (session TTL 1h)", version, *listen)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	log.Printf("shutting down…")
	shutdownCtx, c := context.WithTimeout(context.Background(), 10*time.Second)
	defer c()
	_ = srv.Shutdown(shutdownCtx)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
