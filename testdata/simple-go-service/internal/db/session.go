package db

type SessionStore interface {
	Save(userID string) error
}
