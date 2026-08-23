package httpapi

import (
	"net/http"
	"time"
)

// The conversation lookup exists because the control it feeds was answering for
// the wrong directory.
//
// The resume control has listed a directory's prior conversations since spec
// 009, but the list was resolved once, server-side, for the *first suggested*
// directory — so an operator who typed or picked a different one was offered
// somebody else's work to continue. A wrong list is worse than no list.
//
// It is a read and nothing else. It discloses strictly less than the form it
// serves already renders, and it is bounded by the same allowlist, so it widens
// nothing: session.Conversations resolves the directory through ResolveWorkDir
// before it becomes a path, which means the set of directories whose
// conversations can be listed is exactly the set the operator may start a
// session in.

// patternConversations is the route, with the method part of it for the reason
// every other pattern here has one: a GET-only route that answered a POST would
// be a read that could be reached by a form somebody else's page submitted.
const patternConversations = "GET /sessions/conversations"

// queryDir is the working directory the caller is asking about, spelled exactly
// as they typed it into the form. It is never used as a path — ResolveWorkDir's
// result is.
const queryDir = "dir"

// conversationsResponse is the whole of what leaves this route: an identifier
// and a time per conversation, and nothing else (FR-025).
//
// No title, no first message, no size, no path. What a conversation *said* is
// the operator's work and not this daemon's to render, and no transcript is
// opened to build this.
type conversationsResponse struct {
	Conversations []conversationView `json:"conversations"`
}

// conversations answers the resume control.
//
// **Every failure is an empty list**, and there is no 400 and no 404. That is
// session.Conversations' own contract carried up to the boundary: a directory
// outside the allowlist, one that does not exist, one with no history, one that
// cannot be read, and a Claude layout this daemon does not recognise all answer
// identically.
//
// Two reasons. A form that refused to render because somebody else's release
// moved a directory would be this daemon broken by a change it has no part in,
// and the worst outcome of an empty list is an operator who starts a fresh
// session — which is what they got before the control existed. And answering
// differently for "outside the allowlist" than for "no history" would make this
// route a way to ask which directories exist, which is the enumeration every
// other refusal in this package is shaped to prevent.
func (s *Server) conversations(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, r, http.StatusOK, conversationsResponse{
		Conversations: s.conversationsForDir(s.clock.Now(), r.URL.Query().Get(queryDir)),
	})
}

// conversationsForDir is the one place a directory becomes a list of
// conversations, shared by this route and by the first render of the form so the
// two cannot disagree about what an offer looks like.
func (s *Server) conversationsForDir(now time.Time, dir string) []conversationView {
	found := s.sessions.Conversations(dir)
	out := make([]conversationView, 0, len(found))
	for _, c := range found {
		out = append(out, conversationView{
			ID:    c.ID,
			Short: shortConversation(c.ID),
			// formatAge's vocabulary, so one page cannot spell a duration two
			// ways. A conversation written a moment ago reads "less than a
			// minute ago" rather than as a timestamp nobody asked for.
			Modified: formatAge(now.Sub(c.Modified)) + " ago",
		})
	}
	return out
}
