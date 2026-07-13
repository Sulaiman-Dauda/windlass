// Package migrations embeds the forward-only SQL migration files.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
