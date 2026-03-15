package auth

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/atombasedev/atombase/config"
)

const SaveMagicLink = `
INSERT INTO email_magic_links (id, email, token_hash, created_at, expires_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(email) DO UPDATE SET
  id = excluded.id,
  token_hash = excluded.token_hash,
  created_at = excluded.created_at,
  expires_at = excluded.expires_at;`

var (
	ErrInvalidEmail                   = errors.New("invalid email")
	ErrInvalidOrExpiredMagicLink      = errors.New("invalid or expired magic link")
	ErrInvalidOrExpiredOTP            = errors.New("invalid or expired otp")
	ErrMagicLinkCallbackNotConfigured = errors.New("auth magic link callback url is not configured")
)

func BeginMagicLogin(email string, db *sql.DB, ctx context.Context) error {
	email = NormalizeEmail(email)
	if err := ValidateEmail(email); err != nil {
		return err
	}

	now := time.Now().UTC()
	token := ID256()
	if _, err := buildMagicLinkURL(token); err != nil {
		return err
	}
	tokenHash := shaHash(token)
	id := ID128()

	_, err := db.ExecContext(ctx, SaveMagicLink, id, email, tokenHash, now.Unix(), now.Add(15*time.Minute).Unix())

	if err != nil {
		return err
	}

	msg, err := buildMagicLinkEmail(email, token)
	if err != nil {
		_, _ = db.ExecContext(ctx, `DELETE FROM email_magic_links WHERE id = ?`, id)
		return err
	}

	if err := sendEmailFn(ctx, msg); err != nil {
		_, _ = db.ExecContext(ctx, `DELETE FROM email_magic_links WHERE id = ?`, id)
		return err
	}

	return nil

}

func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func ValidateEmail(email string) error {
	if !strings.Contains(email, "@") || strings.HasPrefix(email, "@") || strings.HasSuffix(email, "@") {
		return ErrInvalidEmail
	}
	return nil
}

func buildMagicLinkURL(token string) (string, error) {
	target := strings.TrimSpace(config.Cfg.AuthMagicLinkCallbackURL)
	if target == "" {
		return "", ErrMagicLinkCallbackNotConfigured
	}
	parsed, err := url.Parse(target)
	if err != nil {
		return "", ErrMagicLinkCallbackNotConfigured
	}
	query := parsed.Query()
	query.Set("token", token)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func CompleteMagicLink(token string, db *sql.DB, ctx context.Context) (*User, *Session, bool, error) {
	now := time.Now().UTC().Unix()

	row := db.QueryRowContext(ctx,
		`DELETE FROM email_magic_links
		WHERE token_hash = ? AND expires_at > ?
		RETURNING email`,
		shaHash(token), now,
	)

	var email string
	err := row.Scan(&email)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, false, ErrInvalidOrExpiredMagicLink
	}
	if err != nil {
		return nil, nil, false, err
	}

	user, isNew, err := FindOrCreateUser(email, db, ctx)
	if err != nil {
		return nil, nil, false, err
	}
	session := CreateSession(user.ID)
	if err := SaveSession(session, db, ctx); err != nil {
		return nil, nil, false, err
	}

	return user, session, isNew, nil
}
