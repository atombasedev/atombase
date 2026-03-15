package auth

import (
	"context"
	"fmt"
	"net/smtp"
	"strings"

	"github.com/atombasedev/atombase/config"
)

type outboundEmail struct {
	To      string
	Subject string
	Text    string
}

var sendEmailFn = sendEmail

func sendEmail(_ context.Context, msg outboundEmail) error {
	msg.To = strings.TrimSpace(msg.To)
	msg.Subject = strings.TrimSpace(msg.Subject)
	if msg.To == "" {
		return fmt.Errorf("email recipient is required")
	}
	if msg.Subject == "" {
		return fmt.Errorf("email subject is required")
	}

	from := strings.TrimSpace(config.Cfg.SMTPFrom)
	host := strings.TrimSpace(config.Cfg.SMTPHost)
	if from == "" || host == "" {
		fmt.Printf("Outgoing email\nTo: %s\nSubject: %s\n\n%s\n", msg.To, msg.Subject, msg.Text)
		return nil
	}

	addr := fmt.Sprintf("%s:%d", host, config.Cfg.SMTPPort)
	body := strings.ReplaceAll(msg.Text, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\n", "\r\n")
	raw := strings.Join([]string{
		fmt.Sprintf("From: %s", from),
		fmt.Sprintf("To: %s", msg.To),
		fmt.Sprintf("Subject: %s", msg.Subject),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"",
		body,
	}, "\r\n")

	var auth smtp.Auth
	if config.Cfg.SMTPUsername != "" {
		auth = smtp.PlainAuth("", config.Cfg.SMTPUsername, config.Cfg.SMTPPassword, host)
	}

	return smtp.SendMail(addr, auth, from, []string{msg.To}, []byte(raw))
}

func buildOrganizationInviteEmail(org *Organization, invite *OrganizationInvite) outboundEmail {
	appURL := strings.TrimRight(strings.TrimSpace(config.Cfg.AppURL), "/")
	apiURL := strings.TrimRight(strings.TrimSpace(config.Cfg.ApiURL), "/")

	lines := []string{
		fmt.Sprintf("You have been invited to join %s on Atomicbase.", org.Name),
		"",
		fmt.Sprintf("Organization: %s (%s)", org.Name, org.ID),
		fmt.Sprintf("Role: %s", invite.Role),
		fmt.Sprintf("Invite ID: %s", invite.ID),
		fmt.Sprintf("Expires at: %s", invite.ExpiresAt),
		"",
		"Sign in with this email address before accepting the invite.",
	}

	if appURL != "" {
		lines = append(lines, "", fmt.Sprintf("App: %s", appURL))
	}
	if apiURL != "" {
		lines = append(lines,
			"",
			"Acceptance API:",
			fmt.Sprintf("POST %s/auth/orgs/%s/invites/%s/accept", apiURL, org.ID, invite.ID),
		)
	}

	lines = append(lines, "", "If you were not expecting this invitation, you can ignore this email.")

	return outboundEmail{
		To:      invite.Email,
		Subject: fmt.Sprintf("You're invited to %s on Atomicbase", org.Name),
		Text:    strings.Join(lines, "\n"),
	}
}

func buildMagicLinkEmail(email, token string) (outboundEmail, error) {
	url, err := buildMagicLinkURL(token)
	if err != nil {
		return outboundEmail{}, err
	}
	lines := []string{
		"Use this link to sign in to Atomicbase:",
		"",
		url,
		"",
		"This link expires in 15 minutes.",
		"If you did not request this login link, you can ignore this email.",
	}

	if appURL := strings.TrimRight(strings.TrimSpace(config.Cfg.AppURL), "/"); appURL != "" {
		lines = append(lines, "", fmt.Sprintf("App: %s", appURL))
	}

	return outboundEmail{
		To:      NormalizeEmail(email),
		Subject: "Your Atomicbase sign-in link",
		Text:    strings.Join(lines, "\n"),
	}, nil
}
