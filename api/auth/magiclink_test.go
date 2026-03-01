package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atombasedev/atombase/config"
)

func TestValidateEmail(t *testing.T) {
	tests := []struct {
		name    string
		email   string
		wantErr bool
	}{
		{name: "valid", email: "user@example.com"},
		{name: "missing at", email: "user.example.com", wantErr: true},
		{name: "starts with at", email: "@example.com", wantErr: true},
		{name: "ends with at", email: "user@", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateEmail(tc.email)
			if tc.wantErr {
				if !errors.Is(err, ErrInvalidEmail) {
					t.Fatalf("expected ErrInvalidEmail, got %v", err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestBuildMagicLinkURL_UsesConfigAndEscapesToken(t *testing.T) {
	orig := config.Cfg.ApiURL
	config.Cfg.ApiURL = " https://api.atomicbase.dev/ "
	t.Cleanup(func() {
		config.Cfg.ApiURL = orig
	})

	url := buildMagicLinkURL("a+b/c==")
	if url != "https://api.atomicbase.dev/auth/magic-link/complete?token=a%2Bb%2Fc%3D%3D" {
		t.Fatalf("unexpected url: %s", url)
	}
}

func TestCompleteMagicLink_SuccessConsumesLinkAndCreatesSession(t *testing.T) {
	db := setupAuthTestDB(t)
	token := "known-token"
	now := time.Now().UTC().Unix()

	_, err := db.Exec(
		`INSERT INTO email_magic_links (id, email, token_hash, created_at, expires_at) VALUES (?, ?, ?, ?, ?)`,
		"ml_1",
		"NEWUSER@Example.com",
		shaHash(token),
		now,
		now+300,
	)
	if err != nil {
		t.Fatalf("seed magic link: %v", err)
	}

	user, session, isNew, err := CompleteMagicLink(token, db, context.Background())
	if err != nil {
		t.Fatalf("complete magic link: %v", err)
	}
	if !isNew {
		t.Fatal("expected first login via magic link to create user")
	}
	if user.Email != "newuser@example.com" {
		t.Fatalf("expected normalized user email, got %q", user.Email)
	}
	if session == nil || session.UserID != user.ID {
		t.Fatalf("expected session for created user, got %#v", session)
	}

	_, err = ValidateSession(session.Token(), db, context.Background())
	if err != nil {
		t.Fatalf("expected saved session to validate, got %v", err)
	}

	var count int
	err = db.QueryRow(`SELECT COUNT(*) FROM email_magic_links WHERE id = ?`, "ml_1").Scan(&count)
	if err != nil {
		t.Fatalf("count magic links: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected magic link to be deleted after completion, found %d rows", count)
	}
}

func TestCompleteMagicLink_InvalidOrExpired(t *testing.T) {
	db := setupAuthTestDB(t)
	now := time.Now().UTC().Unix()

	_, err := db.Exec(
		`INSERT INTO email_magic_links (id, email, token_hash, created_at, expires_at) VALUES (?, ?, ?, ?, ?)`,
		"ml_expired",
		"user@example.com",
		shaHash("expired-token"),
		now-600,
		now-60,
	)
	if err != nil {
		t.Fatalf("seed expired magic link: %v", err)
	}

	_, _, _, err = CompleteMagicLink("expired-token", db, context.Background())
	if !errors.Is(err, ErrInvalidOrExpiredMagicLink) {
		t.Fatalf("expected ErrInvalidOrExpiredMagicLink, got %v", err)
	}

	_, _, _, err = CompleteMagicLink("missing-token", db, context.Background())
	if !errors.Is(err, ErrInvalidOrExpiredMagicLink) {
		t.Fatalf("expected ErrInvalidOrExpiredMagicLink for unknown token, got %v", err)
	}
}

func TestBeginMagicLogin_InvalidEmail(t *testing.T) {
	db := setupAuthTestDB(t)
	err := BeginMagicLogin("not-an-email", db, context.Background())
	if !errors.Is(err, ErrInvalidEmail) {
		t.Fatalf("expected ErrInvalidEmail, got %v", err)
	}

	var count int
	err = db.QueryRow(`SELECT COUNT(*) FROM email_magic_links`).Scan(&count)
	if err != nil {
		t.Fatalf("count magic links: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no rows inserted for invalid email, found %d", count)
	}
}
