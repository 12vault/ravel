package auth

import "example.com/simple/internal/db"

type SessionManager struct {
	Store db.SessionStore
}

func (*SessionManager) CreateSession(userID string) string {
	saveSession(userID)
	return userID
}

func saveSession(userID string) {
	_ = userID
}
