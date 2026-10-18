// Package videotools embeds the static web assets and fonts into the server binary.
package videotools

import "embed"

//go:embed web/static assets/fonts
var Assets embed.FS
