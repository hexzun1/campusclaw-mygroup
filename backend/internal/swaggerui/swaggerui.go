// Package swaggerui serves the self-hosted API documentation under /swagger/
// (design.md Decision 9): the swagger-ui assets come from the embedded
// filesystem of github.com/swaggo/files/v2, the page and the initializer are
// this repository's own files, and the specification is the generated
// campusclaw/backend/docs. Every resource is same-origin and no script is
// inline, so the server-level CSP holds unchanged.
package swaggerui

import (
	"bytes"
	"embed"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"time"

	files "github.com/swaggo/files/v2"
)

// own carries the two files this repository writes itself. They replace the
// upstream index.html (which points at the petstore spec) and initializer
// (which points at the swagger.io demo).
//
//go:embed index.html swagger-initializer.js
var own embed.FS

// Handler serves the /swagger/ subtree. spec returns the generated Swagger
// JSON (docs.SwaggerInfo.ReadDoc), so the served document cannot go stale
// relative to the binary.
//
// The mapping is: "" and index.html from this repository, the initializer
// from this repository, doc.json from spec, everything else from the
// swagger-ui asset set. An unknown path is a 404.
func Handler(spec func() string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/swagger/")
		switch {
		case name == "" || name == "index.html":
			serveFrom(w, r, own, "index.html")
		case name == "swagger-initializer.js":
			serveFrom(w, r, own, "swagger-initializer.js")
		case name == "doc.json":
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_, _ = io.WriteString(w, spec())
		default:
			serveFrom(w, r, files.FS, name)
		}
	})
}

// serveFrom writes one embedded file. The name keeps its extension so
// http.ServeContent infers the content type; a missing file is a 404, and the
// open call rejects paths that try to escape the filesystem.
func serveFrom(w http.ResponseWriter, r *http.Request, fsys fs.FS, name string) {
	f, err := fsys.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}
