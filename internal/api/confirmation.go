package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"
)

type confirmationInput struct {
	ConfirmationToken string `json:"confirmationToken"`
	ConfirmationName  string `json:"confirmationName"`
}
type confirmationChallenge struct {
	binding [32]byte
	expires time.Time
}

// Challenges are server-issued, short-lived and single-use. Bind to the session,
// route, exact operation and target so changing a query or selection invalidates it.
func (s *Server) requireConfirmation(w http.ResponseWriter, r *http.Request, action, target, reason, preview string, in confirmationInput) bool {
	session := sessionFrom(r)
	binding := sha256.Sum256([]byte(session.CSRF + "\x00" + r.Method + "\x00" + r.URL.Path + "\x00" + action + "\x00" + target))
	if in.ConfirmationToken == "" {
		count := 0
		s.confirmations.Range(func(key, value any) bool {
			if time.Now().After(value.(confirmationChallenge).expires) {
				s.confirmations.Delete(key)
			} else {
				count++
			}
			return true
		})
		if count >= 1024 {
			writeError(w, http.StatusTooManyRequests, "too many pending confirmations")
			return false
		}
		bytes := make([]byte, 32)
		if _, err := rand.Read(bytes); err != nil {
			writeError(w, http.StatusInternalServerError, "could not create confirmation")
			return false
		}
		token := hex.EncodeToString(bytes)
		s.confirmations.Store(token, confirmationChallenge{binding, time.Now().Add(5 * time.Minute)})
		name, _, _ := strings.Cut(action, "\n")
		writeJSON(w, http.StatusOK, map[string]any{"needsConfirmation": true, "operation": name, "reason": reason, "confirmationTarget": target, "confirmationToken": token, "preview": preview})
		return false
	}
	if in.ConfirmationName != target {
		writeError(w, http.StatusBadRequest, "confirmation does not match the target")
		return false
	}
	value, exists := s.confirmations.LoadAndDelete(in.ConfirmationToken)
	if !exists {
		writeError(w, http.StatusConflict, "confirmation expired or already used; request a new confirmation")
		return false
	}
	challenge := value.(confirmationChallenge)
	if time.Now().After(challenge.expires) || challenge.binding != binding {
		writeError(w, http.StatusConflict, "confirmation expired or operation changed; request a new confirmation")
		return false
	}
	return true
}
