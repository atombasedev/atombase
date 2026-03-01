package auth

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"strings"
	"time"
)

type Session struct {
	Id        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Secret    string    `json:"secret"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

type SessionToken string

const sessionExpiresIn = 24 * time.Hour

var ErrInvalidSession = errors.New("invalid session")

func CreateSession(userID string) *Session {
	now := time.Now().UTC()

	return &Session{
		Id:        ID128(),
		UserID:    userID,
		Secret:    ID128(),
		CreatedAt: now,
		ExpiresAt: now.Add(sessionExpiresIn),
	}
}

func splitToken(token SessionToken) (id, secret string, err error) {
	parts := strings.SplitN(string(token), ".", 2)
	if len(parts) != 2 {
		return "", "", ErrInvalidSession
	}

	return parts[0], parts[1], nil
}

// Token returns the session token in format: sessionId.secret
func (s *Session) Token() SessionToken {
	return SessionToken(s.Id + "." + s.Secret)
}

func SaveSession(session *Session, db *sql.DB, ctx context.Context) error {
	secretHash := shaHash(session.Secret)

	_, err := db.ExecContext(ctx,
		"INSERT INTO atombase_sessions (id, secret_hash, user_id, created_at, expires_at) VALUES (?, ?, ?, ?, ?)",
		session.Id, secretHash, session.UserID,
		session.CreatedAt.Format(time.RFC3339),
		session.ExpiresAt.Format(time.RFC3339),
	)

	return err
}

func ValidateSession(token SessionToken, db *sql.DB, ctx context.Context) (*Session, error) {
	id, secret, err := splitToken(token)
	if err != nil {
		return nil, err
	}

	var session Session
	var secretHash []byte
	var expiresAt, createdAt string

	err = db.QueryRowContext(ctx,
		`SELECT id, secret_hash, user_id, expires_at, created_at
		 FROM atombase_sessions WHERE id = ?`,
		id,
	).Scan(&session.Id, &secretHash, &session.UserID, &expiresAt, &createdAt)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInvalidSession
	}
	if err != nil {
		return nil, err
	}

	// Constant-time comparison
	if subtle.ConstantTimeCompare(shaHash(secret), secretHash) != 1 {
		return nil, ErrInvalidSession
	}

	// Parse timestamps
	session.ExpiresAt, _ = time.Parse(time.RFC3339, expiresAt)
	session.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)

	if time.Now().UTC().After(session.ExpiresAt) {
		return nil, ErrInvalidSession
	}

	return &session, nil
}

func DeleteSession(sessionID string, db *sql.DB, ctx context.Context) error {
	_, err := db.ExecContext(ctx, "DELETE FROM atombase_sessions WHERE id = ?", sessionID)
	return err
}

func DeleteUserSessions(userID string, db *sql.DB, ctx context.Context) error {
	_, err := db.ExecContext(ctx, "DELETE FROM atombase_sessions WHERE user_id = ?", userID)
	return err
}
