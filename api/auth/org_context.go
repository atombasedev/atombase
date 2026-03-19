package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/atombasedev/atombase/tools"
)

type OrganizationContext struct {
	Organization *Organization        `json:"organization"`
	Member       *OrganizationMember  `json:"member,omitempty"`
	Members      []OrganizationMember `json:"members"`
	Invites      []OrganizationInvite `json:"invites"`
}

func (api *API) getOrganizationContext(ctx context.Context, actor *orgActor, organizationID string) (*OrganizationContext, error) {
	org, err := api.getOrganization(ctx, actor, organizationID)
	if err != nil {
		return nil, err
	}

	db, _, err := api.connOrganizationTenant(ctx, actor, organizationID)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	member, members, invites, err := queryOrganizationContext(ctx, db, actor)
	if err != nil {
		return nil, err
	}
	if err := api.populateOrganizationMemberEmails(ctx, members); err != nil {
		return nil, err
	}
	if member != nil {
		member.Email = ""
		for _, listedMember := range members {
			if listedMember.UserID == member.UserID {
				member.Email = listedMember.Email
				break
			}
		}
	}

	return &OrganizationContext{
		Organization: org,
		Member:       member,
		Members:      members,
		Invites:      invites,
	}, nil
}

func queryOrganizationContext(ctx context.Context, db *sql.DB, actor *orgActor) (*OrganizationMember, []OrganizationMember, []OrganizationInvite, error) {
	var (
		memberJSON  sql.NullString
		membersJSON string
		invitesJSON string
		err         error
	)

	if actor.IsService {
		err = db.QueryRowContext(ctx, `
			WITH ordered_members AS (
				SELECT user_id, role, status, created_at
				FROM atombase_membership
				ORDER BY created_at ASC, user_id ASC
			),
			ordered_invites AS (
				SELECT id, email, role, invited_by, expires_at, created_at
				FROM atombase_invites
				WHERE expires_at > ?
				ORDER BY created_at ASC, id ASC
			)
			SELECT
				NULL,
				COALESCE((
					SELECT json_group_array(json_object(
						'userId', user_id,
						'role', role,
						'status', status,
						'createdAt', created_at
					))
					FROM ordered_members
				), '[]'),
				COALESCE((
					SELECT json_group_array(json_object(
						'id', id,
						'email', email,
						'role', role,
						'invitedBy', invited_by,
						'expiresAt', expires_at,
						'createdAt', created_at
					))
					FROM ordered_invites
				), '[]')
		`, time.Now().UTC().Format(time.RFC3339)).Scan(&memberJSON, &membersJSON, &invitesJSON)
	} else {
		err = db.QueryRowContext(ctx, `
			WITH actor AS (
				SELECT user_id, role, status, created_at
				FROM atombase_membership
				WHERE user_id = ? AND status = 'active'
			),
			ordered_members AS (
				SELECT user_id, role, status, created_at
				FROM atombase_membership
				WHERE EXISTS (SELECT 1 FROM actor)
				ORDER BY created_at ASC, user_id ASC
			),
			ordered_invites AS (
				SELECT id, email, role, invited_by, expires_at, created_at
				FROM atombase_invites
				WHERE expires_at > ?
				  AND EXISTS (SELECT 1 FROM actor)
				ORDER BY created_at ASC, id ASC
			)
			SELECT
				(
					SELECT json_object(
						'userId', user_id,
						'role', role,
						'status', status,
						'createdAt', created_at
					)
					FROM actor
				),
				COALESCE((
					SELECT json_group_array(json_object(
						'userId', user_id,
						'role', role,
						'status', status,
						'createdAt', created_at
					))
					FROM ordered_members
				), '[]'),
				COALESCE((
					SELECT json_group_array(json_object(
						'id', id,
						'email', email,
						'role', role,
						'invitedBy', invited_by,
						'expiresAt', expires_at,
						'createdAt', created_at
					))
					FROM ordered_invites
				), '[]')
		`, actor.UserID, time.Now().UTC().Format(time.RFC3339)).Scan(&memberJSON, &membersJSON, &invitesJSON)
	}
	if err != nil {
		return nil, nil, nil, err
	}

	var member *OrganizationMember
	if !actor.IsService {
		if !memberJSON.Valid || memberJSON.String == "" {
			return nil, nil, nil, tools.UnauthorizedErr("organization membership is required")
		}
		member = &OrganizationMember{}
		if err := json.Unmarshal([]byte(memberJSON.String), member); err != nil {
			return nil, nil, nil, err
		}
	}

	members := []OrganizationMember{}
	if membersJSON != "" {
		if err := json.Unmarshal([]byte(membersJSON), &members); err != nil {
			return nil, nil, nil, err
		}
	}

	invites := []OrganizationInvite{}
	if invitesJSON != "" {
		if err := json.Unmarshal([]byte(invitesJSON), &invites); err != nil {
			return nil, nil, nil, err
		}
	}

	return member, members, invites, nil
}
