//go:build !dist

// Package web embeds the two single-page applications (admin, user) and serves
// them as static assets with SPA index.html fallback.
//
// The default build embeds committed placeholder bundles under web/stub so
// `go build ./...` succeeds with NO frontend build present. A production build
// uses the `dist` tag (go build -tags dist) to embed the real Vite output under
// web/dist instead. Only one of these two files is compiled for a given build,
// so they may both declare embeddedAdmin/embeddedUser without conflict.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:stub/admin
var adminRaw embed.FS

//go:embed all:stub/user
var userRaw embed.FS

func embeddedAdmin() fs.FS {
	sub, err := fs.Sub(adminRaw, "stub/admin")
	if err != nil {
		panic("embed admin stub: " + err.Error())
	}
	return sub
}

func embeddedUser() fs.FS {
	sub, err := fs.Sub(userRaw, "stub/user")
	if err != nil {
		panic("embed user stub: " + err.Error())
	}
	return sub
}
