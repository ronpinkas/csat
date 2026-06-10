// Command csat runs the self-contained CSAT survey + analytics server.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/ronpinkas/csat/internal/admin"
	"github.com/ronpinkas/csat/internal/config"
	"github.com/ronpinkas/csat/internal/httpx"
	"github.com/ronpinkas/csat/internal/survey"
	"github.com/ronpinkas/csat/internal/surveydef"
	"github.com/ronpinkas/csat/internal/tenant"
	"github.com/ronpinkas/csat/internal/token"
	"github.com/ronpinkas/csat/internal/web"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	configPath := flag.String("config", "/etc/csat/config.toml", "path to the TOML config file")
	showVersion := flag.Bool("version", false, "print version and exit")
	mint := flag.Bool("mint", false, "mint a survey link for the given -cid/-ts and exit (ops/testing helper)")
	mintSubject := flag.String("subject", "", "subject (phone, order id, ticket id…) for -mint")
	mintCID := flag.String("cid", "", "alias for -subject")
	mintTS := flag.Int64("ts", 0, "subject time as unix seconds for -mint")
	mintBase := flag.String("base", "", "base URL for -mint, e.g. https://csat.example.com")
	mintLang := flag.String("lang", "en", "language for -mint (en|es)")
	mintRef := flag.String("ref", "", "tenant ref for -mint (multi-tenant mode; empty = single-tenant)")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	// Auto-detect logo.<ext> alongside the config file unless a dir is set.
	if cfg.Branding.LogoDir == "" {
		cfg.Branding.LogoDir = filepath.Dir(*configPath)
	}

	secret, generated, err := cfg.ResolveCryptoKey()
	if err != nil {
		log.Fatalf("crypto key: %v", err)
	}

	if *mint {
		subject := *mintSubject
		if subject == "" {
			subject = *mintCID
		}
		tok, err := token.Encrypt(secret, subject, *mintTS, *mintLang, *mintRef)
		if err != nil {
			log.Fatalf("mint: %v", err)
		}
		fmt.Printf("%s/s?t=%s\n", *mintBase, tok)
		return
	}
	if generated {
		log.Printf("generated a new per-deployment token secret at %s", cfg.Security.CryptoKeyPath)
		log.Printf("  -> share this value with your call platform's link-builder (also shown on /settings):")
		log.Printf("     %s", secret)
	}

	var provider tenant.Provider
	if cfg.Tenancy.Multi() {
		provider, err = tenant.NewMulti(cfg.Tenancy.DataDir)
		log.Printf("multi-tenant mode: one database per ref under %s", cfg.Tenancy.DataDir)
	} else {
		provider, err = tenant.NewSingle(cfg.DB.Path)
	}
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer provider.Close()

	tmpl, err := web.LoadTemplates(nil)
	if err != nil {
		log.Fatalf("templates: %v", err)
	}

	def, err := surveydef.Load(cfg.Survey.Definition)
	if err != nil {
		log.Fatalf("survey definition: %v", err)
	}
	log.Printf("survey: %d question(s) loaded", len(def.Questions))

	secure := cfg.Server.TLS.Mode == "autocert"
	static, err := web.StaticHandler()
	if err != nil {
		log.Fatalf("static: %v", err)
	}

	surveyH := survey.New(provider, tmpl, cfg, def, secret, secure)
	surveyRL := httpx.NewLimiter(30, 10).Middleware(cfg.Server.TrustProxy, cfg.Server.TrustedProxies)

	adminH, err := admin.New(provider, tmpl, cfg, def, secret, secure)
	if err != nil {
		log.Fatalf("admin: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("GET /s", surveyRL(http.HandlerFunc(surveyH.Form)))
	mux.Handle("POST /s", surveyRL(http.HandlerFunc(surveyH.Submit)))
	mux.Handle("GET /static/", cacheForever(http.StripPrefix("/static/", static)))
	mux.HandleFunc("GET /branding/logo", surveyH.Logo)
	mux.HandleFunc("GET /branding/theme.css", surveyH.ThemeCSS)
	adminH.Mount(mux)

	handler := httpx.Chain(mux,
		httpx.Recover(),
		httpx.SecurityHeaders(secure),
		httpx.Logger(),
		httpx.MaxBytes(64*1024),
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Printf("csat %s starting (site=%q)", version, cfg.Site.Name)
	if err := httpx.Run(ctx, cfg, handler); err != nil {
		log.Fatalf("server: %v", err)
	}
	log.Print("stopped")
	_ = os.Stdout.Sync()
}

// cacheForever marks embedded, versioned static assets as immutable.
func cacheForever(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		next.ServeHTTP(w, r)
	})
}
