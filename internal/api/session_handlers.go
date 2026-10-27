package api

import "net/http"

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r)
	items, err := s.sessions.List(r.Context(), sess.UserID, sess.ID)
	if err != nil {
		writeError(w, 500, "could not list sessions")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"sessions": items})
}

func (s *Server) revokeSession(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r)
	id := r.PathValue("sessionID")
	ok, err := s.sessions.Revoke(r.Context(), sess.UserID, id)
	if err != nil {
		writeError(w, 500, "could not revoke session")
		return
	}
	if !ok {
		writeError(w, 404, "session not found")
		return
	}
	s.queries.revokeSessions(sess.UserID, func(sessionID string) bool { return sessionID == id })
	auditFrom(r).entry.Target = id
	if id == sess.ID {
		s.clearCookie(w, r)
	}
	writeJSON(w, 200, map[string]bool{"current": id == sess.ID})
}

func (s *Server) revokeOtherSessions(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r)
	cookie, _ := r.Cookie(cookieName)
	if err := s.sessions.DeleteOthers(r.Context(), sess.UserID, cookie.Value); err != nil {
		writeError(w, 500, "could not revoke sessions")
		return
	}
	s.queries.revokeSessions(sess.UserID, func(sessionID string) bool { return sessionID != sess.ID })
	writeJSON(w, 200, map[string]string{"status": "ok"})
}
