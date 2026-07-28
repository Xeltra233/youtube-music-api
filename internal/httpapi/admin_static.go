package httpapi

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/xeltra/ytmusic-bridge/internal/admin"
)

func (s *Server) mountAdminStatic(mux *http.ServeMux) {
	sub, err := fs.Sub(admin.Assets, "assets")
	if err != nil {
		// Should not happen with embed; skip static if broken.
		return
	}
	fileServer := http.FileServer(http.FS(sub))

	// /admin and /admin/ -> index.html (upload page)
	mux.HandleFunc("GET /admin", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/", http.StatusFound)
	})
	mux.HandleFunc("GET /admin/{$}", func(w http.ResponseWriter, r *http.Request) {
		s.serveAdminAsset(w, r, sub, "index.html")
	})
	mux.HandleFunc("GET /admin/{path...}", func(w http.ResponseWriter, r *http.Request) {
		// Strip /admin/ prefix for FS lookup.
		p := strings.TrimPrefix(r.URL.Path, "/admin/")
		if p == "" {
			s.serveAdminAsset(w, r, sub, "index.html")
			return
		}
		// Prevent path tricks; FS open is rooted.
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/" + p
		fileServer.ServeHTTP(w, r2)
	})
}

func (s *Server) serveAdminAsset(w http.ResponseWriter, r *http.Request, fsys fs.FS, name string) {
	f, err := fsys.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// http.ServeContent needs io.ReadSeeker
	rs, ok := f.(ioReadSeeker)
	if !ok {
		http.Error(w, "asset error", http.StatusInternalServerError)
		return
	}
	http.ServeContent(w, r, name, st.ModTime(), rs)
}

type ioReadSeeker interface {
	Read(p []byte) (n int, err error)
	Seek(offset int64, whence int) (int64, error)
}
