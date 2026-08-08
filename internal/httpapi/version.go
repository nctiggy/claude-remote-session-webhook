package httpapi

// version.go is GET /dashboard/version — the running daemon's answer to "what
// are you?", which is US1's second half. The first is `crswd --version` on the
// command line, and the two are the same string because they read the same
// variable: internal/buildinfo.Version, stamped at link time by the release
// workflow and "dev" in anything it did not build.
//
// **This file holds no version of its own, and that is the whole of it.** A
// constant here, or a copy taken at construction, would compile, pass every test
// that asks the route what it says, and drift the day a release stamped one and
// not the other — which is a daemon reporting a version it is not running, to an
// operator deciding whether to update. The variable is read inside the handler,
// per request, for that reason and no other.
//
// It answers JSON rather than a rendered page, and the contract is why: the
// version contract names one new file, this one, and no template beside it —
// unlike the settings page, whose contract named both. What asks this route is
// something checking a fact rather than a person reading a document, so the
// answer is the fact.

import (
	"net/http"

	"github.com/nctiggy/claude-remote-session-webhook/internal/buildinfo"
)

// patternVersion is the route, and the method is part of it for the reason it is
// part of every other pattern in this package: net/http matches both halves, so
// a path registered for GET is not silently reachable by POST.
//
// It carries no `{$}` because it names an exact path rather than the root — only
// `/` is a subtree pattern in net/http's router.
const patternVersion = "GET /dashboard/version"

// versionResponse is what the route answers with.
//
// One field, because one is what this milestone can honestly fill. The contract
// describes a second — the latest release available — and that answer needs the
// fetch US4 builds; a field invented now would be a null nobody can explain.
// Adding it later is an addition to this struct rather than a change to the
// shape, which is the point of answering with an object at all.
type versionResponse struct {
	Version string `json:"version"`
}

// version serves GET /dashboard/version (FR-002, contracts/version.md).
//
// It reads no operator out of the context, unlike every page on this door. There
// is nothing here that could be rendered for the wrong person: the response
// names no session, no configuration and no identity, so there is no fail-closed
// branch to write and an unnecessary one would only suggest this handler is the
// thing standing between a stranger and an answer. authenticateBrowser is,
// exactly as it is for the embedded assets, which read no operator either.
//
// It mints no page token for the reason the settings page mints none: a token
// authorises a write, and this route offers nothing to write.
func (s *Server) version(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, r, http.StatusOK, versionResponse{Version: buildinfo.Version})
}
