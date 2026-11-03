// Package docs embeds the shipped API documentation for the management
// assistant. Tools can inspect request contracts without filesystem access.
package docs

import "embed"

//go:embed *.md
var Files embed.FS
