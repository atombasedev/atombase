package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/atombasedev/atombase/config"
	"github.com/atombasedev/atombase/tools"
	_ "github.com/mattn/go-sqlite3"
	_ "github.com/tursodatabase/libsql-client-go/libsql"
)

type OrganizationMember struct {
	UserID    string `json:"userId"`
	Email     string `json:"email"`
	Role      string `json:"role"`
	Status    string `json:"status"`
	CreatedAt string `json:"createdAt"`
}

type createOrganizationMemberRequest struct {
	UserID string `json:"userId"`
	Role   string `json:"role"`
	Status string `json:"status,omitempty"`
}

type updateOrganizationMemberRequest struct {
	Role   *string `json:"role,omitempty"`
	Status *string `json:"status,omitempty"`
}

type tenantOpener func(databaseID, authToken string) (*sql.DB, error)

var openOrganizationTenantDB tenantOpener = func(databaseID, authToken string) (*sql.DB, error) {
	org := config.Cfg.TursoOrganization
	if org == "" {
		return nil, errors.New("TURSO_ORGANIZATION environment variable is not set but is required to access external databases")
	}
	if authToken == "" {
		return nil, errors.New("database has no auth token configured")
	}
	return sql.Open("libsql", fmt.Sprintf("libsql://%s-%s.turso.io?authToken=%s", databaseID, org, authToken))
}

func (api *API) handleListOrganizationMembers(w http.ResponseWriter, r *http.Request) {
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
	members, err := api.listOrganizationMembers(r.Context(), actor, orgID)
	if err != nil {
		tools.RespErr(w, err)
		return
	}
	tools.RespondJSON(w, http.StatusOK, members)
}

func (api *API) handleCreateOrganizationMember(w http.ResponseWriter, r *http.Request) {
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

	var req createOrganizationMemberRequest
	if err := tools.DecodeJSON(r.Body, &req); err != nil {
		tools.RespErr(w, tools.ErrInvalidJSON)
		return
	}
	member, err := api.createOrganizationMember(r.Context(), actor, orgID, req)
	if err != nil {
		tools.RespErr(w, err)
		return
	}
	tools.RespondJSON(w, http.StatusCreated, member)
}

func (api *API) handleUpdateOrganizationMember(w http.ResponseWriter, r *http.Request) {
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
	userID := r.PathValue("userID")
	if userID == "" {
		tools.RespErr(w, tools.InvalidRequestErr("user id is required"))
		return
	}

	var req updateOrganizationMemberRequest
	if err := tools.DecodeJSON(r.Body, &req); err != nil {
		tools.RespErr(w, tools.ErrInvalidJSON)
		return
	}
	member, err := api.updateOrganizationMember(r.Context(), actor, orgID, userID, req)
	if err != nil {
		tools.RespErr(w, err)
		return
	}
	tools.RespondJSON(w, http.StatusOK, member)
}

