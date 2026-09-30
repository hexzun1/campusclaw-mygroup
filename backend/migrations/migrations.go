// Package migrations embeds the SQL migration files so the api can apply them
// at startup.
//
// The MySQL image's docker-entrypoint-initdb.d scripts run only when the data
// volume is empty, so an existing volume never receives a newly added
// migration. The executor in internal/db is what closes that gap
// (design.md Decision 2); this package only carries the bytes.
//
// The directory is still mounted into docker-entrypoint-initdb.d, which
// ignores this .go file and keeps working for a brand-new volume.
package migrations

import "embed"

// FS holds every *.sql file in this directory, keyed by filename.
//
//go:embed *.sql
var FS embed.FS
