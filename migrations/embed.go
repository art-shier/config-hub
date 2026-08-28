package migrations

import "embed"

// FS contains the SQL migration files shipped with the binary.
//
//go:embed *.sql
var FS embed.FS
