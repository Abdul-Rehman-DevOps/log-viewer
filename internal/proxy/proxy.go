package proxy

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

// Dozzle returns a reverse proxy to the local Dozzle process.
func Dozzle(target string) http.Handler {
	u, err := url.Parse(target)
	if err != nil {
		panic(err)
	}
	p := httputil.NewSingleHostReverseProxy(u)
	p.FlushInterval = 100 * time.Millisecond
	orig := p.Director
	p.Director = func(r *http.Request) {
		orig(r)
		r.Host = u.Host
	}
	p.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, "log engine starting — configure namespaces in /admin if this persists: "+err.Error(), http.StatusBadGateway)
	}
	return p
}
