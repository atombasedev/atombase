package auth

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/atombasedev/atombase/tools"
)

type OrganizationInvite struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	Role      string `json:"role"`
	InvitedBy string `json:"invitedBy"`
	ExpiresAt string `json:"expiresAt"`
	CreatedAt string `json:"createdAt"`
}

type createOrganizationInviteRequest struct {
	ID        string `json:"id,omitempty"`
	Email     string `json:"email"`
	Role      string `json:"role"`
	ExpiresAt string `json:"expiresAt,omitempty"`
}

func (api *API) handleListOrganizationInvites(w http.ResponseWriter, r *http.Request) {
	actor, err := api.getOrgActor(r)
	if err != nil {
		tools.RespErr(w, tools.UnauthorizedErr("invalid session"))
		return
	}
	orgID := r.PathValue("orgID")
	if orgID == "" {
		tools.RespErr(w, tools.InvalidRequestErr("organization id is required"))
		return
	}
	invites, err := api.listOrganizationInvites(r.Context(), actor, orgID)
	if err != nil {
		tools.RespErr(w, err)
		return
	}
	tools.RespondJSON(w, http.StatusOK, invites)
}

func (api *API) handleCreateOrganizationInvite(w http.ResponseWriter, r *http.Request) {
	actor, err := api.getOrgActor(r)
	if err != nil {
		tools.RespErr(w, tools.UnauthorizedErr("invalid session"))
		return
	}
	orgID := r.PathValue("orgID")
	if orgID == "" {
		tools.RespErr(w, tools.InvalidRequestErr("organization id is required"))
		return
	}
	var req createOrganizationInviteRequest
	if err := tools.DecodeJSON(r.Body, &req); err != nil {
		tools.RespErr(w, tools.ErrInvalidJSON)
		return
	}
	invite, err := api.createOrganizationInvite(r.Context(), actor, orgID, req)
	if err != nil {
		tools.RespErr(w, err)
		return
	}
	tools.RespondJSON(w, http.StatusCreated, invite)
}