func (api *API) handleDeleteOrganizationMember(w http.ResponseWriter, r *http.Request) {
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
	userID := r.PathValue("userID")
	if userID == "" {
		tools.RespErr(w, tools.InvalidRequestErr("user id is required"))
		return
	}
	if err := api.deleteOrganizationMember(r.Context(), actor, orgID, userID); err != nil {
		tools.RespErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (api *API) listOrganizationMembers(ctx context.Context, actor *orgActor, organizationID string) ([]OrganizationMember, error) {
	db, _, err := api.connOrganizationTenant(ctx, actor, organizationID)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	var rows *sql.Rows
	if actor.IsService {
		rows, err = db.QueryContext(ctx, `
			SELECT user_id, role, status, created_at
			FROM atombase_membership
			ORDER BY created_at ASC, user_id ASC
		`)
	} else {
		rows, err = db.QueryContext(ctx, `
			WITH actor AS (
				SELECT 1 AS allowed
				FROM atombase_membership
				WHERE user_id = ? AND status = 'active'
			),
			members AS (
				SELECT user_id, role, status, created_at
				FROM atombase_membership
				WHERE EXISTS (SELECT 1 FROM actor)
				ORDER BY created_at ASC, user_id ASC
			)
			SELECT user_id, role, status, created_at
			FROM members
		`, actor.UserID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var members []OrganizationMember
	for rows.Next() {
		var member OrganizationMember
		if err := rows.Scan(&member.UserID, &member.Role, &member.Status, &member.CreatedAt); err != nil {
			return nil, err
		}
		members = append(members, member)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if members == nil {
		return nil, tools.UnauthorizedErr("organization membership is required")
	}
	if err := api.populateOrganizationMemberEmails(ctx, members); err != nil {
		return nil, err
	}
	return members, nil
}

func (api *API) createOrganizationMember(ctx context.Context, actor *orgActor, organizationID string, req createOrganizationMemberRequest) (*OrganizationMember, error) {
	req.UserID = strings.TrimSpace(req.UserID)
	req.Role = strings.TrimSpace(req.Role)
	req.Status = strings.TrimSpace(req.Status)
	if req.UserID == "" {
		return nil, tools.InvalidRequestErr("userId is required")
	}
	if req.Role == "" {
		return nil, tools.InvalidRequestErr("role is required")
	}
	if req.Status == "" {
		req.Status = "active"
	}

	db, management, err := api.connOrganizationTenant(ctx, actor, organizationID)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	if !actor.IsService {
		actorRole, err := lookupOrganizationMemberRole(ctx, db, actor.UserID)
		if err != nil {
			return nil, err
		}
		if !managementAllows(management, actorRole, "invite", req.Role) {
			return nil, tools.UnauthorizedErr("organization member creation is not allowed")
		}
	}

	var row *sql.Row
	if actor.IsService {
		row = db.QueryRowContext(ctx, `
			INSERT INTO atombase_membership (user_id, role, status)
			VALUES (?, ?, ?)
			ON CONFLICT(user_id) DO NOTHING
			RETURNING user_id, role, status, created_at
		`, req.UserID, req.Role, req.Status)
	} else {
		row = db.QueryRowContext(ctx, `
			INSERT INTO atombase_membership (user_id, role, status)
			SELECT ?, ?, ?
			WHERE EXISTS (
				SELECT 1
				FROM atombase_membership actor
				WHERE actor.user_id = ?
				  AND actor.status = 'active'
			)
			ON CONFLICT(user_id) DO NOTHING
			RETURNING user_id, role, status, created_at
		`, req.UserID, req.Role, req.Status, actor.UserID)
	}

	var member OrganizationMember
	if err := row.Scan(&member.UserID, &member.Role, &member.Status, &member.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, tools.UnauthorizedErr("organization member creation is not allowed")
		}
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return nil, tools.InvalidRequestErr("member already exists")
		}
		return nil, err
	}
	return &member, nil
}

func (api *API) updateOrganizationMember(ctx context.Context, actor *orgActor, organizationID, memberUserID string, req updateOrganizationMemberRequest) (*OrganizationMember, error) {
	if req.Role == nil && req.Status == nil {
		return nil, tools.InvalidRequestErr("role or status is required")
	}
	if req.Role != nil {
		trimmed := strings.TrimSpace(*req.Role)
		if trimmed == "" {
			return nil, tools.InvalidRequestErr("role cannot be empty")
		}
		req.Role = &trimmed
	}
	if req.Status != nil {
		trimmed := strings.TrimSpace(*req.Status)
		if trimmed == "" {
			return nil, tools.InvalidRequestErr("status cannot be empty")
		}
		req.Status = &trimmed
	}

	db, management, err := api.connOrganizationTenant(ctx, actor, organizationID)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	targetRole, err := lookupOrganizationMemberRole(ctx, db, memberUserID)
	if err != nil {
		return nil, err
	}
	if !actor.IsService {
		actorRole, err := lookupOrganizationMemberRole(ctx, db, actor.UserID)
		if err != nil {
			return nil, err
		}
		if req.Role != nil && !managementAllows(management, actorRole, "assignRole", *req.Role) {
			return nil, tools.UnauthorizedErr("organization member update is not allowed")
		}
		if req.Role == nil && req.Status != nil && !managementAllows(management, actorRole, "assignRole", targetRole) {
			return nil, tools.UnauthorizedErr("organization member update is not allowed")
		}
	}

	newRole := sql.NullString{}
	newStatus := sql.NullString{}
	if req.Role != nil {
		newRole.Valid = true
		newRole.String = *req.Role
	}
	if req.Status != nil {
		newStatus.Valid = true
		newStatus.String = *req.Status
	}

	var row *sql.Row
	if actor.IsService {
		row = db.QueryRowContext(ctx, `
			UPDATE atombase_membership
			SET role = COALESCE(?, role),
			    status = COALESCE(?, status)
			WHERE user_id = ?
			  AND (
			    (SELECT role FROM atombase_membership WHERE user_id = ?) != 'owner'
			    OR (
			      COALESCE(?, (SELECT role FROM atombase_membership WHERE user_id = ?)) = 'owner'
			      AND COALESCE(?, (SELECT status FROM atombase_membership WHERE user_id = ?)) = 'active'
			    )
			    OR (
			      SELECT COUNT(*)
			      FROM atombase_membership
			      WHERE role = 'owner' AND status = 'active'
			    ) > 1
			  )
			RETURNING user_id, role, status, created_at
		`, newRole, newStatus, memberUserID, memberUserID, newRole, memberUserID, newStatus, memberUserID)
	} else {
		row = db.QueryRowContext(ctx, `
			UPDATE atombase_membership
			SET role = COALESCE(?, role),
			    status = COALESCE(?, status)
			WHERE user_id = ?
			  AND EXISTS (
			    SELECT 1
			    FROM atombase_membership actor
			    WHERE actor.user_id = ?
			      AND actor.status = 'active'
			  )
			  AND (
			    (SELECT role FROM atombase_membership WHERE user_id = ?) != 'owner'
			    OR (
			      COALESCE(?, (SELECT role FROM atombase_membership WHERE user_id = ?)) = 'owner'
			      AND COALESCE(?, (SELECT status FROM atombase_membership WHERE user_id = ?)) = 'active'
			    )
			    OR (
			      SELECT COUNT(*)
			      FROM atombase_membership
			      WHERE role = 'owner' AND status = 'active'
			    ) > 1
			  )
			RETURNING user_id, role, status, created_at
		`, newRole, newStatus, memberUserID, actor.UserID, memberUserID, newRole, memberUserID, newStatus, memberUserID)
	}

	var member OrganizationMember
	if err := row.Scan(&member.UserID, &member.Role, &member.Status, &member.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, tools.UnauthorizedErr("organization member update is not allowed")
		}
		return nil, err
	}
	return &member, nil
}

func (api *API) deleteOrganizationMember(ctx context.Context, actor *orgActor, organizationID, memberUserID string) error {
	db, management, err := api.connOrganizationTenant(ctx, actor, organizationID)
	if err != nil {
		return err
	}
	defer db.Close()
	targetRole, err := lookupOrganizationMemberRole(ctx, db, memberUserID)
	if err != nil {
		return err
	}
	if !actor.IsService {
		actorRole, err := lookupOrganizationMemberRole(ctx, db, actor.UserID)
		if err != nil {
			return err
		}
		if !managementAllows(management, actorRole, "removeMember", targetRole) {
			return tools.UnauthorizedErr("organization member deletion is not allowed")
		}
	}

	var res sql.Result
	if actor.IsService {
		res, err = db.ExecContext(ctx, `
			DELETE FROM atombase_membership
			WHERE user_id = ?
			  AND (
			    (SELECT role FROM atombase_membership WHERE user_id = ?) != 'owner'
			    OR (
			      (
			        SELECT COUNT(*)
			        FROM atombase_membership
			        WHERE role = 'owner' AND status = 'active'
			      ) > 1
			    )
			  )
		`, memberUserID, memberUserID)
	} else {
		res, err = db.ExecContext(ctx, `
			DELETE FROM atombase_membership
			WHERE user_id = ?
			  AND EXISTS (
			    SELECT 1
			    FROM atombase_membership actor
			    WHERE actor.user_id = ?
			      AND actor.status = 'active'
			  )
			  AND (
			    (SELECT role FROM atombase_membership WHERE user_id = ?) != 'owner'
			    OR (
			      (
			        SELECT COUNT(*)
			        FROM atombase_membership
			        WHERE role = 'owner' AND status = 'active'
			      ) > 1
			    )
			  )
		`, memberUserID, actor.UserID, memberUserID)
	}
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return tools.UnauthorizedErr("organization member deletion is not allowed")
	}
	return nil
}

func lookupOrganizationMemberRole(ctx context.Context, db *sql.DB, userID string) (string, error) {
	var role string
	err := db.QueryRowContext(ctx, `
		SELECT role
		FROM atombase_membership
		WHERE user_id = ? AND status = 'active'
	`, userID).Scan(&role)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", tools.UnauthorizedErr("organization membership is required")
		}
		return "", err
	}
	return role, nil
}

