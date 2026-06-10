package main

import (
	"embed"
	"io/fs"
	"net/http"

	"github.com/go-chi/chi/v5"
)

//go:embed all:adminui/dist
var adminDist embed.FS

// registerAdminSPA serves the embedded operator SPA: index.html at /admin and
// /admin/, hashed assets at /admin/assets/*. The SPA shell is public (it's just
// the login + dashboard chrome); the data behind it (/admin/cost) is gated by the
// operator passkey. Built with Vite base "/admin/", so asset URLs are absolute.
func registerAdminSPA(root chi.Router) error {
	sub, err := fs.Sub(adminDist, "adminui/dist")
	if err != nil {
		return err
	}
	index, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		return err
	}
	serveIndex := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		_, _ = w.Write(index)
	}
	root.Get("/admin", serveIndex)
	root.Get("/admin/", serveIndex)
	root.Handle("/admin/assets/*", http.StripPrefix("/admin/", http.FileServer(http.FS(sub))))
	return nil
}