func (api *API) handleDeleteOrganizationInvite(w http.ResponseWriter, r *http.Request) {
	actor, err := api.getOrgActor(r)
	if err != nil {
		tools.RespErr(w, tools.UnauthorizedErr("invalid session"))
		return
	}
	orgID := r.PathValue("orgID")
	inviteID := r.PathValue("inviteID")
	if orgID == "" || inviteID == "" {
		tools.RespErr(w, tools.InvalidRequestErr("organization id and invite id are required"))
		return
	}
	if err := api.deleteOrganizationInvite(r.Context(), actor, orgID, inviteID); err != nil {
		tools.RespErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (api *API) handleAcceptOrganizationInvite(w http.ResponseWriter, r *http.Request) {
	actor, err := api.getOrgActor(r)
	if err != nil || actor.IsService || actor.UserID == "" {
		tools.RespErr(w, tools.UnauthorizedErr("invalid session"))
		return
	}
	orgID := r.PathValue("orgID")
	inviteID := r.PathValue("inviteID")
	if orgID == "" || inviteID == "" {
		tools.RespErr(w, tools.InvalidRequestErr("organization id and invite id are required"))
		return
	}
	member, err := api.acceptOrganizationInvite(r.Context(), actor, orgID, inviteID)
	if err != nil {
		tools.RespErr(w, err)
		return
	}
	tools.RespondJSON(w, http.StatusOK, member)
}

func (api *API) listOrganizationInvites(ctx context.Context, actor *orgActor, organizationID string) ([]OrganizationInvite, error) {
	db, _, err := api.connOrganizationTenant(ctx, actor, organizationID)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	if !actor.IsService {
		if _, err := lookupOrganizationMemberRole(ctx, db, actor.UserID); err != nil {
			return nil, err
		}
	}

	rows, err := db.QueryContext(ctx, `
		SELECT id, email, role, invited_by, expires_at, created_at
		FROM atombase_invites
		WHERE expires_at > ?
		ORDER BY created_at ASC, id ASC
	`, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var invites []OrganizationInvite
	for rows.Next() {
		var invite OrganizationInvite
		if err := rows.Scan(&invite.ID, &invite.Email, &invite.Role, &invite.InvitedBy, &invite.ExpiresAt, &invite.CreatedAt); err != nil {
			return nil, err
		}
		invites = append(invites, invite)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if invites == nil {
		invites = []OrganizationInvite{}
	}
	return invites, nil
}

func (api *API) createOrganizationInvite(ctx context.Context, actor *orgActor, organizationID string, req createOrganizationInviteRequest) (*OrganizationInvite, error) {
	req.ID = strings.TrimSpace(req.ID)
	req.Email = NormalizeEmail(req.Email)
	req.Role = strings.TrimSpace(req.Role)
	req.ExpiresAt = strings.TrimSpace(req.ExpiresAt)
	if req.Email == "" {
		return nil, tools.InvalidRequestErr("email is required")
	}
	if req.Role == "" {
		return nil, tools.InvalidRequestErr("role is required")
	}
	if req.ID == "" {
		req.ID = ID128()
	}
	if req.ExpiresAt == "" {
		req.ExpiresAt = time.Now().UTC().Add(7 * 24 * time.Hour).Format(time.RFC3339)
	} else if _, err := time.Parse(time.RFC3339, req.ExpiresAt); err != nil {
		return nil, tools.InvalidRequestErr("expiresAt must be RFC3339")
	}

	db, management, err := api.connOrganizationTenant(ctx, actor, organizationID)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	invitedBy := "service"
	if !actor.IsService {
		actorRole, err := lookupOrganizationMemberRole(ctx, db, actor.UserID)
		if err != nil {
			return nil, err
		}
		if !managementAllows(management, actorRole, "invite", req.Role) {
			return nil, tools.UnauthorizedErr("organization invite creation is not allowed")
		}
		invitedBy = actor.UserID
	}

	org, err := api.getOrganizationRecord(ctx, organizationID)
	if err != nil {
		return nil, err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	row := tx.QueryRowContext(ctx, `
		INSERT INTO atombase_invites (id, email, role, invited_by, expires_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(email) DO UPDATE SET
			id = excluded.id,
			role = excluded.role,
			invited_by = excluded.invited_by,
			expires_at = excluded.expires_at,
			created_at = CURRENT_TIMESTAMP
		RETURNING id, email, role, invited_by, expires_at, created_at
	`, req.ID, req.Email, req.Role, invitedBy, req.ExpiresAt)

	var invite OrganizationInvite
	if err := row.Scan(&invite.ID, &invite.Email, &invite.Role, &invite.InvitedBy, &invite.ExpiresAt, &invite.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, tools.UnauthorizedErr("organization invite creation is not allowed")
		}
		return nil, err
	}

	msg, err := buildOrganizationInviteEmail(org, &invite)
	if err != nil {
		return nil, err
	}
	if err := sendEmailFn(ctx, msg); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &invite, nil
}

func (api *API) deleteOrganizationInvite(ctx context.Context, actor *orgActor, organizationID, inviteID string) error {
	db, management, err := api.connOrganizationTenant(ctx, actor, organizationID)
	if err != nil {
		return err
	}
	defer db.Close()

	var targetRole string
	if err := db.QueryRowContext(ctx, `SELECT role FROM atombase_invites WHERE id = ?`, inviteID).Scan(&targetRole); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			if actor.IsService {
				return tools.ErrDatabaseNotFound
			}
			return tools.UnauthorizedErr("organization invite deletion is not allowed")
		}
		return err
	}

	if !actor.IsService {
		actorRole, err := lookupOrganizationMemberRole(ctx, db, actor.UserID)
		if err != nil {
			return err
		}
		if !managementAllows(management, actorRole, "invite", targetRole) {
			return tools.UnauthorizedErr("organization invite deletion is not allowed")
		}
	}

	res, err := db.ExecContext(ctx, `DELETE FROM atombase_invites WHERE id = ?`, inviteID)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		if actor.IsService {
			return tools.ErrDatabaseNotFound
		}
		return tools.UnauthorizedErr("organization invite deletion is not allowed")
	}
	return nil
}

func (api *API) acceptOrganizationInvite(ctx context.Context, actor *orgActor, organizationID, inviteID string) (*OrganizationMember, error) {
	user, err := GetUserByID(actor.UserID, api.db, ctx)
	if err != nil {
		return nil, err
	}
	db, _, err := api.connOrganizationTenant(ctx, actor, organizationID)
	if err != nil {
		return nil, tools.UnauthorizedErr("organization invite is invalid")
	}
	defer db.Close()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var role, email string
	err = tx.QueryRowContext(ctx, `
		SELECT role, email
		FROM atombase_invites
		WHERE id = ? AND expires_at > ?
	`, inviteID, time.Now().UTC().Format(time.RFC3339)).Scan(&role, &email)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, tools.UnauthorizedErr("organization invite is invalid")
		}
		return nil, err
	}
	if NormalizeEmail(user.Email) != NormalizeEmail(email) {
		return nil, tools.UnauthorizedErr("organization invite is invalid")
	}

	now := time.Now().UTC().Format(time.RFC3339)
	var member OrganizationMember
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO atombase_membership (user_id, role, status, created_at)
		VALUES (?, ?, 'active', ?)
		ON CONFLICT(user_id) DO UPDATE SET
			role = excluded.role,
			status = 'active'
		RETURNING user_id, role, status, created_at
	`, actor.UserID, role, now).Scan(&member.UserID, &member.Role, &member.Status, &member.CreatedAt); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM atombase_invites WHERE id = ?`, inviteID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &member, nil
}