func managementAllows(management ManagementMap, actorRole string, action string, targetRole string) bool {
	policy, ok := management[actorRole]
	if !ok {
		return false
	}
	switch action {
	case "invite":
		return permissionAllows(policy.Invite, targetRole)
	case "assignRole":
		return permissionAllows(policy.AssignRole, targetRole)
	case "removeMember":
		return permissionAllows(policy.RemoveMember, targetRole)
	case "updateOrg":
		return policy.UpdateOrg
	case "deleteOrg":
		return policy.DeleteOrg
	case "transferOwnership":
		return policy.TransferOwnership
	default:
		return false
	}
}

func permissionAllows(permission ManagementPermission, targetRole string) bool {
	if permission.Any {
		return true
	}
	if len(permission.Roles) == 0 {
		return false
	}
	for _, role := range permission.Roles {
		if role == targetRole {
			return true
		}
	}
	return false
}

func (api *API) connOrganizationTenant(ctx context.Context, actor *orgActor, organizationID string) (*sql.DB, ManagementMap, error) {
	if api == nil || api.store == nil {
		return nil, nil, errors.New("auth api not initialized")
	}
	databaseID, authToken, management, err := api.store.LookupOrganizationAuthz(ctx, organizationID)
	if err != nil {
		if actor != nil && !actor.IsService {
			return nil, nil, tools.UnauthorizedErr("organization access denied")
		}
		return nil, nil, err
	}
	db, err := openOrganizationTenantDB(databaseID, authToken)
	if err != nil {
		return nil, nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, nil, err
	}
	return db, management, nil
}

func (api *API) populateOrganizationMemberEmails(ctx context.Context, members []OrganizationMember) error {
	if api == nil || api.db == nil || len(members) == 0 {
		return nil
	}

	placeholders := make([]string, 0, len(members))
	args := make([]any, 0, len(members))
	for _, member := range members {
		placeholders = append(placeholders, "?")
		args = append(args, member.UserID)
	}

	rows, err := api.db.QueryContext(ctx, `
		SELECT id, email
		FROM atombase_users
		WHERE id IN (`+strings.Join(placeholders, ",")+`)
	`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	emails := make(map[string]string, len(members))
	for rows.Next() {
		var userID, email string
		if err := rows.Scan(&userID, &email); err != nil {
			return err
		}
		emails[userID] = email
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for i := range members {
		members[i].Email = emails[members[i].UserID]
	}
	return nil
}
