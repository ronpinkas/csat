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
	"time"

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
	mintSet := flag.Int64("set", 0, "question set id for -mint (0 = latest set)")
	mintTenant := flag.Bool("mint-tenant", false, "mint a tenant-provisioning URL for -ref (platform use) and exit")
	mintTTL := flag.Int64("ttl", 86400, "provisioning token TTL in seconds (-mint-tenant)")
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
		link := fmt.Sprintf("%s/s?t=%s", *mintBase, tok)
		if *mintSet > 0 {
			link += fmt.Sprintf("&set=%d", *mintSet)
		}
		fmt.Println(link)
		return
	}

	if *mintTenant {
		if *mintRef == "" {
			log.Fatal("mint-tenant: -ref is required")
		}
		tok, err := admin.MintApplianceToken(secret, *mintRef, "", "", "", time.Duration(*mintTTL)*time.Second)
		if err != nil {
			log.Fatalf("mint-tenant: %v", err)
		}
		// POST this URL to provision the tenant; the JSON reply has invite_url.
		fmt.Printf("%s/provision?t=%s\n", *mintBase, tok)
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

	// The survey definition is only a SEED: on first run it is stored in the
	// database (and existing responses backfilled to it); thereafter the DB is
	// the source of truth and the survey is edited via the admin Survey tab. So a
	// configured survey.json that has been removed (e.g. deleted after porting) is
	// not fatal — we fall back to the built-in default as the seed. A file that
	// exists but is invalid is still a hard error.
	def := surveydef.Default()
	if p := cfg.Survey.Definition; p != "" {
		if _, statErr := os.Stat(p); statErr == nil {
			loaded, err := surveydef.Load(p)
			if err != nil {
				log.Fatalf("survey definition: %v", err)
			}
			def = loaded
		} else {
			log.Printf("survey: %s not found — using the built-in default as the seed (edit surveys in the admin Survey tab)", p)
		}
	}
	log.Printf("survey: %d question(s) in the seed definition", len(def.Questions))

	secure := cfg.Server.TLS.Mode == "autocert" || cfg.Server.SecureCookies
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

	// The seed file's content now lives in the database (admin.New seeded it). In
	// single-tenant mode, rename it to <path>.ported to make the switch explicit
	// — surveys are edited in the admin Survey tab from here on. Best-effort: the
	// config dir is read-only under the hardened systemd unit, so on failure we
	// just tell the operator to remove it. (Multi-tenant keeps the file as the
	// seed template for future tenants.)
	if !cfg.Tenancy.Multi() {
		if p := cfg.Survey.Definition; p != "" {
			if _, statErr := os.Stat(p); statErr == nil {
				ported := p + ".ported"
				if rerr := os.Rename(p, ported); rerr == nil {
					log.Printf("survey: ported %s into the database and renamed it to %s (edit surveys in the admin Survey tab)", p, ported)
				} else {
					log.Printf("survey: %s is seeded into the database and no longer used — you may remove it (auto-rename failed: %v)", p, rerr)
				}
			}
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("GET /s", surveyRL(http.HandlerFunc(surveyH.Form)))
	mux.Handle("POST /s", surveyRL(http.HandlerFunc(surveyH.Submit)))
	mux.Handle("POST /s/save", surveyRL(http.HandlerFunc(surveyH.Save)))
	mux.Handle("GET /static/", cacheForever(http.StripPrefix("/static/", static)))
	mux.HandleFunc("GET /branding/logo", surveyH.Logo)
	mux.HandleFunc("GET /branding/theme.css", surveyH.ThemeCSS)
	adminH.Mount(mux)

	handler := httpx.Chain(mux,
		httpx.Recover(),
		httpx.SecurityHeaders(secure),
		httpx.Logger(),
		// 64 KiB was too tight: a survey definition (URL-encoded JSON) and a long
		// free-text submission both exceed it, and an over-limit body fails to
		// parse — which surfaces as a bogus "invalid CSRF token". 2 MiB also
		// matches the budget the logo upload already assumes.
		httpx.MaxBytes(2*1024*1024),
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
