package web

import _ "embed"

// The server error shell shares the SPA's canonical tokens and UI components,
// including dark colors, without depending on a successful asset request.
//
//go:embed src/shared/styles/tokens.css
var errorPageTokens string

//go:embed src/shared/styles/ui.css
var errorPageUI string

func ErrorPageStyles() string { return errorPageTokens + "\n" + errorPageUI }
