package auth

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/atombasedev/atombase/definitions"
	"github.com/atombasedev/atombase/tools"
)

type testUserDatabaseStore struct {
	db *sql.DB
}

func (s testUserDatabaseStore) DB() *sql.DB { return s.db }

func (s testUserDatabaseStore) CreateOrganization(ctx context.Context, req CreateOrganizationParams) (*Organization, error) {
	return nil, errors.New("not implemented")
}

func (s testUserDatabaseStore) CreateUserDatabase(ctx context.Context, req CreateUserDatabaseParams) (*UserDatabase, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO atombase_databases (id, definition_id, definition_version) VALUES (?, 1, 1)`, "user-db-"+req.UserID); err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE atombase_users SET database_id = ?, updated_at = ? WHERE id = ?`, "user-db-"+req.UserID, now, req.UserID); err != nil {
		return nil, err
	}
	return &UserDatabase{
		ID:                "user-db-" + req.UserID,
		DefinitionID:      1,
		DefinitionName:    req.Definition,
		DefinitionType:    "user",
		DefinitionVersion: 1,
	}, nil
}

func (s testUserDatabaseStore) LookupDefinitionProvision(ctx context.Context, name string) (*DefinitionProvisionMeta, error) {
	switch name {
	case "workspace":
		return &DefinitionProvisionMeta{
			ID:      1,
			Name:    name,
			Type:    definitions.DefinitionTypeUser,
			Version: 1,
			Provision: &definitions.Condition{
				Field: "auth.verified",
				Op:    "eq",
				Value: true,
			},
		}, nil
	case "blocked":
		return &DefinitionProvisionMeta{
			ID:      2,
			Name:    name,
			Type:    definitions.DefinitionTypeUser,
			Version: 1,
		}, nil
	case "orgspace":
		return &DefinitionProvisionMeta{
			ID:      3,
			Name:    name,
			Type:    definitions.DefinitionTypeOrganization,
			Version: 1,
		}, nil
	default:
		return nil, tools.ErrDefinitionNotFound
	}
}

func (s testUserDatabaseStore) LookupOrganizationTenant(ctx context.Context, organizationID string) (string, string, error) {
	return "", "", tools.ErrDatabaseNotFound
}

func (s testUserDatabaseStore) LookupOrganizationAuthz(ctx context.Context, organizationID string) (string, string, ManagementMap, error) {
	return "", "", nil, tools.ErrDatabaseNotFound
}

func (s testUserDatabaseStore) DeleteOrganization(ctx context.Context, organizationID string) error {
	return tools.ErrDatabaseNotFound
}

func setupUserDatabaseAPI(t *testing.T) *API {
	t.Helper()

	db := setupAuthTestDB(t)
	for _, stmt := range []string{
		`CREATE TABLE atombase_definitions (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			definition_type TEXT NOT NULL,
			current_version INTEGER NOT NULL
		)`,
		`CREATE TABLE atombase_databases (
			id TEXT PRIMARY KEY,
			definition_id INTEGER NOT NULL,
			definition_version INTEGER NOT NULL
		)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create user database schema: %v", err)
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(`INSERT INTO atombase_users (id, email, email_verified_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, "user-1", "user@example.com", now, now, now); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO atombase_users (id, email, created_at, updated_at) VALUES (?, ?, ?, ?)`, "user-2", "pending@example.com", now, now); err != nil {
		t.Fatalf("seed unverified user: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO atombase_definitions (id, name, definition_type, current_version) VALUES (1, 'workspace', 'user', 1)`); err != nil {
		t.Fatalf("seed definition: %v", err)
	}

	return NewAPI(testUserDatabaseStore{db: db})
}

func TestCreateUserDatabase_ProvisionedOnce(t *testing.T) {
	api := setupUserDatabaseAPI(t)

	database, err := api.createUserDatabase(context.Background(), "user-1", createUserDatabaseRequest{
		Definition: "workspace",
	})
	if err != nil {
		t.Fatalf("create user database: %v", err)
	}
	if database.DefinitionType != "user" {
		t.Fatalf("expected user definition type, got %s", database.DefinitionType)
	}

	user, err := GetUserByID("user-1", api.db, context.Background())
	if err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if user.DatabaseID == nil || *user.DatabaseID == "" {
		t.Fatalf("expected provisioned database id on user record")
	}

	if _, err := api.createUserDatabase(context.Background(), "user-1", createUserDatabaseRequest{
		Definition: "workspace",
	}); !errors.Is(err, tools.ErrDatabaseExists) {
		t.Fatalf("expected duplicate provisioning to fail, got %v", err)
	}
}

func TestCreateUserDatabase_RequiresDefinition(t *testing.T) {
	api := setupUserDatabaseAPI(t)

	if _, err := api.createUserDatabase(context.Background(), "user-1", createUserDatabaseRequest{}); err == nil {
		t.Fatalf("expected invalid request for missing definition, got %v", err)
	}
}

func TestCreateUserDatabase_DeniesWhenProvisionRuleMissing(t *testing.T) {
	api := setupUserDatabaseAPI(t)

	if _, err := api.createUserDatabase(context.Background(), "user-1", createUserDatabaseRequest{
		Definition: "blocked",
	}); !errors.Is(err, tools.ErrUnauthorized) {
		t.Fatalf("expected unauthorized when provision rule missing, got %v", err)
	}
}

func TestCreateUserDatabase_RejectsWrongDefinitionType(t *testing.T) {
	api := setupUserDatabaseAPI(t)

	if _, err := api.createUserDatabase(context.Background(), "user-1", createUserDatabaseRequest{
		Definition: "orgspace",
	}); err == nil {
		t.Fatal("expected wrong definition type to fail")
	}
}

func TestCreateUserDatabase_RequiresVerifiedEmailWhenRuleChecksVerified(t *testing.T) {
	api := setupUserDatabaseAPI(t)

	if _, err := api.createUserDatabase(context.Background(), "user-2", createUserDatabaseRequest{
		Definition: "workspace",
	}); !errors.Is(err, tools.ErrUnauthorized) {
		t.Fatalf("expected unauthorized for unverified user, got %v", err)
	}
}
