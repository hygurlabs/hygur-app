// Package webui embeds the built single-page web client (Vite + React +
// TypeScript) served by the sidecar. `make webui` runs `vite build`, which
// writes index.html and the content-hashed assets straight into ./dist; go:embed
// then bundles that directory into the binary. The SPA talks to the local JSON
// API on the same loopback origin and replaces the legacy SwiftUI views as the
// primary Hygur interface.
package webui

import "embed"

// DistFS is the built SPA: dist/index.html plus dist/assets/*. The
// "__HYGUR_TOKEN__" placeholder inside index.html is substituted with the live
// API token at serve time (see handleWebUI) so same-origin fetches authenticate.
//
//go:embed all:dist
var DistFS embed.FS
