// Package httpx provides the HTTP server (timeouts, graceful shutdown, optional
// autocert TLS) and shared middleware (security headers, rate limiting, panic
// recovery, scrubbed request logging).
package httpx

import (
	"context"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/instantaiguru/csat/internal/config"
	"golang.org/x/crypto/acme/autocert"
)

// Middleware wraps an http.Handler.
type Middleware func(http.Handler) http.Handler

// Chain applies middlewares in order (the first listed is outermost).
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// Recover converts panics into a generic 500.
func Recover() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if v := recover(); v != nil {
					log.Printf("panic: %v (%s %s)", v, r.Method, r.URL.Path)
					http.Error(w, "internal error", http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// Logger logs method, path, status, and duration. Query strings are never
// logged (the survey/invite tokens are sensitive capabilities).
func Logger() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sw, r)
			log.Printf("%s %s -> %d (%s)", r.Method, r.URL.Path, sw.status, time.Since(start).Round(time.Millisecond))
		})
	}
}

type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusWriter) WriteHeader(code int) {
	if !s.wroteHeader {
		s.status = code
		s.wroteHeader = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if !s.wroteHeader {
		s.wroteHeader = true
	}
	return s.ResponseWriter.Write(b)
}

// MaxBytes limits request body size on mutating requests.
func MaxBytes(n int64) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch {
				r.Body = http.MaxBytesReader(w, r.Body, n)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ClientIP resolves the client IP, honoring X-Forwarded-For only when the
// immediate peer is a trusted proxy.
func ClientIP(r *http.Request, trustProxy bool, trusted []string) string {
	remoteIP := hostOnly(r.RemoteAddr)
	if trustProxy && ipInCIDRs(remoteIP, trusted) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			first := strings.TrimSpace(strings.Split(xff, ",")[0])
			if first != "" {
				return first
			}
		}
	}
	return remoteIP
}

func hostOnly(addr string) string {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}

func ipInCIDRs(ip string, cidrs []string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	for _, c := range cidrs {
		if _, network, err := net.ParseCIDR(c); err == nil && network.Contains(parsed) {
			return true
		}
		if c == ip {
			return true
		}
	}
	return false
}

// Run starts the server with hardened timeouts and blocks until ctx is
// cancelled, then shuts down gracefully.
func Run(ctx context.Context, cfg *config.Config, handler http.Handler) error {
	srv := &http.Server{
		Addr:              cfg.Server.ListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	var autocertHTTP *http.Server
	if cfg.Server.TLS.Mode == "autocert" {
		m := &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(cfg.Server.TLS.Domains...),
			Cache:      autocert.DirCache(cfg.Server.TLS.CacheDir),
		}
		if cfg.Server.TLS.Email != "" {
			m.Email = cfg.Server.TLS.Email
		}
		srv.TLSConfig = m.TLSConfig()
		if srv.Addr == "" || srv.Addr == ":8080" {
			srv.Addr = ":443"
		}
		// HTTP-01 challenge + redirect to HTTPS on :80.
		autocertHTTP = &http.Server{Addr: ":80", Handler: m.HTTPHandler(nil), ReadHeaderTimeout: 10 * time.Second}
		go func() {
			if err := autocertHTTP.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Printf("autocert http: %v", err)
			}
		}()
	}

	errc := make(chan error, 1)
	go func() {
		var err error
		if cfg.Server.TLS.Mode == "autocert" {
			log.Printf("listening on %s (autocert TLS for %s)", srv.Addr, strings.Join(cfg.Server.TLS.Domains, ", "))
			err = srv.ListenAndServeTLS("", "")
		} else {
			log.Printf("listening on %s (plain HTTP — terminate TLS at your proxy)", srv.Addr)
			err = srv.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			errc <- err
		}
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		log.Print("shutting down...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if autocertHTTP != nil {
			_ = autocertHTTP.Shutdown(shutdownCtx)
		}
		return srv.Shutdown(shutdownCtx)
	}
}
