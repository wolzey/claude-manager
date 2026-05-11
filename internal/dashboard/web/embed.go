// Package web exposes the embedded dashboard assets as an fs.FS.
package web

import (
	"embed"
	"io/fs"
)

//go:embed index.html app.js style.css
var assets embed.FS

// Assets returns the embedded filesystem rooted at the dashboard web/ directory.
func Assets() fs.FS { return assets }
