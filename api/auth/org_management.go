package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/atombasedev/atombase/tools"
)

type Organization struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	OwnerID    string          `json:"ownerId"`
	MaxMembers *int            `json:"maxMembers,omitempty"`
	Metadata   json.RawMessage `json:"metadata"`
	CreatedAt  string          `json:"createdAt"`
	UpdatedAt  string          `json:"updatedAt"`
}

type updateOrganizationRequest struct {
	Name       *string         `json:"name,omitempty"`
	MaxMembers *int            `json:"maxMembers,omitempty"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`
}

type transferOrganizationOwnershipRequest struct {
	UserID string `json:"userId"`
}

func (api *API) handleUpdateOrganization(w http.ResponseWriter, r *http.Request) {
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
	var req updateOrganizationRequest
	if err := tools.DecodeJSON(r.Body, &req); err != nil {
		tools.RespErr(w, tools.ErrInvalidJSON)
		return
	}
	org, err := api.updateOrganization(r.Context(), actor, orgID, req)
	if err != nil {
		tools.RespErr(w, err)
		return
	}
	tools.RespondJSON(w, http.StatusOK, org)
}

func (api *API) handleDeleteOrganization(w http.ResponseWriter, r *http.Request) {
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
	if err := api.deleteOrganization(r.Context(), actor, orgID); err != nil {
		tools.RespErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (api *API) handleTransferOrganizationOwnership(w http.ResponseWriter, r *http.Request) {
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
	var req transferOrganizationOwnershipRequest
	if err := tools.DecodeJSON(r.Body, &req); err != nil {
		tools.RespErr(w, tools.ErrInvalidJSON)
		return
	}
	org, err := api.transferOrganizationOwnership(r.Context(), actor, orgID, req)
	if err != nil {
		tools.RespErr(w, err)
		return
	}
	tools.RespondJSON(w, http.StatusOK, org)
}

func (api *API) updateOrganization(ctx context.Context, actor *orgActor, organizationID string, req updateOrganizationRequest) (*Organization, error) {
	if req.Name == nil && req.MaxMembers == nil && len(req.Metadata) == 0 {
		return nil, tools.InvalidRequestErr("name, maxMembers, or metadata is required")
	}
	if req.Name != nil {
		trimmed := strings.TrimSpace(*req.Name)
		if trimmed == "" {
			return nil, tools.InvalidRequestErr("name cannot be empty")
		}
		req.Name = &trimmed
	}
	if len(req.Metadata) > 0 && !json.Valid(req.Metadata) {
		return nil, tools.InvalidRequestErr("metadata must be valid JSON")
	}

	_, _, err := api.authorizeOrganizationAction(ctx, actor, organizationID, "updateOrg")
	if err != nil {
		return nil, err
	}

	name := sql.NullString{}
	if req.Name != nil {
		name.Valid = true
		name.String = *req.Name
	}
	maxMembers := sql.NullInt64{}
	if req.MaxMembers != nil {
		maxMembers.Valid = true
		maxMembers.Int64 = int64(*req.MaxMembers)
	}
	metadata := sql.NullString{}
	if len(req.Metadata) > 0 {
		metadata.Valid = true
		metadata.String = string(req.Metadata)
	}
	now := time.Now().UTC().Format(time.RFC3339)

	if _, err := api.db.ExecContext(ctx, `
		UPDATE atombase_organizations
		SET name = COALESCE(?, name),
		    max_members = COALESCE(?, max_members),
		    metadata = COALESCE(?, metadata),
		    updated_at = ?
		WHERE id = ?
	`, name, maxMembers, metadata, now, organizationID); err != nil {
		return nil, err
	}

	return api.getOrganizationRecord(ctx, organizationID)
}

func (api *API) deleteOrganization(ctx context.Context, actor *orgActor, organizationID string) error {
	if _, _, err := api.authorizeOrganizationAction(ctx, actor, organizationID, "deleteOrg"); err != nil {
		return err
	}
	return api.store.DeleteOrganization(ctx, organizationID)
}

func (api *API) transferOrganizationOwnership(ctx context.Context, actor *orgActor, organizationID string, req transferOrganizationOwnershipRequest) (*Organization, error) {
	req.UserID = strings.TrimSpace(req.UserID)
	if req.UserID == "" {
		return nil, tools.InvalidRequestErr("userId is required")
	}
	org, tenantDB, err := api.authorizeOrganizationAction(ctx, actor, organizationID, "transferOwnership")
	if err != nil {
		return nil, err
	}
	defer tenantDB.Close()

	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := tenantDB.ExecContext(ctx, `
		INSERT INTO atombase_membership (user_id, role, status, created_at)
		VALUES (?, 'owner', 'active', ?)
		ON CONFLICT(user_id) DO UPDATE SET
			role = 'owner',
			status = 'active'
	`, req.UserID, now); err != nil {
		return nil, err
	}

	if _, err := api.db.ExecContext(ctx, `
		UPDATE atombase_organizations
		SET owner_id = ?, updated_at = ?
		WHERE id = ?
	`, req.UserID, now, org.ID); err != nil {
		return nil, err
	}

	return api.getOrganizationRecord(ctx, organizationID)
}

func (api *API) authorizeOrganizationAction(ctx context.Context, actor *orgActor, organizationID, action string) (*Organization, *sql.DB, error) {
	tenantDB, management, err := api.connOrganizationTenant(ctx, actor, organizationID)
	if err != nil {
		return nil, nil, err
	}
	if actor.IsService {
		org, err := api.getOrganizationRecord(ctx, organizationID)
		if err != nil {
			tenantDB.Close()
			return nil, nil, err
		}
		return org, tenantDB, nil
	}
	actorRole, err := lookupOrganizationMemberRole(ctx, tenantDB, actor.UserID)
	if err != nil {
		tenantDB.Close()
		return nil, nil, err
	}
	if !managementAllows(management, actorRole, action, "") {
		tenantDB.Close()
		return nil, nil, tools.UnauthorizedErr("organization action is not allowed")
	}
	org, err := api.getOrganizationRecord(ctx, organizationID)
	if err != nil {
		tenantDB.Close()
		return nil, nil, err
	}
	return org, tenantDB, nil
}

func (api *API) getOrganizationRecord(ctx context.Context, organizationID string) (*Organization, error) {
	if api == nil || api.db == nil {
		return nil, errors.New("auth api not initialized")
	}
	row := api.db.QueryRowContext(ctx, `
		SELECT id, name, owner_id, max_members, metadata, created_at, updated_at
		FROM atombase_organizations
		WHERE id = ?
	`, organizationID)

	var org Organization
	var maxMembers sql.NullInt64
	var metadata string
	if err := row.Scan(&org.ID, &org.Name, &org.OwnerID, &maxMembers, &metadata, &org.CreatedAt, &org.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, tools.ErrDatabaseNotFound
		}
		return nil, err
	}
	if maxMembers.Valid {
		value := int(maxMembers.Int64)
		org.MaxMembers = &value
	}
	org.Metadata = json.RawMessage(metadata)
	return &org, nil
}
