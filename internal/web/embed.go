// Package web holds the embedded HTML templates and static assets, plus a small
// renderer. Everything here is compiled into the binary via go:embed, so the
// deployed artifact is a single self-contained file.
package web

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"html/template"
	"io"
	"io/fs"
	"net/http"
)

//go:embed templates/*.tmpl
var templatesFS embed.FS

//go:embed static
var staticFS embed.FS

// Templates parses every page template against the shared base layout. Each
// page template defines the "content" block; layout.tmpl wraps it.
type Templates struct {
	pages map[string]*template.Template
}

// pageTemplates are the standalone page templates (each composed with layout.tmpl).
var pageTemplates = []string{
	"survey.tmpl",
	"survey_done.tmpl",
	"survey_error.tmpl",
	"login.tmpl",
	"force_change.tmpl",
	"invite_redeem.tmpl",
	"forgot.tmpl",
	"reset.tmpl",
	"dashboard.tmpl",
	"users.tmpl",
	"settings.tmpl",
	"survey_edit.tmpl",
}

// LoadTemplates parses all page templates once. Call at startup. It registers
// an "asset" function that appends a content-hash query string to static URLs
// so that immutable-cached assets are re-fetched whenever their content changes.
func LoadTemplates(funcs template.FuncMap) (*Templates, error) {
	merged := template.FuncMap{"asset": assetURL}
	for k, v := range funcs {
		merged[k] = v
	}
	t := &Templates{pages: make(map[string]*template.Template)}
	for _, name := range pageTemplates {
		tmpl := template.New("layout.tmpl").Funcs(merged)
		tmpl, err := tmpl.ParseFS(templatesFS, "templates/layout.tmpl", "templates/"+name)
		if err != nil {
			return nil, err
		}
		t.pages[name] = tmpl
	}
	return t, nil
}

// assetFingerprint is a short hash of all embedded static content, computed once.
var assetFingerprint = computeAssetFingerprint()

func computeAssetFingerprint() string {
	h := sha256.New()
	_ = fs.WalkDir(staticFS, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := staticFS.ReadFile(p)
		if err != nil {
			return err
		}
		h.Write([]byte(p))
		h.Write(b)
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))[:10]
}

// assetURL appends the content fingerprint as a cache-busting query param.
func assetURL(path string) string {
	return path + "?v=" + assetFingerprint
}

// Render executes the named page template (e.g. "survey.tmpl") into w.
func (t *Templates) Render(w io.Writer, name string, data any) error {
	tmpl, ok := t.pages[name]
	if !ok {
		return fs.ErrNotExist
	}
	return tmpl.ExecuteTemplate(w, "layout.tmpl", data)
}

// StaticHandler serves the embedded /static tree.
func StaticHandler() (http.Handler, error) {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, err
	}
	return http.FileServerFS(sub), nil
}
