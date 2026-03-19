package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/atombasedev/atombase/config"
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

type CreateOrganizationParams struct {
	ID         string
	Name       string
	Definition string
	OwnerID    string
	MaxMembers *int
	Metadata   json.RawMessage
}

type createOrganizationRequest struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Definition string          `json:"definition"`
	OwnerID    string          `json:"ownerId,omitempty"`
	MaxMembers *int            `json:"maxMembers,omitempty"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`
}

type updateOrganizationRequest struct {
	Name       *string         `json:"name,omitempty"`
	MaxMembers *int            `json:"maxMembers,omitempty"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`
}

type transferOrganizationOwnershipRequest struct {
	UserID string `json:"userId"`
}

func (api *API) handleListOrganizations(w http.ResponseWriter, r *http.Request) {
	actor, err := api.getOrgActor(r)
	if err != nil {
		tools.RespErr(w, tools.UnauthorizedErr("invalid session"))
		return
	}
	orgs, err := api.listOrganizations(r.Context(), actor)
	if err != nil {
		tools.RespErr(w, err)
		return
	}
	tools.RespondJSON(w, http.StatusOK, orgs)
}

func (api *API) handleCreateOrganization(w http.ResponseWriter, r *http.Request) {
	actor, err := api.getOrgActor(r)
	if err != nil {
		tools.RespErr(w, tools.UnauthorizedErr("invalid session"))
		return
	}
	var req createOrganizationRequest
	if err := tools.DecodeJSON(r.Body, &req); err != nil {
		tools.RespErr(w, tools.ErrInvalidJSON)
		return
	}
	org, err := api.createOrganization(r.Context(), actor, req)
	if err != nil {
		tools.RespErr(w, err)
		return
	}
	tools.RespondJSON(w, http.StatusCreated, org)
}

func (api *API) handleGetOrganization(w http.ResponseWriter, r *http.Request) {
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
	orgCtx, err := api.getOrganizationContext(r.Context(), actor, orgID)
	if err != nil {
		tools.RespErr(w, err)
		return
	}
	tools.RespondJSON(w, http.StatusOK, orgCtx)
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

func (api *API) createOrganization(ctx context.Context, actor *orgActor, req createOrganizationRequest) (*Organization, error) {
	req.ID = strings.TrimSpace(req.ID)
	req.Name = strings.TrimSpace(req.Name)
	req.Definition = strings.TrimSpace(req.Definition)
	req.OwnerID = strings.TrimSpace(req.OwnerID)
	if req.ID == "" {
		return nil, tools.InvalidRequestErr("id is required")
	}
	if code, msg, _ := tools.ValidateResourceName(req.ID); code != "" {
		return nil, tools.InvalidRequestErr(msg)
	}
	if req.Name == "" {
		return nil, tools.InvalidRequestErr("name is required")
	}
	if req.Definition == "" {
		return nil, tools.InvalidRequestErr("definition is required")
	}
	if len(req.Metadata) > 0 && !json.Valid(req.Metadata) {
		return nil, tools.InvalidRequestErr("metadata must be valid JSON")
	}
	ownerID := req.OwnerID
	if actor.IsService {
		if ownerID == "" {
			return nil, tools.InvalidRequestErr("ownerId is required")
		}
	} else {
		if ownerID != "" && ownerID != actor.UserID {
			return nil, tools.UnauthorizedErr("ownerId must match the authenticated user")
		}
		ownerID = actor.UserID
		if limit := config.Cfg.MaxOrganizationsPerUser; limit > 0 {
			count, err := api.countOwnedOrganizations(ctx, ownerID)
			if err != nil {
				return nil, err
			}
			if count >= limit {
				return nil, tools.InvalidRequestErr("organization creation limit reached")
			}
		}
	}

	org, err := api.store.CreateOrganization(ctx, CreateOrganizationParams{
		ID:         req.ID,
		Name:       req.Name,
		Definition: req.Definition,
		OwnerID:    ownerID,
		MaxMembers: req.MaxMembers,
		Metadata:   req.Metadata,
	})
	if err != nil {
		return nil, err
	}
	return org, nil
}

func (api *API) countOwnedOrganizations(ctx context.Context, ownerID string) (int, error) {
	if api == nil || api.db == nil {
		return 0, errors.New("auth api not initialized")
	}
	var count int
	if err := api.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM atombase_organizations
		WHERE owner_id = ?
	`, ownerID).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (api *API) listOrganizations(ctx context.Context, actor *orgActor) ([]Organization, error) {
	if api == nil || api.db == nil {
		return nil, errors.New("auth api not initialized")
	}
	rows, err := api.db.QueryContext(ctx, `
		SELECT id, name, owner_id, max_members, metadata, created_at, updated_at
		FROM atombase_organizations
		ORDER BY created_at ASC, id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var orgs []Organization
	for rows.Next() {
		org, err := scanOrganization(rows)
		if err != nil {
			return nil, err
		}
		orgs = append(orgs, *org)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if actor.IsService {
		if orgs == nil {
			orgs = []Organization{}
		}
		return orgs, nil
	}
	var visible []Organization
	for _, org := range orgs {
		db, _, err := api.connOrganizationTenant(ctx, actor, org.ID)
		if err != nil {
			continue
		}
		_, memberErr := lookupOrganizationMemberRole(ctx, db, actor.UserID)
		_ = db.Close()
		if memberErr == nil {
			visible = append(visible, org)
		}
	}
	if visible == nil {
		visible = []Organization{}
	}
	return visible, nil
}

func (api *API) getOrganization(ctx context.Context, actor *orgActor, organizationID string) (*Organization, error) {
	if actor.IsService {
		return api.getOrganizationRecord(ctx, organizationID)
	}
	db, _, err := api.connOrganizationTenant(ctx, actor, organizationID)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if _, err := lookupOrganizationMemberRole(ctx, db, actor.UserID); err != nil {
		return nil, tools.UnauthorizedErr("organization access denied")
	}
	return api.getOrganizationRecord(ctx, organizationID)
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

	org, err := scanOrganization(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, tools.ErrDatabaseNotFound
		}
		return nil, err
	}
	return org, nil
}

type organizationScanner interface {
	Scan(dest ...any) error
}

func scanOrganization(row organizationScanner) (*Organization, error) {
	var org Organization
	var maxMembers sql.NullInt64
	var metadata string
	if err := row.Scan(&org.ID, &org.Name, &org.OwnerID, &maxMembers, &metadata, &org.CreatedAt, &org.UpdatedAt); err != nil {
		return nil, err
	}
	if maxMembers.Valid {
		value := int(maxMembers.Int64)
		org.MaxMembers = &value
	}
	org.Metadata = json.RawMessage(metadata)
	return &org, nil
}
