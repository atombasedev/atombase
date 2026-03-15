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
	"github.com/atombasedev/atombase/primarystore"
)

type authResolver struct {
	store *primarystore.Store
}

func (r authResolver) DB() *sql.DB {
	return r.store.DB()
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
