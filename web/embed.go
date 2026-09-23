package web

import (
	"embed"
)

// Svelte SPA build output.
// Populated by `make svelte-embed` (copies svelte/dist -> web/svelteDist).
// The all: prefix keeps the placeholder file so `go build` works before
// the frontend has been built.
//
//go:embed all:svelteDist
var SvelteAssets embed.FS
