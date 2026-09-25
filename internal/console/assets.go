package console

import (
	"embed"
	"net/http"
)

//go:embed assets/index.html assets/app.js assets/app.css
var assets embed.FS

// asset is one embedded file and the type it is served as.
type asset struct{ file, contentType string }

// served is every path outside /api/ that answers. A path is looked up
// exactly, never joined onto a directory, so no spelling of a path reaches a
// file that is not listed here.
var served = map[string]asset{
	"/":        {"assets/index.html", "text/html; charset=utf-8"},
	"/app.js":  {"assets/app.js", "text/javascript; charset=utf-8"},
	"/app.css": {"assets/app.css", "text/css; charset=utf-8"},
}

func serveAsset(w http.ResponseWriter, r *http.Request) {
	a, ok := served[r.URL.Path]
	if !ok {
		refuse(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		refuse(w, http.StatusMethodNotAllowed, "only GET is served here")
		return
	}
	body, err := assets.ReadFile(a.file)
	if err != nil {
		refuse(w, http.StatusInternalServerError, "the page's own file is missing from this build")
		return
	}
	w.Header().Set("Content-Type", a.contentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
