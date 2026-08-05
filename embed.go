// Package flowlio carries the assets the binaries ship with, and nothing else.
//
// It sits at the repository root for one reason: `go:embed` cannot reach outside the directory of
// the file that declares it, and the migrations are the source of truth of the data model — they
// live in sql/migrations/ and they are not moving under internal/ to please a directive.
package flowlio

import "embed"

// Migrations holds every forward migration, in the same layout as on disk: sql/migrations/*.up.sql.
//
// Embedding them is what makes the API image usable WITHOUT a checkout of this repository. Before
// that, the schema came from a bind mount of sql/migrations into a migrate container, so the image
// alone was not enough to bring up an instance — a self-hosted user had to clone the source.
//
// Down migrations are deliberately excluded: a rollback is a human decision taken with the
// golang-migrate CLI (`make down-dev`), never something a container start can reach.
//
//go:embed sql/migrations/*.up.sql
var Migrations embed.FS
