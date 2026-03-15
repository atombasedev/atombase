package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/atombasedev/atombase/auth"
	"github.com/atombasedev/atombase/config"
	"github.com/atombasedev/atombase/platform"
	"github.com/atombasedev/atombase/primarystore"
)

type authResolver struct {
	store    *primarystore.Store
	platform *platform.API
}

func (r authResolver) DB() *sql.DB {
	return r.store.DB()
}

func (r authResolver) CreateOrganization(ctx context.Context, req auth.CreateOrganizationParams) (*auth.Organization, error) {
	if r.platform == nil {
		return nil, fmt.Errorf("platform api not initialized")
	}
	if _, err := r.platform.CreateDatabase(ctx, platform.CreateDatabaseRequest{
		ID:               req.ID,
		Definition:       req.Definition,
		OrganizationID:   req.ID,
		OrganizationName: req.Name,
		OwnerID:          req.OwnerID,
		MaxMembers:       req.MaxMembers,
	}); err != nil {
		return nil, err
	}
	metadata := "{}"
	if len(req.Metadata) > 0 {
		metadata = string(req.Metadata)
	}
	now := ""
	if err := r.store.DB().QueryRowContext(ctx, `
		UPDATE atombase_organizations
		SET metadata = ?
		WHERE id = ?
		RETURNING created_at
	`, metadata, req.ID).Scan(&now); err != nil {
		return nil, err
	}

	org := &auth.Organization{
		ID:        req.ID,
		Name:      req.Name,
		OwnerID:   req.OwnerID,
		Metadata:  []byte(metadata),
		CreatedAt: now,
		UpdatedAt: now,
	}
	if req.MaxMembers != nil {
		value := *req.MaxMembers
		org.MaxMembers = &value
	}
	return org, nil
}

func (r authResolver) CreateUserDatabase(ctx context.Context, req auth.CreateUserDatabaseParams) (*auth.UserDatabase, error) {
	if r.platform == nil {
		return nil, fmt.Errorf("platform api not initialized")
	}
	databaseID := "userdb-" + strings.ToLower(strings.ReplaceAll(auth.ID128(), "_", "a"))
	record, err := r.platform.CreateDatabase(ctx, platform.CreateDatabaseRequest{
		ID:         databaseID,
		Definition: req.Definition,
		UserID:     req.UserID,
	})
	if err != nil {
		return nil, err
	}
	return &auth.UserDatabase{
		ID:                record.ID,
		DefinitionID:      record.DefinitionID,
		DefinitionName:    record.DefinitionName,
		DefinitionType:    record.DefinitionType,
		DefinitionVersion: record.DefinitionVersion,
	}, nil
}

func (r authResolver) LookupDefinitionProvision(ctx context.Context, name string) (*auth.DefinitionProvisionMeta, error) {
	meta, err := r.store.LookupDefinitionProvision(ctx, name)
	if err != nil {
		return nil, err
	}
	return &auth.DefinitionProvisionMeta{
		ID:        meta.ID,
		Name:      meta.Name,
		Type:      meta.Type,
		Version:   meta.Version,
		Provision: meta.Provision,
	}, nil
}

func (r authResolver) LookupOrganizationTenant(ctx context.Context, organizationID string) (string, string, error) {
	return r.store.LookupOrganizationTenant(ctx, organizationID)
}

func (r authResolver) LookupOrganizationAuthz(ctx context.Context, organizationID string) (string, string, auth.ManagementMap, error) {
	databaseID, authToken, management, err := r.store.LookupOrganizationAuthz(ctx, organizationID)
	if err != nil {
		return "", "", nil, err
	}
	out := auth.ManagementMap{}
	for role, policy := range management {
		out[role] = auth.ManagementPolicy{
			Invite: auth.ManagementPermission{
				Any:   policy.Invite.Any,
				Roles: append([]string(nil), policy.Invite.Roles...),
			},
			AssignRole: auth.ManagementPermission{
				Any:   policy.AssignRole.Any,
				Roles: append([]string(nil), policy.AssignRole.Roles...),
			},
			RemoveMember: auth.ManagementPermission{
				Any:   policy.RemoveMember.Any,
				Roles: append([]string(nil), policy.RemoveMember.Roles...),
			},
			UpdateOrg:         policy.UpdateOrg,
			DeleteOrg:         policy.DeleteOrg,
			TransferOwnership: policy.TransferOwnership,
		}
	}
	return databaseID, authToken, out, nil
}

func (r authResolver) DeleteOrganization(ctx context.Context, organizationID string) error {
	databaseID, _, err := r.store.LookupOrganizationTenant(ctx, organizationID)
	if err != nil {
		return err
	}
	if err := deleteTursoDatabase(ctx, databaseID); err != nil {
		return err
	}
	_, err = r.store.DB().ExecContext(ctx, `DELETE FROM atombase_databases WHERE id = ?`, databaseID)
	return err
}

func deleteTursoDatabase(ctx context.Context, name string) error {
	url := fmt.Sprintf("https://api.turso.tech/v1/organizations/%s/databases/%s", config.Cfg.TursoOrganization, name)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+config.Cfg.TursoAPIKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("turso api returned %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return nil
}
