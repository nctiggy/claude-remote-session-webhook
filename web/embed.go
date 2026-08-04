// Package web is the dashboard's front end compiled into the binary: the
// html/template sources under templates/, and the static assets under static/.
// There is no npm, no build step, and no file read from disk at runtime — a
// daemon that has started is a daemon that already holds every byte it will
// serve.
//
// This package exists for a mechanical reason worth writing down, because
// tasks.md asks for the embed to live in internal/httpapi/render.go and Go will
// not allow that: a //go:embed pattern may not name a path outside the directory
// tree of the file carrying it. web/ sits at the repository root (AGENTS.md's
// project map) and internal/httpapi is not above it, so the directives have to
// be here. Nothing else moved — plan.md's dependency direction, httpapi → web,
// is exactly what this produces, and the parsing that T002 is really about still
// happens in render.go at construction.
//
// Deliberately no other Go code: what to render, in what order, and behind which
// door are decisions for the package that answers requests.
package web

import "embed"

// Templates is the template tree, rooted at templates/. It is parsed as one set
// by internal/httpapi; nothing here decides how it is named or executed.
//
//go:embed templates
var Templates embed.FS

// Static is the asset tree, rooted at static/. It holds the stylesheet today and
// gains the stream client and the rain canvas later in this milestone. A third
// asset needs a reason: every one of them is an origin the CSP must permit, and
// docs/security.md sends that policy with no exceptions.
//
//go:embed static
var Static embed.FS
