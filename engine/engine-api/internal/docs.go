package engineapi

import (
	"embed"
	"net/http"
)

//go:embed docs/index.html docs/openapi.yaml docs/*.js docs/*.css docs/*.svg
var docs embed.FS

func registerDocs(mux *http.ServeMux) {
	mux.HandleFunc("GET /docs", serveDocs)
	mux.HandleFunc("GET /docs/{asset}", serveDocs)
}

func serveDocs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	name := r.PathValue("asset")
	switch name {
	case "":
		name = "index.html"
		w.Header().Set("Content-Security-Policy",
			"default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'")
	case "index.html":
		http.NotFound(w, r)
		return
	case "openapi.yaml":
		w.Header().Set("Content-Type", "application/yaml")
	}
	http.ServeFileFS(w, r, docs, "docs/"+name)
}
