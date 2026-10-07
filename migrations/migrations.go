// Package migrations bettet die SQL-Migrationen in das Binary ein.
package migrations

import "embed"

// FS enthält alle Migrationen im goose-Format.
//
//go:embed *.sql
var FS embed.FS
