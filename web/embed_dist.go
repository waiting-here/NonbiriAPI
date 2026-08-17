//go:build dist

// Package web embeds the two single-page applications (admin, user) and serves
// them as static assets with SPA index.html fallback.
//
// This file is compiled only with the `dist` build tag (go build -tags dist).
// It embeds the real Vite build output under web/dist, which is gitignored and
// produced by `npm run build` (Phase 0 track B). Building with -tags dist therefore
// requires the frontend build to have run first; the default (untagged) build
// uses the committed web/stub placeholders embedded by embed_stub.go so `go
// build ./...` always passes without any frontend tooling.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist/admin
var adminRaw embed.FS

//go:embed all:dist/user
var userRaw embed.FS

func embeddedAdmin() fs.FS {
	sub, err := fs.Sub(adminRaw, "dist/admin")
	if err != nil {
		panic("embed admin dist: " + err.Error())
	}
	return sub
}

func embeddedUser() fs.FS {
	sub, err := fs.Sub(userRaw, "dist/user")
	if err != nil {
		panic("embed user dist: " + err.Error())
	}
	return sub
}